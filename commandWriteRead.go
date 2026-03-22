package ads

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log/slog"
)

func (conn *Connection) WriteRead(group uint32, offset uint32, readLength uint32, send []byte) (data []byte, err error) {
	conn.waitGroup.Add(1)
	defer conn.waitGroup.Done()
	request := bytes.NewBuffer([]byte{})
	type writeReadCommandPacket struct {
		Group       uint32
		Offset      uint32
		ReadLength  uint32
		WriteLength uint32
	}
	var content = writeReadCommandPacket{
		group,
		offset,
		readLength,
		uint32(len(send)),
	}

	err = binary.Write(request, binary.LittleEndian, content)
	if err != nil {
		slog.Error("binary.Write failed", "error", err)
		return nil, fmt.Errorf("failed to write request header: %w", err)
	}
	err = binary.Write(request, binary.LittleEndian, send)
	if err != nil {
		slog.Error("binary.Write failed", "error", err)
		return nil, fmt.Errorf("failed to write request data: %w", err)
	}

	slog.Debug("Request", "request", request.Bytes())

	// Try to send the request
	resp, err := conn.sendRequest(CommandIDReadWrite, request.Bytes())
	if err != nil {
		return nil, fmt.Errorf("write-read request failed: %w", err)
	}

	payload, err := parseReadWritePayloadResponse("WriteRead", resp, &readLength)
	if err != nil {
		return nil, err
	}
	return payload, nil
}
