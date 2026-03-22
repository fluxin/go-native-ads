package ads

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log/slog"
)

// ReadDeviceInfoResponse connected device info

// DeviceInfo connected device info
type DeviceInfo struct {
	Major      uint8
	Minor      uint8
	Version    uint16
	DeviceName [16]byte
}

func (conn *Connection) ReadDeviceInfo() (response DeviceInfo, err error) {
	conn.waitGroup.Add(1)
	defer conn.waitGroup.Done()
	// Try to send the request
	resp, err := conn.sendRequest(CommandIDReadDeviceInfo, []byte{})
	if err != nil {
		return
	}

	payload, err := parseErrorWithFixedPayload("ReadDeviceInfo", resp, 20)
	if err != nil {
		return response, err
	}
	respBuffer := bytes.NewBuffer(payload)
	if err := binary.Read(respBuffer, binary.LittleEndian, &response); err != nil {
		return response, fmt.Errorf("failed to parse ReadDeviceInfo payload: %w", err)
	}

	slog.Debug("Device Info")

	return response, nil
}
