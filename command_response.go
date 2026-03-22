package ads

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

type readWriteResponseHeader struct {
	Error  ReturnCode
	Length uint32
}

func parseReadWritePayloadResponse(op string, resp []byte, expectedLength *uint32) ([]byte, error) {
	respBuf := bytes.NewBuffer(resp)
	var header readWriteResponseHeader
	if err := binary.Read(respBuf, binary.LittleEndian, &header); err != nil {
		return nil, fmt.Errorf("failed to parse %s response header: %w", op, err)
	}
	if header.Error != ReturnCodeNoErrors {
		return nil, fmt.Errorf("ADS error %d in %s", header.Error, op)
	}
	payload := respBuf.Bytes()
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
		return fmt.Errorf("ADS error %d in %s", adsErr, op)
	}
	return nil
}

func parseErrorWithFixedPayload(op string, resp []byte, payloadSize int) ([]byte, error) {
	const errorSize = 4
	expectedLen := errorSize + payloadSize
	if len(resp) != expectedLen {
		return nil, fmt.Errorf("invalid %s response length: expected=%d actual=%d", op, expectedLen, len(resp))
	}
	adsErr := ReturnCode(binary.LittleEndian.Uint32(resp[:errorSize]))
	if adsErr != ReturnCodeNoErrors {
		return nil, fmt.Errorf("ADS error %d in %s", adsErr, op)
	}
	return resp[errorSize:], nil
}
