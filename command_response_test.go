package ads

import "testing"

func TestParseErrorOnlyResponse(t *testing.T) {
	if err := parseErrorOnlyResponse("Write", []byte{0, 0, 0, 0}); err != nil {
		t.Fatalf("unexpected error for success response: %v", err)
	}

	if err := parseErrorOnlyResponse("Write", []byte{1, 0, 0, 0}); err == nil {
		t.Fatalf("expected ADS error")
	}

	if err := parseErrorOnlyResponse("Write", []byte{0, 0, 0}); err == nil {
		t.Fatalf("expected length error")
	}
}

func TestParseReadWritePayloadResponse(t *testing.T) {
	payload := []byte{0x11, 0x22}
	resp := mkReadWriteResp(t, ReturnCodeNoErrors, payload)

	expected := uint32(2)
	got, err := parseReadWritePayloadResponse("Read", resp, &expected)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != len(payload) || got[0] != payload[0] || got[1] != payload[1] {
		t.Fatalf("unexpected payload: %v", got)
	}

	wrongExpected := uint32(3)
	if _, err := parseReadWritePayloadResponse("Read", resp, &wrongExpected); err == nil {
		t.Fatalf("expected mismatch error")
	}
}

func TestParseErrorWithFixedPayload(t *testing.T) {
	resp := []byte{0, 0, 0, 0, 0xAA, 0xBB, 0xCC, 0xDD}
	payload, err := parseErrorWithFixedPayload("ReadState", resp, 4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(payload) != 4 || payload[0] != 0xAA {
		t.Fatalf("unexpected payload: %v", payload)
	}

	if _, err := parseErrorWithFixedPayload("ReadState", resp, 3); err == nil {
		t.Fatalf("expected fixed-size mismatch")
	}
}
