package ads

import "testing"

func TestParseSingleBytePayload(t *testing.T) {
	v, err := parseSingleBytePayload("x", []byte{7})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != 7 {
		t.Fatalf("unexpected value: %d", v)
	}
}

func TestParseSingleBytePayloadLengthError(t *testing.T) {
	if _, err := parseSingleBytePayload("x", []byte{1, 2}); err == nil {
		t.Fatalf("expected payload length error")
	}
}

func TestResetNotificationStateLocked(t *testing.T) {
	conn := &Connection{
		pendingNotifications: map[uint32][]pendingNotification{1: {}},
		activeNotifications:  map[uint32]NotificationCallback{2: nil},
		notificationToSubID:  map[uint32]uint64{2: 9},
		subscriptions: map[uint64]*subscriptionSpec{
			9: {adsHandle: 2, group: 123},
		},
		symbols:                  map[string]*Symbol{"A": {Handle: 55}},
		symbolVersionWatchHandle: 88,
	}

	conn.resetNotificationStateLocked()

	if len(conn.pendingNotifications) != 0 || len(conn.activeNotifications) != 0 || len(conn.notificationToSubID) != 0 {
		t.Fatalf("notification maps were not reset")
	}
	if conn.symbolVersionWatchHandle != 0 {
		t.Fatalf("watch handle should be reset")
	}
	if conn.subscriptions[9].adsHandle != 0 {
		t.Fatalf("subscription ads handle should be reset")
	}
	if conn.subscriptions[9].group != uint32(GroupSymbolValueByHandle) {
		t.Fatalf("subscription group should be normalized")
	}
	if conn.symbols["A"].Handle != 0 {
		t.Fatalf("symbol handles should be reset")
	}
}
