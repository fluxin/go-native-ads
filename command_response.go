package ads

import (
	"encoding/binary"
	"fmt"
)

type readWriteResponseHeader struct {
	Error  ReturnCode
	Length uint32
}

func parseReadWritePayloadResponse(op string, resp []byte, expectedLength *uint32) ([]byte, error) {
	if len(resp) < 4 {
		return nil, fmt.Errorf("short %s response", op)
	}
	code := ReturnCode(binary.LittleEndian.Uint32(resp))
	if code != ReturnCodeNoErrors {
		return nil, &ProtocolError{Operation: op, Code: code}
	}
	if len(resp) < 8 {
		return nil, fmt.Errorf("short %s response header", op)
	}
	header := readWriteResponseHeader{Length: binary.LittleEndian.Uint32(resp[4:])}
	payload := resp[8:]
	if header.Length != uint32(len(payload)) {
		return nil, fmt.Errorf("invalid %s payload length: header=%d actual=%d", op, header.Length, len(payload))
	}
	if expectedLength != nil && header.Length != *expectedLength {
		return nil, fmt.Errorf("unexpected %s payload length: expected=%d actual=%d", op, *expectedLength, header.Length)
	}
	return payload, nil
}

func parseErrorOnlyResponse(op string, resp []byte) error {
	const expectedLen = 4
	if len(resp) != expectedLen {
		return fmt.Errorf("invalid %s response length: expected=%d actual=%d", op, expectedLen, len(resp))
	}
	adsErr := ReturnCode(binary.LittleEndian.Uint32(resp))
	if adsErr != ReturnCodeNoErrors {
		return &ProtocolError{Operation: op, Code: adsErr}
	}
	return nil
}

func parseErrorWithFixedPayload(op string, resp []byte, payloadSize int) ([]byte, error) {
	const errorSize = 4
	if len(resp) < errorSize {
		return nil, fmt.Errorf("short %s response", op)
	}
	code := ReturnCode(binary.LittleEndian.Uint32(resp))
	if code != ReturnCodeNoErrors {
		return nil, &ProtocolError{Operation: op, Code: code}
	}
	expectedLen := errorSize + payloadSize
	if len(resp) != expectedLen {
		return nil, fmt.Errorf("invalid %s response length: expected=%d actual=%d", op, expectedLen, len(resp))
	}
	return resp[errorSize:], nil
}
