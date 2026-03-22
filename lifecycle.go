package ads

import (
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"net"
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

func (conn *Connection) onTransportError(err error) {
	if err == nil {
		return
	}
	if conn.ctx.Err() != nil {
		return
	}
	conn.stateLock.Lock()
	if conn.state == connectionStateClosed {
		conn.stateLock.Unlock()
		return
	}
	policy := conn.reconnectPolicy
	if !policy.Enabled {
		conn.state = connectionStateDisconnected
		conn.stateLock.Unlock()
		return
	}
	conn.stateLock.Unlock()
	select {
	case conn.reconnectSignal <- struct{}{}:
	default:
	}
	if conn.reconnectRunning.CompareAndSwap(false, true) {
		go conn.reconnectLoop()
	}
}

func (conn *Connection) reconnectLoop() {
	defer conn.reconnectRunning.Store(false)
	for {
		select {
		case <-conn.ctx.Done():
			return
		case <-conn.reconnectSignal:
			conn.stateLock.Lock()
			if conn.state == connectionStateClosed {
				conn.stateLock.Unlock()
				return
			}
			conn.state = connectionStateReconnecting
			policy := conn.reconnectPolicy
			conn.stateLock.Unlock()

			attempt := 0
			for {
				if conn.ctx.Err() != nil {
					return
				}
				attempt++
				if policy.MaxAttempts > 0 && attempt > policy.MaxAttempts {
					slog.Error("reconnect stopped after max attempts", "attempts", attempt-1)
					conn.stateLock.Lock()
					if conn.state != connectionStateClosed {
						conn.state = connectionStateDisconnected
					}
					conn.stateLock.Unlock()
					break
				}

				if conn.connection != nil {
					_ = conn.connection.Close()
				}

				conn.connectLock.Lock()
				err := conn.reconnectConnectStep()
				if err == nil {
					conn.reconnectRefreshStep()
					conn.stateLock.Lock()
					conn.state = connectionStateConnected
					conn.stateLock.Unlock()
					conn.epoch.Add(1)
					conn.connectLock.Unlock()
					slog.Info("ADS connection re-established", "attempt", attempt)
					break
				}
				conn.connectLock.Unlock()
				slog.Warn("ADS reconnect failed", "attempt", attempt, "error", err)

				backoff := jitteredBackoff(policy, attempt)
				select {
				case <-time.After(backoff):
				case <-conn.ctx.Done():
					return
				}
			}
		}
	}
}

func (conn *Connection) reconnectConnectStep() error {
	if conn.testReconnectConnectFn != nil {
		return conn.testReconnectConnectFn()
	}
	return conn.connectWithMetadata()
}

func (conn *Connection) reconnectRefreshStep() {
	if conn.testReconnectRefreshFn != nil {
		conn.testReconnectRefreshFn()
		return
	}
	conn.refreshAfterReconnect()
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
	conn.cleanupActiveNotificationHandles()
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
	policy := conn.reconnectPolicy
	conn.stateLock.Unlock()

	if state == connectionStateConnected {
		return nil
	}
	if state == connectionStateConnecting {
		return nil
	}
	if state == connectionStateReconnecting {
		return nil
	}
	if state == connectionStateClosed {
		return fmt.Errorf("connection is closed")
	}
	if !policy.Enabled {
		return fmt.Errorf("connection is not active")
	}

	conn.onTransportError(net.ErrClosed)
	deadline := time.Now().Add(policy.MaxBackoff + policy.InitialBackoff)
	for time.Now().Before(deadline) {
		conn.stateLock.Lock()
		if conn.state == connectionStateConnected {
			conn.stateLock.Unlock()
			return nil
		}
		conn.stateLock.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("connection is reconnecting")
}

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
}
