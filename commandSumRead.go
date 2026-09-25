package ads

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log/slog"
)

func parseSumReadResponse(commands []sumReadSubCommand, resp []byte) ([]sumReadSubResult, error) {
	payload, err := parseReadWritePayloadResponse("SumRead", resp, nil)
	if err != nil {
		return nil, err
	}

	errorBytes := len(commands) * 4
	dataBytes := 0
	for _, cmd := range commands {
		dataBytes += int(cmd.Length)
	}
	expectedPayloadLen := errorBytes + dataBytes
	if len(payload) != expectedPayloadLen {
		return nil, fmt.Errorf("invalid SumRead payload size: expected=%d actual=%d", expectedPayloadLen, len(payload))
	}

	results := make([]sumReadSubResult, len(commands))
	offset := 0
	for i := range commands {
		errorCode := ReturnCode(binary.LittleEndian.Uint32(payload[offset : offset+4]))
		offset += 4
		results[i].Error = errorCode
		if errorCode != ReturnCodeNoErrors {
			slog.Warn("SumRead sub-command returned ADS error", "index", i, "error", errorCode)
		}
	}

	for i, cmd := range commands {
		end := offset + int(cmd.Length)
		chunk := payload[offset:end:end]
		offset = end
		if results[i].Error == ReturnCodeNoErrors {
			results[i].Length = cmd.Length
			results[i].Data = chunk
		}
	}

	return results, nil
}

// sumReadSubCommand represents a single read operation in a sum command
type SumReadCommand struct {
	Group  uint32
	Offset uint32
	Length uint32
}

// sumReadSubResult represents the result of a single read operation
type SumReadResult struct {
	Error  ReturnCode
	Length uint32
	// Data is a capacity-limited view into the response. Clone it to retain only this field.
	Data []byte
}

func (conn *Connection) SumRead(commands []SumReadCommand) ([]SumReadResult, error) {
	return conn.sumRead(commands, false)
}
func (conn *Connection) sumRead(commands []sumReadSubCommand, internal bool) ([]sumReadSubResult, error) {

	const maxCommands = 500
	if len(commands) == 0 {
		return nil, fmt.Errorf("empty sum request")
	}
	if len(commands) > maxCommands {
		return nil, fmt.Errorf("batch size %d exceeds maximum %d", len(commands), maxCommands)
	}

	// Calculate total data length
	totalLength := uint64(0)
	for _, cmd := range commands {
		totalLength += uint64(cmd.Length)
	}

	if totalLength+uint64(len(commands)*4)+40 > uint64(conn.frameLimit()) {
		return nil, fmt.Errorf("sum response exceeds frame limit")
	}
	request := bytes.NewBuffer(make([]byte, 0, 16+12*len(commands)))
	type sumReadHeader struct {
		IndexGroup  uint32
		IndexOffset uint32
		ReadLength  uint32
		WriteLength uint32
	}

	header := sumReadHeader{
		IndexGroup:  uint32(GroupSumupRead),
		IndexOffset: uint32(len(commands)),
		ReadLength:  uint32(len(commands)*4) + uint32(totalLength),
		WriteLength: uint32(len(commands) * 12),
	}

	err := binary.Write(request, binary.LittleEndian, header)
	if err != nil {
		return nil, fmt.Errorf("failed to encode sum read header: %w", err)
	}

	// Write each sub-command
	for _, cmd := range commands {
		err := binary.Write(request, binary.LittleEndian, cmd)
		if err != nil {
			return nil, fmt.Errorf("failed to encode sub-command: %w", err)
		}
	}

	slog.Debug("sending SumRead request", "commands", len(commands), "totalLength", totalLength, "indexGroup", header.IndexGroup, "indexOffset", header.IndexOffset, "readLength", header.ReadLength, "writeLength", header.WriteLength)
	for i, cmd := range commands {
		slog.Debug("SumRead sub-command", "index", i, "group", cmd.Group, "offset", cmd.Offset, "length", cmd.Length)
	}

	// Send the request (SumRead is actually a ReadWrite command)
	resp, err := conn.request(CommandIDReadWrite, request.Bytes(), internal)
	if err != nil {
		slog.Error("sum read request failed", "error", err)
		return nil, fmt.Errorf("sum read request failed: %w", err)
	}

	results, err := parseSumReadResponse(commands, resp)
	if err != nil {
		return nil, err
	}
	for i, result := range results {
		if result.Error == ReturnCodeNoErrors {
			slog.Debug("SumRead sub-command completed", "index", i)
		}
	}
	return results, nil
}

type sumReadSubCommand = SumReadCommand

type sumReadSubResult = SumReadResult
