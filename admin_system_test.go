package ads

import (
	"encoding/binary"
	"testing"
)

func TestEncodeWriteControlRequest(t *testing.T) {
	packet, err := encodeWriteControlRequest(AdsStateRun, 7, []byte{0xAA, 0xBB})
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	if len(packet) != 10 {
		t.Fatalf("unexpected packet length: %d", len(packet))
	}
	if got := binary.LittleEndian.Uint16(packet[0:2]); got != uint16(AdsStateRun) {
		t.Fatalf("ads state mismatch: %d", got)
	}
	if got := binary.LittleEndian.Uint16(packet[2:4]); got != 7 {
		t.Fatalf("device state mismatch: %d", got)
	}
	if got := binary.LittleEndian.Uint32(packet[4:8]); got != 2 {
		t.Fatalf("payload length mismatch: %d", got)
	}
	if packet[8] != 0xAA || packet[9] != 0xBB {
		t.Fatalf("payload mismatch: %x", packet[8:10])
	}
}
