package ads

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNormalizeReconnectPolicy(t *testing.T) {
	policy := normalizeReconnectPolicy(ReconnectPolicy{
		Enabled:        true,
		InitialBackoff: -1,
		MaxBackoff:     0,
		Jitter:         3,
	})
	if !policy.Enabled {
		t.Fatalf("expected reconnect enabled")
	}
	if policy.InitialBackoff <= 0 {
		t.Fatalf("expected positive initial backoff")
	}
	if policy.MaxBackoff < policy.InitialBackoff {
		t.Fatalf("expected max backoff >= initial backoff")
	}
	if policy.Jitter < 0 || policy.Jitter > 1 {
		t.Fatalf("expected jitter to be clamped to [0,1], got %f", policy.Jitter)
	}
}

func TestJitteredBackoffCappedWithoutJitter(t *testing.T) {
	policy := ReconnectPolicy{
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     350 * time.Millisecond,
		Jitter:         0,
	}
	if got := jitteredBackoff(policy, 1); got != 100*time.Millisecond {
		t.Fatalf("attempt 1 mismatch: %s", got)
	}
	if got := jitteredBackoff(policy, 2); got != 200*time.Millisecond {
		t.Fatalf("attempt 2 mismatch: %s", got)
	}
	if got := jitteredBackoff(policy, 3); got != 350*time.Millisecond {
		t.Fatalf("attempt 3 should clamp to max: %s", got)
	}
}

func TestOnTransportErrorDisabledReconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conn := &Connection{
		ctx:             ctx,
		reconnectSignal: make(chan struct{}, 1),
		reconnectPolicy: ReconnectPolicy{Enabled: false},
		state:           connectionStateConnected,
	}

	conn.onTransportError(errors.New("boom"))

	conn.stateLock.Lock()
	state := conn.state
	conn.stateLock.Unlock()
	if state != connectionStateDisconnected {
		t.Fatalf("expected disconnected state, got %v", state)
	}
	select {
	case <-conn.reconnectSignal:
		t.Fatalf("unexpected reconnect signal when reconnect is disabled")
	default:
	}
}

func TestRouterConnectRequestPacket(t *testing.T) {
	expected := []byte{0x00, 0x10, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00}
	packet := buildRouterPortConnectPacket(0)
	if len(packet) != len(expected) {
		t.Fatalf("packet length mismatch: %d", len(packet))
	}
	for i := range expected {
		if packet[i] != expected[i] {
			t.Fatalf("packet byte mismatch at %d: got 0x%02x, want 0x%02x", i, packet[i], expected[i])
		}
	}
}
