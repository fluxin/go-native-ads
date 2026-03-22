package ads

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log/slog"
)

// DeleteDeviceNotification does stuff
func (conn *Connection) DeleteDeviceNotification(handle uint32) error {
	if err := conn.ensureConnected(); err != nil {
		return err
	}

	conn.waitGroup.Add(1)
	defer conn.waitGroup.Done()
	request := &bytes.Buffer{}
	if err := binary.Write(request, binary.LittleEndian, handle); err != nil {
		return fmt.Errorf("failed to encode DeleteDeviceNotification request: %w", err)
	}
	// Try to send the request
	resp, err := conn.sendRequest(CommandIDDeleteDeviceNotification, request.Bytes())
	if err != nil {
		slog.Error("failed to delete notification handle", "handle", handle, "error", err)
		return err
	}
	adsErr, err := parseDeleteDeviceNotificationResponse(resp)
	if err != nil {
		slog.Error("failed parsing delete notification response", "handle", handle, "error", err)
		return err
	}
	if adsErr != ReturnCodeNoErrors && adsErr != ReturnCodeDeviceNotifyHandleInvalid {
		err := fmt.Errorf("ADS error %d in DeleteDeviceNotification", adsErr)
		slog.Error("delete notification returned ADS error", "handle", handle, "error", err)
		return err
	}
	if adsErr == ReturnCodeDeviceNotifyHandleInvalid {
		slog.Debug("notification handle already invalid on device", "handle", handle)
	}
	// Safely remove from active notification maps using mutex
	conn.symbolLock.Lock()
	if subID, ok := conn.notificationToSubID[handle]; ok {
		if sub, found := conn.subscriptions[subID]; found {
			sub.adsHandle = 0
		}
		delete(conn.notificationToSubID, handle)
	}
	delete(conn.activeNotifications, handle)
	delete(conn.pendingNotifications, handle)
	conn.symbolLock.Unlock()
	slog.Debug("deleted notification handle", "handle", handle)
	return nil
}

func parseDeleteDeviceNotificationResponse(resp []byte) (ReturnCode, error) {
	const expectedLen = 4
	if len(resp) != expectedLen {
		return ReturnCodeClientSyncResponseInvalid, fmt.Errorf("invalid DeleteDeviceNotification response length: expected=%d actual=%d", expectedLen, len(resp))
	}
	return ReturnCode(binary.LittleEndian.Uint32(resp)), nil
}

func (conn *Connection) cancelSubscription(id uint64) error {
	conn.symbolLock.Lock()
	spec, ok := conn.subscriptions[id]
	if !ok {
		conn.symbolLock.Unlock()
		return nil
	}
	handle := spec.adsHandle
	delete(conn.subscriptions, id)
	if handle != 0 {
		delete(conn.notificationToSubID, handle)
	}
	conn.symbolLock.Unlock()

	if handle != 0 {
		conn.stateLock.Lock()
		state := conn.state
		conn.stateLock.Unlock()
		if state == connectionStateConnected || state == connectionStateConnecting || state == connectionStateReconnecting {
			if err := conn.DeleteDeviceNotification(handle); err != nil {
				return err
			}
		}
	}
	return nil
}
