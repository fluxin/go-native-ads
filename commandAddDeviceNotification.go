package ads

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log/slog"
	"math"
	"time"
)

const adsNotificationTick = 100 * time.Nanosecond

func durationToAdsTicks(name string, d time.Duration, defaultValue time.Duration) (uint32, error) {
	if d == 0 {
		d = defaultValue
	}
	if d < 0 {
		return 0, fmt.Errorf("%s must be >= 0", name)
	}

	ticks := d / adsNotificationTick
	if d > 0 && ticks == 0 {
		ticks = 1
	}
	if ticks > math.MaxUint32 {
		return 0, fmt.Errorf("%s too large: %s", name, d)
	}
	return uint32(ticks), nil
}

func (conn *Connection) AddDeviceNotification(
	group uint32,
	offset uint32,
	length uint32,
	transmissionMode TransMode,
	maxDelay time.Duration,
	cycleTime time.Duration,
) (handle uint32, err error) {
	return conn.addDeviceNotification(group, offset, length, transmissionMode, maxDelay, cycleTime, false)
}
func (conn *Connection) addDeviceNotification(group, offset, length uint32, transmissionMode TransMode, maxDelay, cycleTime time.Duration, internal bool) (handle uint32, err error) {
	request := new(bytes.Buffer)
	type addDeviceNotificationCommandPacket struct {
		Group            uint32
		Offset           uint32
		Length           uint32
		TransmissionMode uint32
		MaxDelay         uint32
		CycleTime        uint32
		Reserved         [16]byte
	}

	maxDelayUnits, err := durationToAdsTicks("maxDelay", maxDelay, 100*time.Millisecond)
	if err != nil {
		return 0, err
	}
	cycleTimeUnits, err := durationToAdsTicks("cycleTime", cycleTime, 100*time.Millisecond)
	if err != nil {
		return 0, err
	}
	slog.Debug("sending AddDeviceNotification request",
		"group", group,
		"offset", offset,
		"length", length,
		"mode", transmissionMode,
		"maxDelay", maxDelay,
		"maxDelayUnits", maxDelayUnits,
		"cycleTime", cycleTime,
		"cycleTimeUnits", cycleTimeUnits)

	content := addDeviceNotificationCommandPacket{
		group,
		offset,
		length,
		uint32(transmissionMode),
		maxDelayUnits,
		cycleTimeUnits,
		[16]byte{},
	}
	err = binary.Write(request, binary.LittleEndian, content)
	if err != nil {
		slog.Error("failed to encode AddDeviceNotification request", "error", err)
		return 0, fmt.Errorf("failed to encode AddDeviceNotification request: %w", err)
	}
	// Try to send the request
	resp, err := conn.request(CommandIDAddDeviceNotification, request.Bytes(), internal)
	if err != nil {
		return
	}
	payload, err := parseErrorWithFixedPayload("AddDeviceNotification", resp, 4)
	if err != nil {
		slog.Error("failed to decode AddDeviceNotification response", "error", err)
		return 0, err
	}
	if len(payload) != 4 {
		return 0, fmt.Errorf("invalid AddDeviceNotification payload length: expected=4 actual=%d", len(payload))
	}
	handle = binary.LittleEndian.Uint32(payload)
	slog.Debug("registered notification handle", "handle", handle)

	return handle, nil
}
