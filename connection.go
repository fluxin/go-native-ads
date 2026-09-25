package ads

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// NotificationCallback is a function that handles notification data
type NotificationCallback func(ctx context.Context, timestamp uint64, content []byte) error

type Connection struct {
	// Network configuration
	ip         string
	port       int
	connection net.Conn
	local      bool
	transport  ConnectionTransport
	unixSocket string

	// AMS addressing
	target      AmsAddress
	source      AmsAddress
	addressLock sync.RWMutex

	// Communication channels
	sendChannel    chan outgoingPacket
	systemResponse chan routerResponse
	systemLock     sync.Mutex
	timeout        time.Duration
	maxFrameSize   uint32

	// Request handling - invokeid → response channel
	currentRequest    uint32
	activeRequestLock sync.Mutex
	activeRequests    map[uint32]*pendingRequest

	// Symbol and notification state
	symbols                map[string]*Symbol
	activeNotifications    map[uint32]NotificationCallback
	pendingNotifications   map[uint32][]pendingNotification
	subscriptions          map[uint64]*subscriptionSpec
	notificationToSubID    map[uint32]uint64
	nextSubID              uint64
	symbolLock             sync.Mutex
	acquisitions           map[string]*handleAcquisition
	namedHandles           map[string]uint32
	restoreLock            sync.Mutex
	generationLock         generationMutex
	backgroundGroup        sync.WaitGroup
	closeOnce              sync.Once
	unknownCleanup         map[uint32]bool
	pendingBytes           int
	notificationGeneration uint64

	// Type information
	datatypes map[string]SymbolUploadDataType

	// Lifecycle management
	ctx      context.Context
	shutdown context.CancelFunc

	transportLock   sync.Mutex
	transportCancel context.CancelFunc
	transportCtx    context.Context
	transportGroup  sync.WaitGroup

	connectLock      sync.Mutex
	stateLock        sync.Mutex
	state            connectionState
	reconnectSignal  chan struct{}
	reconnectRunning atomic.Bool
	reconnectPolicy  ReconnectPolicy
	epoch            atomic.Uint64

	symbolVersion            uint8
	symbolVersionKnown       bool
	symbolVersionWatchHandle uint32
	symbolVersionRefreshing  atomic.Bool
	versionDirty             bool

	routerStateLock      sync.Mutex
	routerState          RouterState
	routerStateKnown     bool
	routerStateUpdatedAt time.Time

	testReconnectConnectFn          func() error
	testReconnectRefreshFn          func()
	testRestoreLookupSymbolFn       func(string) (*Symbol, error)
	testRestoreAddNotificationFn    func(group, offset, length uint32, mode TransMode, maxDelay, cycleTime time.Duration) (uint32, error)
	testDeleteUnknownNotificationFn func(uint32) error
	testUnknownNotificationDelay    time.Duration
}

const (
	localhostNetID       = "127.0.0.1.1.1"
	defaultUnixSocketAMS = "/run/ams/tcsyssrv.ams.sock"
)

// ConnectionTransport controls how the AMS router connection is established.
type ConnectionTransport string

const (
	// ConnectionTransportAuto uses the local Unix socket on Linux when it exists,
	// otherwise it falls back to TCP.
	ConnectionTransportAuto ConnectionTransport = ""
	// ConnectionTransportTCP forces TCP dialing using IP and Port.
	ConnectionTransportTCP ConnectionTransport = "tcp"
	// ConnectionTransportUnix forces Unix socket dialing using UnixSocketPath.
	ConnectionTransportUnix ConnectionTransport = "unix"
)

// ConnectionOptions configures an ADS connection.
type ConnectionOptions struct {
	// IP is the AMS router address. Defaults to "127.0.0.1".
	IP string
	// Port is the AMS router TCP port. Defaults to 48898.
	Port int
	// NetID is the target AMS Net ID (e.g., "192.168.1.100.1.1").
	// Use "localhost" or "" for local connections.
	NetID string
	// AMSPort is the target AMS port (e.g., 851 for the first PLC runtime).
	AMSPort int
	// SourceNetID is the local AMS Net ID. If empty, the router assigns one.
	SourceNetID string
	// SourcePort is the local AMS port. If zero, the router assigns one.
	SourcePort int
	// ReconnectPolicy controls automatic reconnection. Zero value uses DefaultReconnectPolicy().
	ReconnectPolicy ReconnectPolicy
	// Transport controls TCP vs local Unix socket dialing. Defaults to auto.
	Transport ConnectionTransport
	// UnixSocketPath is used when Transport is unix, or auto selects unix.
	// Defaults to /run/ams/tcsyssrv.ams.sock.
	UnixSocketPath string
	// RequestTimeout bounds ADS requests and router negotiation; default 4s.
	RequestTimeout time.Duration
	// MaxFrameSize bounds incoming and outgoing AMS payloads; default 16 MiB.
	MaxFrameSize uint32
}

// NewConnection creates a new ADS connection.
func NewConnection(ctx context.Context, opts ConnectionOptions) (conn *Connection, err error) {
	if opts.Port == 0 {
		opts.Port = 48898
	}
	if opts.IP == "" {
		opts.IP = "127.0.0.1"
	}
	if opts.NetID == "" || opts.NetID == "localhost" {
		opts.NetID = localhostNetID
	}
	if opts.UnixSocketPath == "" {
		opts.UnixSocketPath = defaultUnixSocketAMS
	}
	switch opts.Transport {
	case ConnectionTransportAuto, ConnectionTransportTCP, ConnectionTransportUnix:
	default:
		return nil, fmt.Errorf("invalid connection transport: %q", opts.Transport)
	}

	if opts.RequestTimeout < 0 {
		return nil, fmt.Errorf("negative request timeout")
	}
	if opts.MaxFrameSize != 0 && opts.MaxFrameSize < 32 {
		return nil, fmt.Errorf("frame limit must be at least 32 bytes")
	}
	if opts.AMSPort < 0 || opts.AMSPort > 65535 || opts.SourcePort < 0 || opts.SourcePort > 65535 {
		return nil, fmt.Errorf("AMS port out of range")
	}
	conn = &Connection{timeout: opts.RequestTimeout, maxFrameSize: opts.MaxFrameSize, ip: opts.IP, port: opts.Port, transport: opts.Transport, unixSocket: opts.UnixSocketPath}
	conn.local = opts.NetID == localhostNetID
	conn.target.NetID, err = stringToNetID(opts.NetID)
	if err != nil {
		return nil, fmt.Errorf("invalid target NetID: %w", err)
	}
	conn.target.Port = uint16(opts.AMSPort)
	if opts.SourceNetID != "" {
		conn.source.NetID, err = stringToNetID(opts.SourceNetID)
		if err != nil {
			return nil, fmt.Errorf("invalid source NetID: %w", err)
		}
	}
	conn.source.Port = uint16(opts.SourcePort)
	conn.systemResponse = make(chan routerResponse, 1)
	conn.activeRequests = map[uint32]*pendingRequest{}
	conn.activeNotifications = make(map[uint32]NotificationCallback)
	conn.unknownCleanup = make(map[uint32]bool)
	conn.pendingNotifications = make(map[uint32][]pendingNotification)
	conn.subscriptions = make(map[uint64]*subscriptionSpec)
	conn.notificationToSubID = make(map[uint32]uint64)
	conn.sendChannel = make(chan outgoingPacket)
	conn.reconnectSignal = make(chan struct{}, 1)
	conn.reconnectPolicy = normalizeReconnectPolicy(opts.ReconnectPolicy)
	conn.state = connectionStateDisconnected
	conn.ctx, conn.shutdown = context.WithCancel(ctx)
	return conn, nil
}

func (conn *Connection) Connect() error {
	conn.connectLock.Lock()
	defer conn.connectLock.Unlock()
	conn.generationLock.Lock()
	defer conn.generationLock.Unlock()
	conn.stateLock.Lock()
	if conn.state == connectionStateClosed || conn.ctx.Err() != nil {
		conn.stateLock.Unlock()
		return net.ErrClosed
	}
	if conn.state == connectionStateConnected {
		conn.stateLock.Unlock()
		return nil
	}
	conn.state = connectionStateConnecting
	conn.stateLock.Unlock()
	if err := conn.connectWithMetadata(); err != nil {
		conn.setState(connectionStateDisconnected)
		return err
	}
	conn.refreshAfterReconnect()
	conn.epoch.Add(1)
	return conn.publishConnected()
}

func (conn *Connection) connectWithMetadata() error {
	slog.Debug("Dialing", "ip", conn.ip, "port", conn.port)
	if conn.local {
		conn.addressLock.Lock()
		conn.target.NetID = [6]byte{127, 0, 0, 1, 1, 1}
		conn.addressLock.Unlock()
	}
	network, address := conn.dialTarget()
	dialer := net.Dialer{Timeout: conn.requestTimeout()}
	connection, err := dialer.DialContext(conn.ctx, network, address)
	if err != nil {
		return fmt.Errorf("dial %s %s: %w", network, address, err)
	}
	transportCtx := conn.activateTransport(connection)
	connected := false
	defer func() {
		if !connected {
			conn.stopTransport()
		}
	}()

	slog.Debug("Connected")
	conn.startTransportWorkers(transportCtx, connection)

	// Negotiate AMS address with the router (only when connecting via the AMS router port).
	if conn.local || conn.port == 48898 {
		resp, err := conn.sendSystem(buildRouterPortConnectPacket(0), true)
		if err != nil {
			return fmt.Errorf("AMS router address negotiation failed: %w", err)
		}
		assignedSource, err := parseRouterPortConnectResponse(resp)
		if err != nil {
			return fmt.Errorf("failed to parse router-assigned AMS address: %w", err)
		}
		slog.Info("Router assigned source", "source", assignedSource)
		conn.addressLock.Lock()
		conn.source = assignedSource
		conn.addressLock.Unlock()
	}

	datatypes, symbols, err := conn.fetchMetadata()
	if err != nil {
		return err
	}
	conn.symbolLock.Lock()
	conn.datatypes = datatypes
	conn.symbols = symbols
	conn.symbolLock.Unlock()
	connected = true
	return nil
}

func (conn *Connection) activateTransport(connection net.Conn) context.Context {
	conn.stopTransport()
	ctx, cancel := context.WithCancel(conn.ctx)
	conn.transportLock.Lock()
	conn.connection = connection
	conn.transportCancel = cancel
	conn.transportCtx = ctx
	conn.sendChannel = make(chan outgoingPacket)
	conn.systemResponse = make(chan routerResponse, 1)
	conn.transportLock.Unlock()
	return ctx
}

func (conn *Connection) startTransportWorkers(ctx context.Context, connection net.Conn) {
	conn.transportGroup.Add(2)
	go func() {
		defer conn.transportGroup.Done()
		conn.listen(ctx, connection)
	}()
	go func() {
		defer conn.transportGroup.Done()
		conn.transmitWorker(ctx, connection)
	}()
}

func (conn *Connection) stopTransport() {
	conn.transportLock.Lock()
	cancel := conn.transportCancel
	connection := conn.connection
	conn.transportCancel = nil
	conn.transportCtx = nil
	conn.connection = nil
	conn.transportLock.Unlock()

	if cancel != nil {
		cancel()
	}
	if connection != nil {
		_ = connection.Close()
	}
	conn.transportGroup.Wait()
}

func (conn *Connection) dialTarget() (network string, address string) {
	if conn.transport == ConnectionTransportUnix || conn.shouldAutoDialUnix() {
		return "unix", conn.unixSocket
	}
	return "tcp", net.JoinHostPort(conn.ip, strconv.Itoa(conn.port))
}

func (conn *Connection) shouldAutoDialUnix() bool {
	if conn.transport != ConnectionTransportAuto || !conn.local || runtime.GOOS != "linux" {
		return false
	}
	info, err := os.Stat(conn.unixSocket)
	return err == nil && info.Mode()&os.ModeSocket != 0
}

// Close cancels outstanding work, closes the transport, and joins owned workers.
// Remote releases are best effort and share a 100ms budget.
func (conn *Connection) Close() {
	conn.closeOnce.Do(func() {
		conn.stateLock.Lock()
		conn.state = connectionStateClosed
		conn.shutdown()
		conn.stateLock.Unlock()
		conn.connectLock.Lock()
		conn.generationLock.Lock()
		conn.closeTransport()
		conn.symbolLock.Lock()
		for _, spec := range conn.subscriptions {
			if spec.delivery != nil {
				spec.delivery.cancel()
			}
		}
		conn.symbolLock.Unlock()
		conn.generationLock.Unlock()
		conn.connectLock.Unlock()
		conn.backgroundGroup.Wait()
	})
}

// Called only after shutdown and with connectLock and generationLock held.
func (conn *Connection) closeTransport() {
	conn.transportLock.Lock()
	connection, cancel := conn.connection, conn.transportCancel
	conn.connection = nil
	conn.transportCancel = nil
	conn.transportCtx = nil
	conn.transportLock.Unlock()
	if cancel != nil {
		cancel()
	}
	if connection == nil {
		conn.transportGroup.Wait()
		return
	}
	// Interrupt reads/writes before joining; a user callback cannot hold these workers.
	_ = connection.SetDeadline(time.Now())
	conn.transportGroup.Wait()
	defer connection.Close()
	deadline := time.Now().Add(100 * time.Millisecond)
	_ = connection.SetWriteDeadline(deadline)
	send := func(packet []byte) bool {
		for len(packet) > 0 {
			if time.Now().After(deadline) {
				return false
			}
			n, err := connection.Write(packet)
			if err != nil || n == 0 {
				return false
			}
			packet = packet[n:]
		}
		return true
	}
	conn.symbolLock.Lock()
	notifications := make([]uint32, 0, len(conn.activeNotifications))
	for handle := range conn.activeNotifications {
		notifications = append(notifications, handle)
	}
	handles := make([]uint32, 0, len(conn.namedHandles))
	for _, handle := range conn.namedHandles {
		handles = append(handles, handle)
	}
	conn.symbolLock.Unlock()
	for _, handle := range notifications {
		data := make([]byte, 4)
		binary.LittleEndian.PutUint32(data, handle)
		packet, _ := conn.encode(CommandIDDeleteDeviceNotification, data, 0)
		if !send(packet) {
			return
		}
	}
	for _, handle := range handles {
		data := make([]byte, 16)
		binary.LittleEndian.PutUint32(data, uint32(GroupSymbolReleaseHandle))
		binary.LittleEndian.PutUint32(data[8:], 4)
		binary.LittleEndian.PutUint32(data[12:], handle)
		packet, _ := conn.encode(CommandIDWrite, data, 0)
		if !send(packet) {
			return
		}
	}
	conn.addressLock.RLock()
	port := conn.source.Port
	conn.addressLock.RUnlock()
	if port != 0 {
		send(buildRouterPortClosePacket(port))
	}
}
