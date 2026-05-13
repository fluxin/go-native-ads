package ads

import (
	"context"
	"errors"
	"net"
	"os"
	"runtime"
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
	ctx := t.Context()

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

func TestConnectionDialTargetTransportModes(t *testing.T) {
	conn := &Connection{
		ip:         "192.0.2.10",
		port:       48898,
		local:      true,
		transport:  ConnectionTransportTCP,
		unixSocket: "/tmp/does-not-matter.sock",
	}
	network, address := conn.dialTarget()
	if network != "tcp" || address != "192.0.2.10:48898" {
		t.Fatalf("forced TCP dial target mismatch: %s %s", network, address)
	}

	conn.transport = ConnectionTransportUnix
	network, address = conn.dialTarget()
	if network != "unix" || address != conn.unixSocket {
		t.Fatalf("forced unix dial target mismatch: %s %s", network, address)
	}
}

func TestConnectionAutoTransportFallsBackToTCP(t *testing.T) {
	conn := &Connection{
		ip:         "127.0.0.1",
		port:       48898,
		local:      true,
		transport:  ConnectionTransportAuto,
		unixSocket: "/tmp/go-native-ads-missing.sock",
	}
	network, address := conn.dialTarget()
	if network != "tcp" || address != "127.0.0.1:48898" {
		t.Fatalf("auto fallback dial target mismatch: %s %s", network, address)
	}
}

func TestConnectionAutoTransportUsesExistingLinuxSocket(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("auto unix socket selection is linux-only")
	}
	path := t.TempDir() + "/ams.sock"
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("test path is not a unix socket: %s", path)
	}

	conn := &Connection{
		ip:         "127.0.0.1",
		port:       48898,
		local:      true,
		transport:  ConnectionTransportAuto,
		unixSocket: path,
	}
	network, address := conn.dialTarget()
	if network != "unix" || address != path {
		t.Fatalf("auto unix dial target mismatch: %s %s", network, address)
	}
}

func TestNewConnectionRejectsInvalidTransport(t *testing.T) {
	_, err := NewConnection(context.Background(), ConnectionOptions{Transport: ConnectionTransport("bluetooth")})
	if err == nil {
		t.Fatalf("expected invalid transport error")
	}
}
