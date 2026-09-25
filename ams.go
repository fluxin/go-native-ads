package ads

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
)

type amsTCPHeader struct {
	Unknown1 uint8
	System   uint8
	Length   uint32
}

type amsHeader struct {
	Target    AmsAddress
	Source    AmsAddress
	Command   CommandID
	State     uint16
	Length    uint32
	ErrorCode uint32
	InvokeID  uint32
}

func stringToNetID(source string) (netID [6]byte, err error) {
	splitLocalhost := strings.Split(source, ".")
	if len(splitLocalhost) != 6 {
		return netID, fmt.Errorf("invalid NetID format: %s, expected 6 octets separated by dots (e.g., 192.168.1.1.1.1)", source)
	}

	for i, a := range splitLocalhost {
		value, err := strconv.ParseUint(a, 10, 8)
		if err != nil {
			return netID, fmt.Errorf("invalid octet at position %d in NetID %s: %w", i, source, err)
		}
		netID[i] = byte(value)
	}
	return netID, nil
}

func (conn *Connection) encode(command CommandID, data []byte, invokeID uint32) ([]byte, error) {
	conn.addressLock.RLock()
	defer conn.addressLock.RUnlock()
	slog.Debug("Starting encoding of AMS header",
		"command", command,
		"target", conn.target,
		"source", conn.source,
		"ID", invokeID,
		"length of data", len(data))
	tcpHeader := &amsTCPHeader{
		0,
		0,
		uint32(32 + len(data)),
	}
	header := &amsHeader{
		conn.target,
		conn.source,
		command,
		uint16(4),
		uint32(len(data)),
		uint32(0),
		invokeID,
	}

	buf := &bytes.Buffer{}
	err := binary.Write(buf, binary.LittleEndian, tcpHeader)
	if err != nil {
		return nil, err
	}
	err = binary.Write(buf, binary.LittleEndian, header)
	if err != nil {
		return nil, err
	}
	err = binary.Write(buf, binary.LittleEndian, data)
	slog.Debug("data to transmit", "data", data)
	if err != nil {
		slog.Error("binary.Write failed", "error", err)
		return nil, err
	}

	slog.Debug("The encoded AMS header", "bytes", buf.Bytes())

	return buf.Bytes(), nil
}
