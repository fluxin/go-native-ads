package ads

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log/slog"
)

// Write - ADS command id: 3
func (conn *Connection) Write(group uint32, offset uint32, data []byte) error {
	return conn.write(group, offset, data, false)
}
func (conn *Connection) write(group, offset uint32, data []byte, internal bool) error {
	if uint64(len(data))+44 > uint64(conn.frameLimit()) {
		return fmt.Errorf("write exceeds frame limit")
	}
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
	resp, err := conn.request(CommandIDWrite, request.Bytes(), internal)
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
