package ads

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log/slog"
)

// Write - ADS command id: 3
func (conn *Connection) Write(group uint32, offset uint32, data []byte) error {
	conn.waitGroup.Add(1)
	defer conn.waitGroup.Done()
	type writeCommandPacket struct {
		Group  uint32
		Offset uint32
		Length uint32
	}
	request := new(bytes.Buffer)
	writeRequest := writeCommandPacket{
		group,
		offset,
		uint32(binary.Size(data)),
	}

	err := binary.Write(request, binary.LittleEndian, writeRequest)
	if err != nil {
		slog.Error("binary.Write failed", "error", err)
		return fmt.Errorf("failed to write request header: %w", err)
	}
	err = binary.Write(request, binary.LittleEndian, data)
	if err != nil {
		slog.Error("binary.Write failed", "error", err)
		return fmt.Errorf("failed to write request data: %w", err)
	}

	// Try to send the request
	resp, err := conn.sendRequest(CommandIDWrite, request.Bytes())
	if err != nil {
		slog.Error("error during send request for write", "error", err)
		return fmt.Errorf("write request failed: %w", err)
	}
	if err := parseErrorOnlyResponse("Write", resp); err != nil {
		slog.Error("error during write", "error", err)
		return err
	}

	return nil
}
