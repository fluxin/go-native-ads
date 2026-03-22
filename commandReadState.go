package ads

import (
	"bytes"
	"encoding/binary"
	"log/slog"
)

// ReadStateResponse - ADS command id: 4
type states struct {
	AdsState    AdsState
	DeviceState uint16
}

func (conn *Connection) ReadState() (response states, err error) {
	conn.waitGroup.Add(1)
	defer conn.waitGroup.Done()
	// Try to send the request
	resp, err := conn.sendRequest(CommandIDReadState, []byte{})
	slog.Debug("response from plc for state", "data", resp)
	if err != nil {
		slog.Error("Error during read state", "error", err)
		return
	}
	payload, err := parseErrorWithFixedPayload("ReadState", resp, 4)
	if err != nil {
		slog.Error("Error parsing state response", "error", err)
		return states{}, err
	}
	stateResponse := states{}
	buf := bytes.NewBuffer(payload)
	err = binary.Read(buf, binary.LittleEndian, &stateResponse)
	if err != nil {
		slog.Error("Error decoding state payload", "error", err)
		return states{}, err
	}
	slog.Debug("response.ADSState", "AdsState", stateResponse.AdsState, "DeviceState", stateResponse.DeviceState)

	return stateResponse, nil
}
