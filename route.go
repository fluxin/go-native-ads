package ads

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"time"
)

type RouterState uint32

const (
	RouterStateStop    RouterState = 0
	RouterStateStart   RouterState = 1
	RouterStateRemoved RouterState = 2
)

type RouteDiagnostics struct {
	RouterAddress string
	RouterPort    int
	Connected     bool
	Local         bool
	Source        AmsAddress
	Target        AmsAddress
	RouterState   RouterState
	StateKnown    bool
	StateUpdated  time.Time
}

type NetIDRouteInfo struct {
	IP    string
	NetID string
	Bytes [6]byte
}

type RouterClient struct {
	conn *Connection
}

const (
	adsRouteUDPPort      = 48899
	adsSystemServicePort = 10000
)

type AddRouteToPLCRequest struct {
	SendingNetID   string
	AddingHostName string
	PLCIP          string
	Username       string
	Password       string
	RouteName      string
	AddedNetID     string
}

func AddRouteToPLC(ctx context.Context, req AddRouteToPLCRequest) (bool, error) {
	payload, err := buildAddRouteToPLCPacket(req)
	if err != nil {
		return false, err
	}
	resp, err := sendRouteUDP(ctx, req.PLCIP, payload, 32)
	if err != nil {
		return false, err
	}
	return parseAddRouteToPLCResponse(resp)
}

func (conn *Connection) Router() RouterClient {
	return RouterClient{conn: conn}
}

func (r RouterClient) Diagnostics() RouteDiagnostics {
	r.conn.routerStateLock.Lock()
	state := r.conn.routerState
	known := r.conn.routerStateKnown
	updated := r.conn.routerStateUpdatedAt
	r.conn.routerStateLock.Unlock()

	r.conn.stateLock.Lock()
	connected := r.conn.state == connectionStateConnected || r.conn.state == connectionStateConnecting || r.conn.state == connectionStateReconnecting
	r.conn.stateLock.Unlock()

	return RouteDiagnostics{
		RouterAddress: r.conn.ip,
		RouterPort:    r.conn.port,
		Connected:     connected,
		Local:         r.conn.local,
		Source:        r.conn.source,
		Target:        r.conn.target,
		RouterState:   state,
		StateKnown:    known,
		StateUpdated:  updated,
	}
}

func (r RouterClient) StateSnapshot() (RouterState, bool, time.Time) {
	r.conn.routerStateLock.Lock()
	defer r.conn.routerStateLock.Unlock()
	return r.conn.routerState, r.conn.routerStateKnown, r.conn.routerStateUpdatedAt
}

func (r RouterClient) LocalNetID() (string, error) {
	resp, err := r.conn.send(buildGetLocalNetIDPacket())
	if err != nil {
		return "", err
	}
	netid, err := parseGetLocalNetIDResponse(resp)
	if err != nil {
		return "", err
	}
	return FormatNetID(netid), nil
}

func (r RouterClient) RegisterPort(requestedPort uint16) (AmsAddress, error) {
	resp, err := r.conn.send(buildRouterPortConnectPacket(requestedPort))
	if err != nil {
		return AmsAddress{}, err
	}
	return parseRouterPortConnectResponse(resp)
}

func (r RouterClient) UnregisterPort(port uint16) error {
	_, err := r.conn.send(buildRouterPortClosePacket(port))
	return err
}

func DiscoverNetIDInfo(ctx context.Context, ip string) (NetIDRouteInfo, error) {
	netID, err := DiscoverNetID(ctx, ip)
	if err != nil {
		return NetIDRouteInfo{}, err
	}
	bytes, err := ParseNetID(netID)
	if err != nil {
		return NetIDRouteInfo{}, err
	}
	return NetIDRouteInfo{IP: ip, NetID: netID, Bytes: bytes}, nil
}

func ParseNetID(value string) ([6]byte, error) {
	return stringToNetID(value)
}

func FormatNetID(value [6]byte) string {
	parts := make([]string, 6)
	for i := 0; i < 6; i++ {
		parts[i] = strconv.Itoa(int(value[i]))
	}
	return parts[0] + "." + parts[1] + "." + parts[2] + "." + parts[3] + "." + parts[4] + "." + parts[5]
}

func (conn *Connection) handleRouterNote(payload []byte) error {
	state, err := parseRouterNoteState(payload)
	if err != nil {
		return err
	}
	conn.routerStateLock.Lock()
	prev := conn.routerState
	known := conn.routerStateKnown
	conn.routerState = state
	conn.routerStateKnown = true
	conn.routerStateUpdatedAt = time.Now()
	conn.routerStateLock.Unlock()
	if !known || prev != state {
		slog.Info("router state changed", "previous", prev, "state", state)
	}
	return nil
}

func DiscoverNetID(ctx context.Context, ip string) (string, error) {
	payload := buildDiscoverNetIDPacket()
	resp, err := sendRouteUDP(ctx, ip, payload, 398)
	if err != nil {
		return "", err
	}
	return parseDiscoverNetIDResponse(resp)
}

func sendRouteUDP(ctx context.Context, ip string, payload []byte, maxResp int) ([]byte, error) {
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "udp", net.JoinHostPort(ip, fmt.Sprintf("%d", adsRouteUDPPort)))
	if err != nil {
		return nil, fmt.Errorf("route udp dial failed: %w", err)
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}

	if _, err := conn.Write(payload); err != nil {
		return nil, fmt.Errorf("route udp send failed: %w", err)
	}
	resp := make([]byte, maxResp)
	n, err := conn.Read(resp)
	if err != nil {
		return nil, fmt.Errorf("route udp read failed: %w", err)
	}
	return resp[:n], nil
}
