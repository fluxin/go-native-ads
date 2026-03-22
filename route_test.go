package ads

import "testing"

func TestBuildAddRouteToPLCPacketValidation(t *testing.T) {
	_, err := buildAddRouteToPLCPacket(AddRouteToPLCRequest{})
	if err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestParseAddRouteToPLCResponse(t *testing.T) {
	okResp := make([]byte, 32)
	okResp[11] = 0x80
	okResp[24] = 0x04
	accepted, err := parseAddRouteToPLCResponse(okResp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !accepted {
		t.Fatalf("expected accepted route")
	}

	badPasswordResp := make([]byte, 32)
	badPasswordResp[11] = 0x80
	badPasswordResp[25] = 0x04
	badPasswordResp[26] = 0x07
	accepted, err = parseAddRouteToPLCResponse(badPasswordResp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if accepted {
		t.Fatalf("expected rejected route")
	}
}

func TestBuildDiscoverNetIDPacket(t *testing.T) {
	packet := buildDiscoverNetIDPacket()
	if len(packet) != 24 {
		t.Fatalf("expected 24-byte packet, got %d", len(packet))
	}
	if packet[11] != 0x00 {
		t.Fatalf("unexpected marker in request")
	}
}

func TestBuildAddRouteToPLCPacketWireLayout(t *testing.T) {
	packet, err := buildAddRouteToPLCPacket(AddRouteToPLCRequest{
		SendingNetID:   "1.2.3.4.1.1",
		AddingHostName: "host-a",
		PLCIP:          "127.0.0.1",
		Username:       "Administrator",
		Password:       "pw",
		RouteName:      "RouteA",
		AddedNetID:     "5.6.7.8.1.1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(packet) < 40 {
		t.Fatalf("packet too short: %d", len(packet))
	}
	if packet[8] != 0x06 || packet[11] != 0x00 {
		t.Fatalf("unexpected add-route service marker bytes: %x", packet[8:12])
	}
	if packet[18] != 0x10 || packet[19] != 0x27 {
		t.Fatalf("system service port must be 10000 little-endian, got: %02x %02x", packet[18], packet[19])
	}
	if packet[20] != 0x05 || packet[21] != 0x00 {
		t.Fatalf("expected write command marker at bytes 20..21")
	}
}

func TestRouterPortPacketHelpers(t *testing.T) {
	connect := buildRouterPortConnectPacket(0)
	if len(connect) != 8 {
		t.Fatalf("connect packet length mismatch: %d", len(connect))
	}
	if connect[0] != 0x00 || connect[1] != 0x10 {
		t.Fatalf("connect packet command mismatch: %02x %02x", connect[0], connect[1])
	}

	closePkt := buildRouterPortClosePacket(851)
	if len(closePkt) != 8 {
		t.Fatalf("close packet length mismatch: %d", len(closePkt))
	}
	if closePkt[0] != 0x01 || closePkt[1] != 0x00 {
		t.Fatalf("close packet command mismatch: %02x %02x", closePkt[0], closePkt[1])
	}
}

func TestParseRouterPortConnectResponse(t *testing.T) {
	resp := []byte{1, 2, 3, 4, 5, 6, 0x34, 0x12}
	addr, err := parseRouterPortConnectResponse(resp)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if addr.NetID != [6]byte{1, 2, 3, 4, 5, 6} {
		t.Fatalf("unexpected netid: %v", addr.NetID)
	}
	if addr.Port != 0x1234 {
		t.Fatalf("unexpected port: %d", addr.Port)
	}
}

func TestParseGetLocalNetIDResponse(t *testing.T) {
	netid, err := parseGetLocalNetIDResponse([]byte{11, 22, 33, 44, 55, 66})
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if netid != [6]byte{11, 22, 33, 44, 55, 66} {
		t.Fatalf("unexpected netid: %v", netid)
	}
}

func TestParseRouterNoteState(t *testing.T) {
	state, err := parseRouterNoteState([]byte{0x01, 0x00, 0x00, 0x00})
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if state != RouterStateStart {
		t.Fatalf("unexpected router state: %d", state)
	}
}

func TestNetIDFormatParseRoundTrip(t *testing.T) {
	netid := [6]byte{172, 30, 0, 2, 1, 1}
	formatted := FormatNetID(netid)
	if formatted != "172.30.0.2.1.1" {
		t.Fatalf("unexpected netid format: %s", formatted)
	}
	parsed, err := ParseNetID(formatted)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if parsed != netid {
		t.Fatalf("unexpected parsed netid: %v", parsed)
	}
}
