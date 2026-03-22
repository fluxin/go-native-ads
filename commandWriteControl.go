package ads

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

type writeControlRequestHeader struct {
	AdsState    uint16
	DeviceState uint16
	Length      uint32
}

func encodeWriteControlRequest(adsState AdsState, deviceState uint16, data []byte) ([]byte, error) {
	request := bytes.NewBuffer(make([]byte, 0, 8+len(data)))
	header := writeControlRequestHeader{
		AdsState:    uint16(adsState),
		DeviceState: deviceState,
		Length:      uint32(len(data)),
	}
	if err := binary.Write(request, binary.LittleEndian, header); err != nil {
		return nil, fmt.Errorf("failed to encode write-control header: %w", err)
	}
	if len(data) > 0 {
		if err := binary.Write(request, binary.LittleEndian, data); err != nil {
			return nil, fmt.Errorf("failed to encode write-control data: %w", err)
		}
	}
	return request.Bytes(), nil
}

func (conn *Connection) writeControl(adsState AdsState, deviceState uint16, data []byte) error {
	conn.waitGroup.Add(1)
	defer conn.waitGroup.Done()

	request, err := encodeWriteControlRequest(adsState, deviceState, data)
	if err != nil {
		return err
	}

	resp, err := conn.sendRequest(CommandIDWriteControl, request)
	if err != nil {
		return fmt.Errorf("write-control request failed: %w", err)
	}
	if err := parseErrorOnlyResponse("WriteControl", resp); err != nil {
		return err
	}
	return nil
}
