package ads

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync/atomic"
	"time"
)

func (conn *Connection) send(data []byte) (response []byte, err error) {
	if err := conn.ensureConnected(); err != nil {
		return nil, err
	}

	conn.waitGroup.Add(1)
	defer conn.waitGroup.Done()
	atomic.AddUint32(&conn.currentRequest, 1)
	ctx, cancel := context.WithCancel(conn.ctx)
	defer cancel()
	select {
	case <-ctx.Done():
		return response, err
	case conn.sendChannel <- data:
	}

	ctx, cancel = context.WithCancel(ctx)
	defer cancel()
	select {
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("request aborted, deadline exceeded %w", ctx.Err())
			slog.Error("sendRequest aborted due to timeout", "error", err)
		} else {
			err = fmt.Errorf("request aborted, shutdown initiated %w", ctx.Err())
			slog.Error("sendRequest aborted due to shutdown", "error", err)
		}
		conn.onTransportError(ctx.Err())
		return nil, err
	case response = <-conn.systemResponse:
		return response, nil
	}
}

func (conn *Connection) sendRequest(command CommandID, data []byte) (response []byte, err error) {
	if conn == nil {
		slog.Error("Failed to encode header, connection is nil pointer")
		return nil, errors.New("connection is nil")
	}
	if err := conn.ensureConnected(); err != nil {
		return nil, err
	}
	conn.waitGroup.Add(1)
	defer conn.waitGroup.Done()
	conn.activeRequestLock.Lock()
	// First, request a new invoke id
	id := atomic.AddUint32(&conn.currentRequest, 1)
	// Create a channel for the response
	responseChan := make(chan []byte)
	conn.activeRequests[id] = responseChan
	conn.activeRequestLock.Unlock()
	slog.Debug("encoding packet", "command", command, "data", data, "id", id)

	pack, err := conn.encode(command, data, id)
	if err != nil {
		// Clean up the channel on encoding error
		conn.activeRequestLock.Lock()
		delete(conn.activeRequests, id)
		conn.activeRequestLock.Unlock()
		slog.Error("Error during sendrequest encode", "error", err)
		return nil, err
	}
	ctx, cancel := context.WithTimeout(conn.ctx, 4000*time.Millisecond)
	defer cancel()
	select {
	case <-ctx.Done():
		// Clean up the channel on timeout/shutdown
		conn.activeRequestLock.Lock()
		delete(conn.activeRequests, id)
		conn.activeRequestLock.Unlock()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			slog.Error("sendRequest aborted due to timeout")
		} else {
			slog.Info("sendRequest aborted due to shutdown")
		}
		conn.onTransportError(ctx.Err())
		return nil, ctx.Err()
	case conn.sendChannel <- pack:
	}
	select {
	case <-ctx.Done():
		// Clean up the channel on timeout/shutdown
		conn.activeRequestLock.Lock()
		delete(conn.activeRequests, id)
		conn.activeRequestLock.Unlock()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			slog.Error("sendRequest aborted due to timeout")
		} else {
			slog.Info("sendRequest aborted due to shutdown")
		}
		conn.onTransportError(ctx.Err())
		return nil, ctx.Err()
	case response = <-responseChan:
		return response, nil
	}
}

func (conn *Connection) listen() <-chan []byte {
	c := make(chan []byte)
	go func() {
		defer close(c)
		reader := bufio.NewReader(conn.connection)
		buf := bytes.Buffer{}
		for {
			tcpHeader := amsTCPHeader{}
			data := make([]byte, 6)
			select {
			case <-conn.ctx.Done():
				slog.Info("exit listen")
				return
			default:
				_, err := io.ReadFull(reader, data)
				if err != nil {
					slog.Debug("listen loop stopped while reading header", "error", err)
					conn.onTransportError(err)
					return
				}
			}
			buf.Write(data)
			err := binary.Read(&buf, binary.LittleEndian, &tcpHeader)
			if err != nil {
				slog.Error("error during header read", "error", err)
				continue
			}
			data = make([]byte, tcpHeader.Length)
			select {
			case <-conn.ctx.Done():
				return
			default:
				_, err := io.ReadFull(reader, data)
				if err != nil {
					slog.Debug("listen loop stopped while reading payload", "error", err)
					conn.onTransportError(err)
					return
				}
			}
			slog.Debug("routing incoming AMS/TCP frame",
				"system", tcpHeader.System,
				"length", tcpHeader.Length)
			if tcpHeader.System > 0 {
				systemCommand := uint16(tcpHeader.System)<<8 | uint16(tcpHeader.Unknown1)
				if systemCommand == amsTCPPortRouterNote {
					if err := conn.handleRouterNote(data); err != nil {
						slog.Debug("failed to parse router note", "error", err)
					}
					continue
				}
				slog.Debug("routing frame to system response channel",
					"system", tcpHeader.System,
					"systemCommand", systemCommand,
					"length", tcpHeader.Length)
				select {
				case conn.systemResponse <- data:
				case <-time.After(100 * time.Millisecond):
					slog.Error("system response channel blocked; dropped frame")
					if len(data) >= 33 {
						cmd := binary.LittleEndian.Uint16(data[32:34])
						slog.Error("dropped frame command", "command", cmd)
					}
				}
			} else {
				slog.Debug("routing frame to ADS handler", "length", tcpHeader.Length)
				go conn.handleReceive(conn.ctx, data)
			}
		}
	}()
	return c
}

func (conn *Connection) handleReceive(ctx context.Context, data []byte) {
	slog.Debug("in read")
	if len(data) < 32 {
		slog.Error("received frame with short AMS header", "length", len(data))
		return
	}
	buf := bytes.NewBuffer(data)
	header := amsHeader{}
	err := binary.Read(buf, binary.LittleEndian, &header)
	if err != nil {
		slog.Error("Error parsing header", "error", err)
		return
	}
	slog.Debug("parsed AMS header",
		"command", header.Command,
		"cmdName", commandName(header.Command),
		"sourceNetID", header.Source.NetID,
		"sourcePort", header.Source.Port,
		"targetNetID", header.Target.NetID,
		"targetPort", header.Target.Port,
		"invokeID", header.InvokeID,
		"length", header.Length)
	slog.Debug("header info", "header", header)

	adsData := data[32:]
	if len(adsData) != int(header.Length) {
		slog.Error("ADS payload length mismatch", "expected", header.Length, "actual", len(adsData))
		return
	}

	slog.Debug("dispatching ADS command", "command", header.Command, "invokeID", header.InvokeID, "length", header.Length)

	switch header.Command {
	case CommandIDDeviceNotification:
		slog.Debug("processing device notification frame")
		err := conn.deviceNotification(ctx, adsData)
		if err != nil {
			slog.Error("failed to process device notification", "error", err)
		}
	default:
		slog.Debug("default receive")
		// Check if the response channel exists and is open
		conn.activeRequestLock.Lock()
		defer conn.activeRequestLock.Unlock()
		if response, ok := conn.activeRequests[header.InvokeID]; ok {
			// Try to send the response to the waiting request function
			select {
			case <-ctx.Done():
				slog.Warn("request context closed before response delivery", "id", header.InvokeID, "command", header.Command)
				return
			case response <- adsData:
				// Delete the map entry after successful send to prevent memory leak
				delete(conn.activeRequests, header.InvokeID)
				slog.Debug("Successfully delivered answer", "id", header.InvokeID, "command", header.Command)
			}
		} else {
			slog.Warn("received ADS response with unknown invoke ID", "invokeID", header.InvokeID, "command", header.Command)
		}

	}
}

func (conn *Connection) transmitWorker() {
	conn.waitGroup.Add(1)
	defer conn.waitGroup.Done()
	writer := bufio.NewWriter(conn.connection)
	ctx, cancel := context.WithCancel(conn.ctx)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			slog.Debug("Exit transmitWorker")
			return
		case data := <-conn.sendChannel:
			slog.Debug("Sending bytes", "size", len(data))
			_, err := writer.Write(data)
			if err != nil {
				slog.Error("Error sending data on conn", "error", err)
				conn.onTransportError(err)
				return
			}
			if err := writer.Flush(); err != nil {
				slog.Error("Error flushing data on conn", "error", err)
				conn.onTransportError(err)
				return
			}
		}
	}
}
