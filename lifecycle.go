package ads

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"math/rand"
	"net"
	"sync/atomic"
	"time"
)

type connectionState uint8

const (
	connectionStateDisconnected connectionState = iota
	connectionStateConnecting
	connectionStateConnected
	connectionStateReconnecting
	connectionStateClosed
)

type ReconnectPolicy struct {
	Enabled                    bool
	InitialBackoff             time.Duration
	MaxBackoff                 time.Duration
	MaxAttempts                int
	Jitter                     float64
	DeleteUnknownNotifications bool
}

func DefaultReconnectPolicy() ReconnectPolicy {
	return ReconnectPolicy{
		Enabled:                    false,
		InitialBackoff:             500 * time.Millisecond,
		MaxBackoff:                 10 * time.Second,
		MaxAttempts:                0,
		Jitter:                     0.2,
		DeleteUnknownNotifications: true,
	}
}

func normalizeReconnectPolicy(policy ReconnectPolicy) ReconnectPolicy {
	if policy == (ReconnectPolicy{}) {
		return DefaultReconnectPolicy()
	}
	if policy.MaxBackoff <= 0 {
		policy.MaxBackoff = 10 * time.Second
	}
	if policy.InitialBackoff <= 0 {
		policy.InitialBackoff = 500 * time.Millisecond
	}
	if policy.MaxBackoff < policy.InitialBackoff {
		policy.MaxBackoff = policy.InitialBackoff
	}
	if policy.Jitter < 0 {
		policy.Jitter = 0
	}
	if policy.Jitter > 1 {
		policy.Jitter = 1
	}
	return policy
}

func (conn *Connection) CurrentEpoch() uint64 {
	return conn.epoch.Load()
}

func (conn *Connection) reconnectPolicySnapshot() ReconnectPolicy {
	conn.stateLock.Lock()
	defer conn.stateLock.Unlock()
	return conn.reconnectPolicy
}

var ErrNotConnected = errors.New("connection is not ready")

func (conn *Connection) setState(state connectionState) {
	conn.stateLock.Lock()
	defer conn.stateLock.Unlock()
	if conn.state != connectionStateClosed {
		conn.state = state
	}
}
func (conn *Connection) publishConnected() error {
	conn.stateLock.Lock()
	defer conn.stateLock.Unlock()
	if conn.ctx.Err() != nil || conn.state == connectionStateClosed {
		return net.ErrClosed
	}
	if conn.testReconnectConnectFn == nil {
		conn.transportLock.Lock()
		ctx := conn.transportCtx
		conn.transportLock.Unlock()
		if ctx == nil || ctx.Err() != nil {
			return net.ErrClosed
		}
	}
	conn.state = connectionStateConnected
	return nil
}

// Add and Wait are serialized with the transition to Closed.
func (conn *Connection) startBackground(fn func()) bool {
	conn.stateLock.Lock()
	defer conn.stateLock.Unlock()
	if conn.state == connectionStateClosed || (conn.ctx != nil && conn.ctx.Err() != nil) {
		return false
	}
	conn.backgroundGroup.Add(1)
	go func() { defer conn.backgroundGroup.Done(); fn() }()
	return true
}
func (conn *Connection) onTransportError(err error) {
	if err == nil || conn.ctx.Err() != nil {
		return
	}
	conn.transportLock.Lock()
	if conn.transportCancel != nil {
		conn.transportCancel()
	}
	if conn.connection != nil {
		_ = conn.connection.Close()
	}
	conn.transportLock.Unlock()
	conn.stateLock.Lock()
	if conn.state == connectionStateClosed {
		conn.stateLock.Unlock()
		return
	}
	enabled := conn.reconnectPolicy.Enabled
	conn.state = connectionStateDisconnected
	if enabled {
		conn.state = connectionStateReconnecting
	}
	conn.stateLock.Unlock()
	if !enabled {
		return
	}
	select {
	case conn.reconnectSignal <- struct{}{}:
	default:
	}
	if conn.reconnectRunning.CompareAndSwap(false, true) {
		if !conn.startBackground(conn.reconnectLoop) {
			conn.reconnectRunning.Store(false)
		}
	}
}
func (conn *Connection) reconnectLoop() {
	defer conn.reconnectRunning.Store(false)
	for {
		select {
		case <-conn.ctx.Done():
			return
		case <-conn.reconnectSignal:
		}
		policy := conn.reconnectPolicySnapshot()
		for attempt := 1; policy.MaxAttempts <= 0 || attempt <= policy.MaxAttempts; attempt++ {
			if conn.ctx.Err() != nil {
				return
			}
			conn.connectLock.Lock()
			conn.generationLock.Lock()
			conn.stateLock.Lock()
			alreadyConnected := conn.state == connectionStateConnected
			conn.stateLock.Unlock()
			if alreadyConnected {
				conn.generationLock.Unlock()
				conn.connectLock.Unlock()
				break
			}
			conn.setState(connectionStateReconnecting)
			conn.stopTransport()
			var err error
			if conn.testReconnectConnectFn != nil {
				err = conn.testReconnectConnectFn()
			} else {
				err = conn.connectWithMetadata()
			}
			if err == nil {
				if conn.testReconnectRefreshFn != nil {
					conn.testReconnectRefreshFn()
				} else {
					conn.refreshAfterReconnect()
				}
				conn.epoch.Add(1)
				err = conn.publishConnected()
			}
			if err != nil {
				conn.setState(connectionStateReconnecting)
			}
			// Errors while bootstrapping are handled by this retry, not a second reconnect.
			select {
			case <-conn.reconnectSignal:
			default:
			}
			conn.generationLock.Unlock()
			conn.connectLock.Unlock()
			if err == nil {
				break
			}
			slog.Debug("ADS reconnect failed", "attempt", attempt, "error", err)
			timer := time.NewTimer(jitteredBackoff(policy, attempt))
			select {
			case <-timer.C:
			case <-conn.ctx.Done():
				timer.Stop()
				return
			}
		}
		conn.stateLock.Lock()
		if conn.state == connectionStateReconnecting {
			conn.state = connectionStateDisconnected
		}
		conn.stateLock.Unlock()
	}
}

func jitteredBackoff(policy ReconnectPolicy, attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	base := float64(policy.InitialBackoff) * math.Pow(2, float64(attempt-1))
	if base > float64(policy.MaxBackoff) {
		base = float64(policy.MaxBackoff)
	}
	if policy.Jitter <= 0 {
		return time.Duration(base)
	}
	spread := base * policy.Jitter
	delta := (rand.Float64() * 2 * spread) - spread
	return time.Duration(base + delta)
}

func (conn *Connection) refreshAfterReconnect() {
	conn.symbolLock.Lock()
	conn.resetNotificationStateLocked()
	subs := conn.subscriptionSnapshotLocked()
	conn.symbolLock.Unlock()
	conn.restoreSubscriptions(subs)
	conn.ensureSymbolVersionWatcher()
}

func (conn *Connection) ensureConnected() error {
	conn.stateLock.Lock()
	state := conn.state
	conn.stateLock.Unlock()
	if state == connectionStateConnected {
		return nil
	}
	if state == connectionStateClosed {
		return net.ErrClosed
	}
	return ErrNotConnected
}

// The generation read lock pins schema and binding information for a typed operation.
func (conn *Connection) beginOperation() error {
	conn.generationLock.RLock()
	if err := conn.ensureConnected(); err != nil {
		conn.generationLock.RUnlock()
		return err
	}
	return nil
}
func (conn *Connection) endOperation() { conn.generationLock.RUnlock() }

type subscriptionSpec struct {
	id         uint64
	symbolName string
	group      uint32
	offset     uint32
	length     uint32
	mode       TransMode
	maxDelay   time.Duration
	cycleTime  time.Duration
	callback   NotificationCallback
	adsHandle  uint32
	factory    func(*Symbol, map[string]SymbolUploadDataType) (NotificationCallback, error)
	delivery   *notificationDelivery
	err        error
	dropped    atomic.Uint64
	cancel     context.CancelFunc
	ctx        context.Context
}
