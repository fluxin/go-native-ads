package ads

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log/slog"
)

func parseSumWriteResponse(commands []sumWriteSubCommand, resp []byte) ([]sumWriteSubResult, error) {
	payload, err := parseReadWritePayloadResponse("SumWrite", resp, nil)
	if err != nil {
		return nil, err
	}

	expectedLen := len(commands) * 4
	if len(payload) != expectedLen {
		return nil, fmt.Errorf("invalid SumWrite payload size: expected=%d actual=%d", expectedLen, len(payload))
	}

	results := make([]sumWriteSubResult, len(commands))
	for i := range commands {
		errorCode := ReturnCode(binary.LittleEndian.Uint32(payload[i*4 : i*4+4]))
		results[i].Error = errorCode
	}
	return results, nil
}

// sumWriteSubCommand represents a single write operation in a sum command
type SumWriteCommand struct {
	Group  uint32
	Offset uint32
	Length uint32
	Data   []byte
}

// sumWriteSubResult represents the result of a single write operation
type SumWriteResult struct {
	Error ReturnCode
}

func (conn *Connection) SumWrite(commands []SumWriteCommand) ([]SumWriteResult, error) {
	return conn.sumWrite(commands, false)
}
func (conn *Connection) sumWrite(commands []sumWriteSubCommand, internal bool) ([]sumWriteSubResult, error) {

	const maxCommands = 500
	if len(commands) == 0 {
		return nil, fmt.Errorf("empty sum request")
	}
	if len(commands) > maxCommands {
		return nil, fmt.Errorf("batch size %d exceeds maximum %d", len(commands), maxCommands)
	}

	// Calculate total data length
	totalDataLength := uint64(0)
	for _, cmd := range commands {
		totalDataLength += uint64(len(cmd.Data))
		if cmd.Length != uint32(len(cmd.Data)) {
			return nil, fmt.Errorf("sum write length mismatch")
		}
	}

	if totalDataLength+uint64(len(commands)*12)+48 > uint64(conn.frameLimit()) {
		return nil, fmt.Errorf("sum request exceeds frame limit")
	}
	request := bytes.NewBuffer(make([]byte, 0, 16+12*len(commands)+int(totalDataLength)))
	type sumWriteHeader struct {
		IndexGroup  uint32
		IndexOffset uint32
		ReadLength  uint32
		WriteLength uint32
	}

	// Each sub-command is 12 bytes (Group + Offset + Length) + data
	header := sumWriteHeader{
		IndexGroup:  uint32(GroupSumupWrite),
		IndexOffset: uint32(len(commands)),
		ReadLength:  uint32(len(commands) * 4), // Just error codes
		WriteLength: uint32(len(commands)*12) + uint32(totalDataLength),
	}

	err := binary.Write(request, binary.LittleEndian, header)
	if err != nil {
		return nil, fmt.Errorf("failed to encode sum write header: %w", err)
	}

	// First: write all sub-command headers
	for _, cmd := range commands {
		subCmd := struct {
			Group  uint32
			Offset uint32
			Length uint32
		}{
			Group:  cmd.Group,
			Offset: cmd.Offset,
			Length: uint32(len(cmd.Data)),
		}
		err := binary.Write(request, binary.LittleEndian, subCmd)
		if err != nil {
			return nil, fmt.Errorf("failed to encode sub-command: %w", err)
		}
	}

	// Second: write all data
	for _, cmd := range commands {
		err := binary.Write(request, binary.LittleEndian, cmd.Data)
		if err != nil {
			return nil, fmt.Errorf("failed to encode sub-command data: %w", err)
		}
	}

	slog.Debug("sending SumWrite request", "commands", len(commands), "totalDataLength", totalDataLength, "indexGroup", header.IndexGroup, "indexOffset", header.IndexOffset, "readLength", header.ReadLength, "writeLength", header.WriteLength)
	for i, cmd := range commands {
		slog.Debug("SumWrite sub-command", "index", i, "group", cmd.Group, "offset", cmd.Offset, "length", len(cmd.Data))
	}

	// Send the request (SumWrite is actually a ReadWrite command)
	resp, err := conn.request(CommandIDReadWrite, request.Bytes(), internal)
	if err != nil {
		slog.Error("sum write request failed", "error", err)
		return nil, fmt.Errorf("sum write request failed: %w", err)
	}

	results, err := parseSumWriteResponse(commands, resp)
	if err != nil {
		return nil, err
	}
	slog.Debug("parsed SumWrite response payload", "length", len(resp)-8, "expectedLength", len(commands)*4)

	for i, result := range results {
		errorCode := result.Error
		if errorCode != ReturnCodeNoErrors {
			slog.Warn("SumWrite sub-command returned ADS error", "index", i, "error", errorCode)
		} else {
			slog.Debug("SumWrite sub-command completed", "index", i)
		}
	}
	return results, nil
}

type sumWriteSubCommand = SumWriteCommand

type sumWriteSubResult = SumWriteResult
