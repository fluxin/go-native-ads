package ads

import "testing"

func TestParseDeleteDeviceNotificationResponse(t *testing.T) {
	resp := []byte{0x14, 0x07, 0x00, 0x00}
	code, err := parseDeleteDeviceNotificationResponse(resp)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if code != ReturnCodeDeviceNotifyHandleInvalid {
		t.Fatalf("expected notify-handle-invalid code, got %d", code)
	}
}
