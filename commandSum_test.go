package ads

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func mkReadWriteResp(t *testing.T, errorCode ReturnCode, payload []byte) []byte {
	t.Helper()
	b := &bytes.Buffer{}
	header := readWriteResponseHeader{Error: errorCode, Length: uint32(len(payload))}
	if err := binary.Write(b, binary.LittleEndian, header); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if _, err := b.Write(payload); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	return b.Bytes()
}

func TestParseSumReadResponseMixedErrors(t *testing.T) {
	commands := []sumReadSubCommand{
		{Length: 2},
		{Length: 4},
	}

	payload := &bytes.Buffer{}
	_ = binary.Write(payload, binary.LittleEndian, ReturnCodeNoErrors)
	_ = binary.Write(payload, binary.LittleEndian, ReturnCodeDeviceInvalidOffset)
	payload.Write([]byte{0x11, 0x22})
	payload.Write([]byte{0xAA, 0xBB, 0xCC, 0xDD})

	resp := mkReadWriteResp(t, ReturnCodeNoErrors, payload.Bytes())
	results, err := parseSumReadResponse(commands, resp)
	if err != nil {
		t.Fatalf("parseSumReadResponse error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results)=%d, want 2", len(results))
	}
	if results[0].Error != ReturnCodeNoErrors {
		t.Fatalf("result[0].Error=%d, want 0", results[0].Error)
	}
	if !bytes.Equal(results[0].Data, []byte{0x11, 0x22}) {
		t.Fatalf("result[0].Data=%v, want [17 34]", results[0].Data)
	}
	if results[1].Error != ReturnCodeDeviceInvalidOffset {
		t.Fatalf("result[1].Error=%d, want %d", results[1].Error, ReturnCodeDeviceInvalidOffset)
	}
	if len(results[1].Data) != 0 {
		t.Fatalf("result[1].Data expected empty, got %v", results[1].Data)
	}
}

func TestParseSumReadResponseLengthMismatch(t *testing.T) {
	commands := []sumReadSubCommand{{Length: 2}}
	payload := &bytes.Buffer{}
	_ = binary.Write(payload, binary.LittleEndian, ReturnCodeNoErrors)
	payload.Write([]byte{0x10})

	resp := mkReadWriteResp(t, ReturnCodeNoErrors, payload.Bytes())
	_, err := parseSumReadResponse(commands, resp)
	if err == nil {
		t.Fatalf("expected length mismatch error")
	}
}

func TestParseSumWriteResponse(t *testing.T) {
	commands := []sumWriteSubCommand{{}, {}}
	payload := &bytes.Buffer{}
	_ = binary.Write(payload, binary.LittleEndian, ReturnCodeNoErrors)
	_ = binary.Write(payload, binary.LittleEndian, ReturnCodeDeviceBusy)

	resp := mkReadWriteResp(t, ReturnCodeNoErrors, payload.Bytes())
	results, err := parseSumWriteResponse(commands, resp)
	if err != nil {
		t.Fatalf("parseSumWriteResponse error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results)=%d, want 2", len(results))
	}
	if results[0].Error != ReturnCodeNoErrors || results[1].Error != ReturnCodeDeviceBusy {
		t.Fatalf("unexpected errors: %+v", results)
	}
}
