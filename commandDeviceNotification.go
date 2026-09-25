package ads

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"time"
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

const (
	maxPendingNotificationsPerHandle = 8
	unknownNotificationCleanupDelay  = 5 * time.Second
)

const maxPendingNotificationHandles = 256
const maxPendingNotificationBytes = 1 << 20

func (conn *Connection) deviceNotification(ctx context.Context, in []byte) error {
	if len(in) < 8 {
		return fmt.Errorf("short notification stream")
	}
	// The AMS payload boundary is authoritative, as in Beckhoff AdsLib.
	// Validate every nested count and size against that boundary before delivery.
	stamps := binary.LittleEndian.Uint32(in[4:8])
	offset := 8
	// Validate the entire frame before publishing any samples.
	type sampleData struct {
		handle    uint32
		timestamp uint64
		data      []byte
	}
	samples := make([]sampleData, 0)
	for i := uint32(0); i < stamps; i++ {
		if len(in)-offset < 12 {
			return fmt.Errorf("short notification stamp")
		}
		timestamp := binary.LittleEndian.Uint64(in[offset:])
		count := binary.LittleEndian.Uint32(in[offset+8:])
		offset += 12
		for j := uint32(0); j < count; j++ {
			if len(in)-offset < 8 {
				return fmt.Errorf("short notification sample")
			}
			handle := binary.LittleEndian.Uint32(in[offset:])
			size := binary.LittleEndian.Uint32(in[offset+4:])
			offset += 8
			if uint64(size) > uint64(len(in)-offset) {
				return fmt.Errorf("truncated notification sample")
			}
			if len(samples) >= 65536 {
				return fmt.Errorf("notification sample count exceeds limit")
			}
			samples = append(samples, sampleData{handle, timestamp, in[offset : offset+int(size)]})
			offset += int(size)
		}
	}
	if offset != len(in) {
		return fmt.Errorf("trailing notification data")
	}
	for _, sample := range samples {
		_ = conn.handleNotification(ctx, sample.handle, sample.timestamp, sample.data)
	}
	return nil
}
func (conn *Connection) handleNotification(ctx context.Context, handle uint32, timestamp uint64, content []byte) error {
	conn.symbolLock.Lock()
	callback, ok := conn.activeNotifications[handle]
	if ok {
		conn.symbolLock.Unlock()
		return callback(ctx, timestamp, content)
	}
	if conn.pendingNotifications == nil {
		conn.pendingNotifications = make(map[uint32][]pendingNotification)
	}
	if conn.unknownCleanup == nil {
		conn.unknownCleanup = make(map[uint32]bool)
	}
	pending := conn.pendingNotifications[handle]
	if len(pending) == 0 && len(conn.pendingNotifications) >= maxPendingNotificationHandles {
		conn.symbolLock.Unlock()
		return nil
	}
	nextBytes := conn.pendingBytes + len(content)
	if len(pending) >= maxPendingNotificationsPerHandle {
		nextBytes -= len(pending[0].content)
	}
	if nextBytes > maxPendingNotificationBytes {
		conn.symbolLock.Unlock()
		return nil
	}
	if len(pending) >= maxPendingNotificationsPerHandle {
		pending = pending[1:]
	}
	copied := append([]byte(nil), content...)
	conn.pendingBytes = nextBytes
	conn.pendingNotifications[handle] = append(pending, pendingNotification{timestamp, copied})
	generation := conn.notificationGeneration
	schedule := !conn.unknownCleanup[handle]
	conn.unknownCleanup[handle] = true
	conn.symbolLock.Unlock()
	if schedule {
		conn.startBackground(func() { conn.cleanupUnknownNotification(handle, generation) })
	}
	return nil
}
func (conn *Connection) deleteUnknownNotificationAfterGrace(handle uint32) {
	conn.symbolLock.Lock()
	generation := conn.notificationGeneration
	conn.symbolLock.Unlock()
	conn.cleanupUnknownNotification(handle, generation)
}
func (conn *Connection) cleanupUnknownNotification(handle uint32, generation uint64) {
	delay := unknownNotificationCleanupDelay
	if conn.testUnknownNotificationDelay > 0 {
		delay = conn.testUnknownNotificationDelay
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	ctx := conn.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}
	conn.symbolLock.Lock()
	if generation != conn.notificationGeneration {
		conn.symbolLock.Unlock()
		return
	}
	_, active := conn.activeNotifications[handle]
	_, pending := conn.pendingNotifications[handle]
	delete(conn.unknownCleanup, handle)
	// Expire unknown data even when remote deletion is disabled.
	for _, item := range conn.pendingNotifications[handle] {
		conn.pendingBytes -= len(item.content)
	}
	delete(conn.pendingNotifications, handle)
	conn.symbolLock.Unlock()
	if active || !pending || !conn.reconnectPolicySnapshot().DeleteUnknownNotifications {
		return
	}
	if err := conn.deleteUnknownNotification(handle); err != nil {
		slog.Debug("unknown notification cleanup failed", "error", err)
	}
}
func (conn *Connection) deleteUnknownNotification(handle uint32) error {
	if conn.testDeleteUnknownNotificationFn != nil {
		return conn.testDeleteUnknownNotificationFn(handle)
	}
	if err := conn.beginOperation(); err != nil {
		return err
	}
	defer conn.endOperation()
	conn.restoreLock.Lock()
	defer conn.restoreLock.Unlock()
	// A late cleanup must not delete a newly registered/reused handle.
	conn.symbolLock.Lock()
	_, active := conn.activeNotifications[handle]
	conn.symbolLock.Unlock()
	if active {
		return nil
	}
	return conn.deleteDeviceNotification(handle, true)
}
