package ads

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"time"
)

const defaultMaxFrameSize = 16 << 20

type outgoingPacket struct {
	data    []byte
	written chan error
}

func (packet outgoingPacket) complete(err error) {
	if packet.written != nil {
		packet.written <- err
	}
}

type commandResponse struct {
	data []byte
	err  error
}
type pendingRequest struct {
	command  CommandID
	response chan commandResponse
}
type routerResponse struct {
	command uint16
	data    []byte
}

// ProtocolError preserves the error code and the layer that returned it.
type ProtocolError struct {
	Operation string
	Code      ReturnCode
	AMS       bool
}

func (e *ProtocolError) Error() string {
	layer := "ADS"
	if e.AMS {
		layer = "AMS"
	}
	return fmt.Sprintf("%s error %d in %s", layer, e.Code, e.Operation)
}

func (conn *Connection) requestTimeout() time.Duration {
	if conn.timeout > 0 {
		return conn.timeout
	}
	return 4 * time.Second
}
func (conn *Connection) frameLimit() uint32 {
	if conn.maxFrameSize > 0 {
		return conn.maxFrameSize
	}
	return defaultMaxFrameSize
}

func (conn *Connection) transportSnapshot() (context.Context, chan outgoingPacket, chan routerResponse, error) {
	conn.transportLock.Lock()
	defer conn.transportLock.Unlock()
	if conn.transportCtx == nil || conn.transportCtx.Err() != nil {
		return nil, nil, nil, net.ErrClosed
	}
	return conn.transportCtx, conn.sendChannel, conn.systemResponse, nil
}

func (conn *Connection) send(data []byte) ([]byte, error) { return conn.sendSystem(data, false) }
func (conn *Connection) sendSystem(data []byte, internal bool) ([]byte, error) {
	if !internal {
		if err := conn.beginOperation(); err != nil {
			return nil, err
		}
		defer conn.endOperation()
	}
	if len(data) < 6 {
		return nil, fmt.Errorf("short system request")
	}
	// System frames have no invoke ID. Only one exchange may be in flight.
	conn.systemLock.Lock()
	defer conn.systemLock.Unlock()
	transport, send, responses, err := conn.transportSnapshot()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(transport, conn.requestTimeout())
	defer cancel()
	command := binary.LittleEndian.Uint16(data)
	var written chan error
	if command == amsTCPPortClose {
		written = make(chan error, 1)
	}
	select {
	case send <- outgoingPacket{data: data, written: written}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if written != nil {
		select {
		case err := <-written:
			return nil, err
		case <-ctx.Done():
			select {
			case err := <-written:
				return nil, err
			default:
				return nil, ctx.Err()
			}
		}
	}
	select {
	case reply := <-responses:
		if reply.command != command {
			conn.failTransport(transport, fmt.Errorf("unexpected system response %#x for %#x", reply.command, command))
			return nil, fmt.Errorf("unexpected system response command")
		}
		return reply.data, nil
	case <-ctx.Done():
		// A timed-out uncorrelated reply must never be consumed by the next request.
		conn.failTransport(transport, ctx.Err())
		return nil, ctx.Err()
	}
}

func (conn *Connection) sendRequest(command CommandID, data []byte) ([]byte, error) {
	return conn.request(command, data, false)
}
func (conn *Connection) request(command CommandID, data []byte, internal bool) ([]byte, error) {
	if conn == nil {
		return nil, errors.New("connection is nil")
	}
	if !internal {
		if err := conn.beginOperation(); err != nil {
			return nil, err
		}
		defer conn.endOperation()
	}
	if uint64(len(data))+32 > uint64(conn.frameLimit()) {
		return nil, fmt.Errorf("request exceeds frame limit")
	}
	transport, send, _, err := conn.transportSnapshot()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(transport, conn.requestTimeout())
	defer cancel()
	reply := make(chan commandResponse, 1)
	conn.activeRequestLock.Lock()
	id := atomic.AddUint32(&conn.currentRequest, 1)
	for conn.activeRequests[id] != nil {
		id = atomic.AddUint32(&conn.currentRequest, 1)
	}
	conn.activeRequests[id] = &pendingRequest{command: command, response: reply}
	conn.activeRequestLock.Unlock()
	defer func() {
		conn.activeRequestLock.Lock()
		delete(conn.activeRequests, id)
		conn.activeRequestLock.Unlock()
	}()
	packet, err := conn.encode(command, data, id)
	if err != nil {
		return nil, err
	}
	select {
	case send <- outgoingPacket{data: packet}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case r := <-reply:
		return r.data, r.err
	case <-ctx.Done():
		select {
		case r := <-reply:
			return r.data, r.err
		default:
			return nil, ctx.Err()
		}
	}
}

func (conn *Connection) failTransport(ctx context.Context, err error) {
	conn.transportLock.Lock()
	current := conn.transportCtx
	conn.transportLock.Unlock()
	if current != ctx || ctx.Err() != nil {
		return
	}
	conn.onTransportError(err)
}

func (conn *Connection) listen(ctx context.Context, connection net.Conn) {
	reader := bufio.NewReader(connection)
	var header [6]byte
	for {
		if _, err := io.ReadFull(reader, header[:]); err != nil {
			conn.failTransport(ctx, err)
			return
		}
		length := binary.LittleEndian.Uint32(header[2:])
		if length > conn.frameLimit() {
			conn.failTransport(ctx, fmt.Errorf("AMS frame length %d exceeds limit", length))
			return
		}
		data := make([]byte, length)
		if _, err := io.ReadFull(reader, data); err != nil {
			conn.failTransport(ctx, err)
			return
		}
		command := binary.LittleEndian.Uint16(header[:2])
		if command != 0 {
			if command == amsTCPPortRouterNote {
				_ = conn.handleRouterNote(data)
				continue
			}
			select {
			case conn.systemResponse <- routerResponse{command, data}:
			case <-ctx.Done():
				return
			default:
				conn.failTransport(ctx, fmt.Errorf("unsolicited system response"))
				return
			}
		} else {
			// Parsing and enqueueing are ordered. User callbacks run on bounded workers.
			conn.handleReceive(ctx, data)
		}
	}
}

func (conn *Connection) handleReceive(ctx context.Context, data []byte) {
	if len(data) < 32 {
		conn.failTransport(ctx, fmt.Errorf("short AMS header"))
		return
	}
	command := CommandID(binary.LittleEndian.Uint16(data[16:]))
	length := binary.LittleEndian.Uint32(data[20:])
	code := binary.LittleEndian.Uint32(data[24:])
	id := binary.LittleEndian.Uint32(data[28:])
	if uint64(length) != uint64(len(data)-32) {
		conn.failTransport(ctx, fmt.Errorf("ADS payload length mismatch"))
		return
	}
	if command == CommandIDDeviceNotification {
		if code != 0 {
			return
		}
		if err := conn.deviceNotification(ctx, data[32:]); err != nil {
			conn.failTransport(ctx, err)
		}
		return
	}
	conn.activeRequestLock.Lock()
	pending := conn.activeRequests[id]
	delete(conn.activeRequests, id)
	conn.activeRequestLock.Unlock()
	if pending == nil {
		return
	}
	response := commandResponse{data: data[32:]}
	if code != 0 {
		response.err = &ProtocolError{Operation: commandName(command), Code: ReturnCode(code), AMS: true}
	} else if pending.command != command {
		response.err = fmt.Errorf("unexpected response command %d", command)
	}
	// Exactly one completion, never block under the request map mutex.
	select {
	case pending.response <- response:
	default:
	}
}

func (conn *Connection) transmitWorker(ctx context.Context, connection net.Conn) {
	conn.transportLock.Lock()
	send := conn.sendChannel
	conn.transportLock.Unlock()
	for {
		select {
		case <-ctx.Done():
			return
		case packet := <-send:
			data := packet.data
			if ctx.Err() != nil {
				packet.complete(ctx.Err())
				return
			}
			if err := connection.SetWriteDeadline(time.Now().Add(conn.requestTimeout())); err != nil {
				packet.complete(err)
				conn.failTransport(ctx, err)
				return
			}
			for len(data) > 0 {
				n, err := connection.Write(data)
				if err != nil {
					packet.complete(err)
					conn.failTransport(ctx, err)
					return
				}
				if n == 0 {
					packet.complete(io.ErrNoProgress)
					conn.failTransport(ctx, io.ErrNoProgress)
					return
				}
				data = data[n:]
			}
			packet.complete(nil)
		}
	}
}
