package ads

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
)

const (
	amsTCPPortClose      uint16 = 0x0001
	amsTCPPortConnect    uint16 = 0x1000
	amsTCPPortRouterNote uint16 = 0x1001
	amsTCPGetLocalNetID  uint16 = 0x1002

	routeServiceDiscoverNetID uint32 = 0x00000001
	routeServiceAddRoute      uint32 = 0x00000006
	routeResponseMarker              = 0x80
)

var (
	routeUDPHeaderPrefix = [8]byte{0x03, 0x66, 0x14, 0x71, 0x00, 0x00, 0x00, 0x00}

	routeCommandWrite      = [2]byte{0x05, 0x00}
	routeRouteNamePrefix   = [4]byte{0x00, 0x00, 0x0c, 0x00}
	routeRouteNameSuffix   = [2]byte{0x07, 0x00}
	routeAddedNetIDPrefix  = [2]byte{0x06, 0x00}
	routeUsernamePrefix    = [2]byte{0x0d, 0x00}
	routePasswordPrefix    = [2]byte{0x02, 0x00}
	routeHostNamePrefix    = [2]byte{0x05, 0x00}
	routeAcceptedStatus    = [3]byte{0x04, 0x00, 0x00}
	routeBadPasswordStatus = [3]byte{0x00, 0x04, 0x07}
)

type routeEndpoint struct {
	NetID [6]byte
	Port  uint16
}

type amsTCPSystemHeader struct {
	Command uint16
	Length  uint32
}

func buildRouterPortConnectPacket(requestedPort uint16) []byte {
	buf := bytes.NewBuffer(make([]byte, 0, 8))
	_ = binary.Write(buf, binary.LittleEndian, amsTCPSystemHeader{Command: amsTCPPortConnect, Length: 2})
	_ = binary.Write(buf, binary.LittleEndian, requestedPort)
	return buf.Bytes()
}

func parseRouterPortConnectResponse(resp []byte) (AmsAddress, error) {
	if len(resp) != 8 {
		return AmsAddress{}, fmt.Errorf("invalid router connect response length: %d", len(resp))
	}
	buf := bytes.NewBuffer(resp)
	address := AmsAddress{}
	if err := binary.Read(buf, binary.LittleEndian, &address); err != nil {
		return AmsAddress{}, fmt.Errorf("failed to parse router connect response: %w", err)
	}
	return address, nil
}

func buildRouterPortClosePacket(port uint16) []byte {
	buf := bytes.NewBuffer(make([]byte, 0, 8))
	_ = binary.Write(buf, binary.LittleEndian, amsTCPSystemHeader{Command: amsTCPPortClose, Length: 2})
	_ = binary.Write(buf, binary.LittleEndian, port)
	return buf.Bytes()
}

func buildGetLocalNetIDPacket() []byte {
	buf := bytes.NewBuffer(make([]byte, 0, 6))
	_ = binary.Write(buf, binary.LittleEndian, amsTCPSystemHeader{Command: amsTCPGetLocalNetID, Length: 0})
	return buf.Bytes()
}

func parseGetLocalNetIDResponse(resp []byte) ([6]byte, error) {
	if len(resp) < 6 {
		return [6]byte{}, fmt.Errorf("local netid response too short: %d", len(resp))
	}
	var netid [6]byte
	copy(netid[:], resp[:6])
	return netid, nil
}

func parseRouterNoteState(resp []byte) (RouterState, error) {
	if len(resp) < 4 {
		return 0, fmt.Errorf("router note response too short: %d", len(resp))
	}
	return RouterState(binary.LittleEndian.Uint32(resp[:4])), nil
}

func buildDiscoverNetIDPacket() []byte {
	buf := bytes.NewBuffer(make([]byte, 0, 24))
	writeRouteHeader(buf, routeServiceDiscoverNetID)
	_ = binary.Write(buf, binary.LittleEndian, routeEndpoint{
		NetID: [6]byte{1, 1, 1, 1, 1, 1},
		Port:  adsSystemServicePort,
	})
	buf.Write([]byte{0x00, 0x00, 0x00, 0x00})
	return buf.Bytes()
}

func buildAddRouteToPLCPacket(req AddRouteToPLCRequest) ([]byte, error) {
	sender, addedNetID, err := validateAddRouteRequest(req)
	if err != nil {
		return nil, err
	}

	routeName := nullTerminate(ifEmpty(req.RouteName, req.AddingHostName))
	addingHost := nullTerminate(req.AddingHostName)
	username := nullTerminate(req.Username)
	password := nullTerminate(req.Password)

	buf := bytes.NewBuffer(make([]byte, 0, 256))
	writeRouteHeader(buf, routeServiceAddRoute)
	if err := binary.Write(buf, binary.LittleEndian, routeEndpoint{NetID: sender, Port: adsSystemServicePort}); err != nil {
		return nil, fmt.Errorf("failed to encode route sender endpoint: %w", err)
	}

	buf.Write(routeCommandWrite[:])
	buf.Write(routeRouteNamePrefix[:])
	writeU16String(buf, routeName)
	buf.Write(routeRouteNameSuffix[:])

	buf.Write(routeAddedNetIDPrefix[:])
	buf.Write(addedNetID[:])
	buf.Write(routeUsernamePrefix[:])
	writeU16String(buf, username)
	buf.Write(routePasswordPrefix[:])
	writeU16String(buf, password)
	buf.Write(routeHostNamePrefix[:])
	writeU16String(buf, addingHost)

	return buf.Bytes(), nil
}

func parseDiscoverNetIDResponse(resp []byte) (string, error) {
	if len(resp) < 18 {
		return "", fmt.Errorf("discover netid response too short: %d", len(resp))
	}
	if resp[11] != routeResponseMarker {
		return "", fmt.Errorf("unexpected discover netid response marker: 0x%02x", resp[11])
	}
	parts := make([]string, 6)
	for i := range 6 {
		parts[i] = fmt.Sprintf("%d", resp[12+i])
	}
	return strings.Join(parts, "."), nil
}

func parseAddRouteToPLCResponse(resp []byte) (bool, error) {
	if len(resp) < 31 {
		return false, fmt.Errorf("route response too short: %d", len(resp))
	}
	if resp[11] != routeResponseMarker {
		return false, fmt.Errorf("unexpected route response marker: 0x%02x", resp[11])
	}

	status := [3]byte{resp[24], resp[25], resp[26]}
	if status == routeAcceptedStatus {
		return true, nil
	}
	if status == routeBadPasswordStatus {
		return false, nil
	}
	return false, fmt.Errorf("unexpected route response status: %x", status)
}

func validateAddRouteRequest(req AddRouteToPLCRequest) ([6]byte, [6]byte, error) {
	if req.SendingNetID == "" || req.AddingHostName == "" || req.PLCIP == "" || req.Username == "" {
		return [6]byte{}, [6]byte{}, fmt.Errorf("sending netid, host name, plc ip and username are required")
	}
	sender, err := stringToNetID(req.SendingNetID)
	if err != nil {
		return [6]byte{}, [6]byte{}, fmt.Errorf("invalid sending netid: %w", err)
	}
	addedValue := ifEmpty(req.AddedNetID, req.SendingNetID)
	added, err := stringToNetID(addedValue)
	if err != nil {
		return [6]byte{}, [6]byte{}, fmt.Errorf("invalid added netid: %w", err)
	}
	return sender, added, nil
}

func writeRouteHeader(buf *bytes.Buffer, service uint32) {
	buf.Write(routeUDPHeaderPrefix[:])
	_ = binary.Write(buf, binary.LittleEndian, service)
}

func writeU16String(buf *bytes.Buffer, value string) {
	_ = binary.Write(buf, binary.LittleEndian, uint16(len(value)))
	buf.WriteString(value)
}

func nullTerminate(value string) string {
	return value + "\x00"
}

func ifEmpty(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
