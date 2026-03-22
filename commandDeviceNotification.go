package ads

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
)

const (
	windowsTick    int64 = 10000000
	secToUnixEpoch int64 = 11644473600
)

type notificationStream struct {
	Length uint32
	Stamps uint32
}
type stampHeader struct {
	Timestamp uint64
	Samples   uint32
}
type notificationSample struct {
	Handle uint32
	Size   uint32
}

type pendingNotification struct {
	timestamp uint64
	content   []byte
}

const maxPendingNotificationsPerHandle = 8

// deviceNotification - ADS command id: 8
func (conn *Connection) deviceNotification(ctx context.Context, in []byte) error {
	conn.waitGroup.Add(1)
	defer conn.waitGroup.Done()
	slog.Debug("processing device notification payload", "dataLen", len(in))

	var stream notificationStream
	var header stampHeader
	var sample notificationSample
	var content []byte

	data := bytes.NewBuffer(in)

	err := binary.Read(data, binary.LittleEndian, &stream)
	if err != nil {
		return fmt.Errorf("unable to read notification %v", err)
	}
	slog.Debug("parsed device notification stream", "stamps", stream.Stamps, "length", stream.Length)
	for i := uint32(0); i < stream.Stamps; i++ {
		err := binary.Read(data, binary.LittleEndian, &header)
		if err != nil {
			return fmt.Errorf("unable to read stamp header: %w", err)
		}

		for j := uint32(0); j < header.Samples; j++ {
			err := binary.Read(data, binary.LittleEndian, &sample)
			if err != nil {
				slog.Error("failed to read notification sample header", "error", err)
				break
			}
			content = make([]byte, sample.Size)
			_, err = data.Read(content)
			if err != nil {
				return fmt.Errorf("unable to read notification content: %w", err)
			}
			slog.Debug("notification sample decoded", "handle", sample.Handle, "size", sample.Size, "timestamp", header.Timestamp)
			conn.handleNotification(ctx, sample.Handle, header.Timestamp, content)
		}
	}
	return nil
}

func (conn *Connection) handleNotification(ctx context.Context, handle uint32, timestamp uint64, content []byte) error {
	conn.symbolLock.Lock()
	callback, ok := conn.activeNotifications[handle]
	if !ok {
		policy := conn.reconnectPolicySnapshot()
		copiedContent := make([]byte, len(content))
		copy(copiedContent, content)
		pending := conn.pendingNotifications[handle]
		if len(pending) >= maxPendingNotificationsPerHandle {
			pending = pending[1:]
		}
		conn.pendingNotifications[handle] = append(pending, pendingNotification{timestamp: timestamp, content: copiedContent})
		conn.symbolLock.Unlock()
		slog.Debug("buffered notification before callback registration", "handle", handle)
		if policy.DeleteUnknownNotifications {
			go func(h uint32) {
				if err := conn.deleteUnknownNotification(h); err != nil {
					slog.Debug("failed to delete unknown notification handle", "handle", h, "error", err)
				}
			}(handle)
		}
		return nil
	}
	conn.symbolLock.Unlock()

	slog.Debug("dispatching notification callback", "handle", handle, "contentLen", len(content))
	err := callback(ctx, timestamp, content)
	if err != nil {
		slog.Error("notification callback failed", "handle", handle, "error", err)
	}
	return nil
}

func (conn *Connection) deleteUnknownNotification(handle uint32) error {
	if conn.testDeleteUnknownNotificationFn != nil {
		return conn.testDeleteUnknownNotificationFn(handle)
	}
	return conn.DeleteDeviceNotification(handle)
}
