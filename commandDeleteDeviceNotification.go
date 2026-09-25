package ads

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log/slog"
)

// DeleteDeviceNotification removes a remote notification and stops its local worker.
func (conn *Connection) DeleteDeviceNotification(handle uint32) error {
	if err := conn.beginOperation(); err != nil {
		return err
	}
	defer conn.endOperation()
	return conn.deleteDeviceNotification(handle, true)
}
func (conn *Connection) deleteDeviceNotification(handle uint32, internal bool) error {
	request := &bytes.Buffer{}
	if err := binary.Write(request, binary.LittleEndian, handle); err != nil {
		return fmt.Errorf("failed to encode DeleteDeviceNotification request: %w", err)
	}
	// Try to send the request
	resp, err := conn.request(CommandIDDeleteDeviceNotification, request.Bytes(), internal)
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
		err := &ProtocolError{Operation: "DeleteDeviceNotification", Code: adsErr}
		slog.Error("delete notification returned ADS error", "handle", handle, "error", err)
		return err
	}
	if adsErr == ReturnCodeDeviceNotifyHandleInvalid {
		slog.Debug("notification handle already invalid on device", "handle", handle)
	}
	var delivery *notificationDelivery
	// Safely remove from active notification maps using mutex
	conn.symbolLock.Lock()
	if subID, ok := conn.notificationToSubID[handle]; ok {
		if sub, found := conn.subscriptions[subID]; found {
			sub.adsHandle = 0
			delivery = sub.delivery
			if delivery != nil {
				delivery.cancel()
			}
		}
		delete(conn.notificationToSubID, handle)
	}
	delete(conn.activeNotifications, handle)
	for _, item := range conn.pendingNotifications[handle] {
		conn.pendingBytes -= len(item.content)
	}
	delete(conn.pendingNotifications, handle)
	conn.symbolLock.Unlock()
	if delivery != nil {
		<-delivery.done
	}
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
