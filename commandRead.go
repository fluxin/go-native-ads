package ads

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log/slog"
)

func (conn *Connection) Read(group uint32, offset uint32, length uint32) (data []byte, err error) {
	return conn.read(group, offset, length, false)
}
func (conn *Connection) read(group, offset, length uint32, internal bool) (data []byte, err error) {
	if uint64(length)+40 > uint64(conn.frameLimit()) {
		return nil, fmt.Errorf("read exceeds frame limit")
	}
	request := bytes.NewBuffer([]byte{})
	type readCommandPacket struct {
		Group  uint32
		Offset uint32
		Length uint32
	}
	var content = readCommandPacket{
		group,
		offset,
		length,
	}

	// Read	- ADS command id: 2
	err = binary.Write(request, binary.LittleEndian, content)

	slog.Debug("Request", "request", content)

	if err != nil {
		slog.Error("binary.Write failed", "error", err)
		return nil, fmt.Errorf("failed to encode read request: %w", err)
	}

	// Try to send the request
	resp, err := conn.request(CommandIDRead, request.Bytes(), internal)
	if err != nil {
		slog.Error("send request failed", "error", err)
		return nil, fmt.Errorf("read request failed: %w", err)
	}

	payload, err := parseReadWritePayloadResponse("Read", resp, &length)
	if err != nil {
		return nil, err
	}
	return payload, nil
}
