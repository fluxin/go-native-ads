package ads

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestReconnectLoopRetriesThenRecovers(t *testing.T) {
	ctx := t.Context()

	var attempts atomic.Int32
	var refreshed atomic.Int32

	conn := &Connection{
		ctx:             ctx,
		reconnectSignal: make(chan struct{}, 1),
		reconnectPolicy: ReconnectPolicy{Enabled: true, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond, MaxAttempts: 5, Jitter: 0},
		state:           connectionStateConnected,
	}
	conn.testReconnectConnectFn = func() error {
		a := attempts.Add(1)
		if a < 3 {
			return errors.New("temporary dial failure")
		}
		return nil
	}
	conn.testReconnectRefreshFn = func() {
		refreshed.Add(1)
	}

	conn.onTransportError(errors.New("boom"))

	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		conn.stateLock.Lock()
		state := conn.state
		conn.stateLock.Unlock()
		if state == connectionStateConnected && attempts.Load() >= 3 && refreshed.Load() == 1 {
			if conn.CurrentEpoch() == 0 {
				t.Fatalf("expected reconnect to increment epoch")
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("reconnect did not recover in time (attempts=%d refreshed=%d)", attempts.Load(), refreshed.Load())
}

func TestReconnectLoopStopsAtMaxAttempts(t *testing.T) {
	ctx := t.Context()

	var attempts atomic.Int32
	conn := &Connection{
		ctx:             ctx,
		reconnectSignal: make(chan struct{}, 1),
		reconnectPolicy: ReconnectPolicy{Enabled: true, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond, MaxAttempts: 2, Jitter: 0},
		state:           connectionStateConnected,
	}
	conn.testReconnectConnectFn = func() error {
		attempts.Add(1)
		return errors.New("always fails")
	}

	conn.onTransportError(errors.New("boom"))

	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		conn.stateLock.Lock()
		state := conn.state
		conn.stateLock.Unlock()
		if state == connectionStateDisconnected {
			if attempts.Load() != 2 {
				t.Fatalf("expected 2 attempts, got %d", attempts.Load())
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("expected disconnected state after max attempts, got attempts=%d", attempts.Load())
}

func TestRefreshAfterReconnectReplaysSubscriptions(t *testing.T) {
	conn := &Connection{
		symbols:              map[string]*Symbol{"MAIN.x": {FullName: "MAIN.x", Handle: 11, Length: 2, DataType: "INT"}},
		subscriptions:        map[uint64]*subscriptionSpec{},
		pendingNotifications: map[uint32][]pendingNotification{7: {}},
		activeNotifications:  map[uint32]NotificationCallback{},
		notificationToSubID:  map[uint32]uint64{},
	}
	conn.subscriptions[1] = &subscriptionSpec{
		id:         1,
		symbolName: "MAIN.x",
		mode:       TransModeServerOnChange,
		maxDelay:   100 * time.Millisecond,
		cycleTime:  100 * time.Millisecond,
		callback:   func(context.Context, uint64, []byte) error { return nil },
		adsHandle:  55,
	}
	conn.testRestoreLookupSymbolFn = func(name string) (*Symbol, error) {
		if name != "MAIN.x" {
			return nil, errors.New("unexpected symbol")
		}
		return &Symbol{FullName: "MAIN.x", Handle: 42, Length: 2, DataType: "INT"}, nil
	}
	conn.testRestoreAddNotificationFn = func(group, offset, length uint32, mode TransMode, maxDelay, cycleTime time.Duration) (uint32, error) {
		if group != uint32(GroupSymbolValueByHandle) || offset != 42 || length != 2 {
			return 0, errors.New("unexpected replay command")
		}
		return 99, nil
	}

	conn.refreshAfterReconnect()

	spec := conn.subscriptions[1]
	if spec.adsHandle != 99 {
		t.Fatalf("expected restored ads handle 99, got %d", spec.adsHandle)
	}
	if spec.offset != 42 || spec.length != 2 {
		t.Fatalf("expected restored symbol binding offset=42 len=2, got offset=%d len=%d", spec.offset, spec.length)
	}
	if got := conn.notificationToSubID[99]; got != 1 {
		t.Fatalf("expected notification->subscription mapping for restored handle, got %d", got)
	}
	if len(conn.pendingNotifications) != 0 {
		t.Fatalf("expected pending notifications reset during refresh")
	}
}

func TestHandleEnsureBoundRebindsOnEpochChange(t *testing.T) {
	conn := &Connection{
		symbols: map[string]*Symbol{
			"MAIN.x": {FullName: "MAIN.x", Handle: 77, Length: 2, DataType: "INT"},
		},
		state: connectionStateConnected,
	}
	conn.epoch.Store(2)

	h := &Handle[Int16]{
		conn:       conn,
		handle:     11,
		length:     2,
		dataType:   "INT",
		symbolName: "MAIN.x",
		symbol:     &Symbol{FullName: "MAIN.x", Handle: 11, Length: 2, DataType: "INT"},
		bindEpoch:  1,
	}

	if err := h.ensureBound(); err != nil {
		t.Fatalf("ensureBound failed: %v", err)
	}
	if h.handle != 77 || h.length != 2 || h.bindEpoch != 2 {
		t.Fatalf("handle did not rebind after epoch change: handle=%d len=%d epoch=%d", h.handle, h.length, h.bindEpoch)
	}
}

func TestUnknownNotificationCleanupPolicy(t *testing.T) {
	ctx := context.Background()
	t.Run("enabled", func(t *testing.T) {
		deleted := make(chan uint32, 1)
		conn := &Connection{
			pendingNotifications:         make(map[uint32][]pendingNotification),
			activeNotifications:          make(map[uint32]NotificationCallback),
			reconnectPolicy:              ReconnectPolicy{DeleteUnknownNotifications: true},
			testUnknownNotificationDelay: time.Millisecond,
		}
		conn.testDeleteUnknownNotificationFn = func(h uint32) error {
			deleted <- h
			return nil
		}

		if err := conn.handleNotification(ctx, 123, 1, []byte{0xAA}); err != nil {
			t.Fatalf("handleNotification failed: %v", err)
		}

		select {
		case h := <-deleted:
			if h != 123 {
				t.Fatalf("unexpected delete handle %d", h)
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("expected unknown notification delete call")
		}
	})

	t.Run("disabled", func(t *testing.T) {
		called := atomic.Bool{}
		conn := &Connection{
			pendingNotifications:         make(map[uint32][]pendingNotification),
			activeNotifications:          make(map[uint32]NotificationCallback),
			reconnectPolicy:              ReconnectPolicy{DeleteUnknownNotifications: false},
			testUnknownNotificationDelay: time.Millisecond,
		}
		conn.testDeleteUnknownNotificationFn = func(h uint32) error {
			called.Store(true)
			return nil
		}

		if err := conn.handleNotification(ctx, 321, 1, []byte{0xBB}); err != nil {
			t.Fatalf("handleNotification failed: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
		if called.Load() {
			t.Fatalf("unexpected delete call when cleanup policy is disabled")
		}
	})

	t.Run("registered during grace", func(t *testing.T) {
		called := atomic.Bool{}
		conn := &Connection{
			pendingNotifications:         make(map[uint32][]pendingNotification),
			activeNotifications:          make(map[uint32]NotificationCallback),
			reconnectPolicy:              ReconnectPolicy{DeleteUnknownNotifications: true},
			testUnknownNotificationDelay: 20 * time.Millisecond,
		}
		conn.testDeleteUnknownNotificationFn = func(h uint32) error {
			called.Store(true)
			return nil
		}

		if err := conn.handleNotification(ctx, 456, 1, []byte{0xCC}); err != nil {
			t.Fatalf("handleNotification failed: %v", err)
		}
		conn.symbolLock.Lock()
		conn.activeNotifications[456] = func(context.Context, uint64, []byte) error { return nil }
		conn.symbolLock.Unlock()

		time.Sleep(50 * time.Millisecond)
		if called.Load() {
			t.Fatalf("unexpected delete call for handle registered during cleanup grace")
		}
	})
}
