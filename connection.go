package ads

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
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

	// AMS addressing
	target AmsAddress
	source AmsAddress

	// Communication channels
	sendChannel    chan []byte
	systemResponse chan []byte

	// Request handling - invokeid → response channel
	currentRequest    uint32
	activeRequestLock sync.Mutex
	activeRequests    map[uint32]chan []byte

	// Symbol and notification state
	symbols              map[string]*Symbol
	activeNotifications  map[uint32]NotificationCallback
	pendingNotifications map[uint32][]pendingNotification
	subscriptions        map[uint64]*subscriptionSpec
	notificationToSubID  map[uint32]uint64
	nextSubID            uint64
	symbolLock           sync.Mutex

	// Type information
	datatypes map[string]SymbolUploadDataType

	// Lifecycle management
	ctx       context.Context
	shutdown  context.CancelFunc
	waitGroup sync.WaitGroup

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

	routerStateLock      sync.Mutex
	routerState          RouterState
	routerStateKnown     bool
	routerStateUpdatedAt time.Time

	testReconnectConnectFn          func() error
	testReconnectRefreshFn          func()
	testRestoreLookupSymbolFn       func(string) (*Symbol, error)
	testRestoreAddNotificationFn    func(group, offset, length uint32, mode TransMode, maxDelay, cycleTime time.Duration) (uint32, error)
	testDeleteUnknownNotificationFn func(uint32) error
}

// NewConnection creates a new ADS connection
func NewConnection(ctx context.Context, ip string, port int, netid string, amsPort int, localNetID string, localPort int, local bool, reconnectPolicy ReconnectPolicy) (conn *Connection, err error) {
	conn = &Connection{ip: ip, port: port, local: local}
	conn.target.NetID, err = stringToNetID(netid)
	if err != nil {
		return nil, fmt.Errorf("invalid target NetID: %w", err)
	}
	conn.target.Port = uint16(amsPort)
	conn.source.NetID, err = stringToNetID(localNetID)
	if err != nil {
		return nil, fmt.Errorf("invalid source NetID: %w", err)
	}
	conn.source.Port = uint16(localPort)
	conn.systemResponse = make(chan []byte)
	conn.activeRequests = map[uint32]chan []byte{}
	conn.activeNotifications = make(map[uint32]NotificationCallback)
	conn.pendingNotifications = make(map[uint32][]pendingNotification)
	conn.subscriptions = make(map[uint64]*subscriptionSpec)
	conn.notificationToSubID = make(map[uint32]uint64)
	conn.sendChannel = make(chan []byte)
	conn.reconnectSignal = make(chan struct{}, 1)
	conn.reconnectPolicy = normalizeReconnectPolicy(reconnectPolicy)
	conn.state = connectionStateDisconnected
	conn.ctx, conn.shutdown = context.WithCancel(ctx)
	return conn, nil
}

func (conn *Connection) Connect() error {
	conn.connectLock.Lock()
	defer conn.connectLock.Unlock()

	conn.stateLock.Lock()
	conn.state = connectionStateConnecting
	conn.stateLock.Unlock()
	if err := conn.connectWithMetadata(); err != nil {
		conn.stateLock.Lock()
		conn.state = connectionStateDisconnected
		conn.stateLock.Unlock()
		return err
	}
	conn.stateLock.Lock()
	conn.state = connectionStateConnected
	conn.stateLock.Unlock()
	conn.epoch.Add(1)
	conn.ensureSymbolVersionWatcher()
	return nil
}

func (conn *Connection) connectWithMetadata() error {
	slog.Debug("Dialing", "ip", conn.ip, "port", conn.port)
	if conn.local {
		conn.target.NetID = [6]byte{127, 0, 0, 1, 1, 1}
		conn.ip = "127.0.0.1"
	}
	var err error
	conn.connection, err = net.Dial("tcp", net.JoinHostPort(conn.ip, strconv.Itoa(conn.port)))
	if err != nil {
		return fmt.Errorf("dial %s: %w", conn.ip, err)
	}
	slog.Debug("Connected")
	conn.listen()
	go conn.transmitWorker()

	// Negotiate AMS address with the router (only when connecting via the AMS router port).
	if conn.local || conn.port == 48898 {
		resp, err := conn.send(buildRouterPortConnectPacket(0))
		if err != nil {
			return fmt.Errorf("AMS router address negotiation failed: %w", err)
		}
		assignedSource, err := parseRouterPortConnectResponse(resp)
		if err != nil {
			return fmt.Errorf("failed to parse router-assigned AMS address: %w", err)
		}
		slog.Info("Router assigned source", "source", assignedSource)
		conn.source = assignedSource
	}

	datatypes, symbols, err := conn.fetchMetadata()
	if err != nil {
		return err
	}
	conn.symbolLock.Lock()
	conn.datatypes = datatypes
	conn.symbols = symbols
	conn.symbolLock.Unlock()
	return nil
}

// Close closes connection and waits for completion
func (conn *Connection) Close() {
	slog.Debug("closing ADS connection")
	slog.Debug("sending shutdown to workers")
	for handle := range conn.activeNotifications {
		conn.DeleteDeviceNotification(handle)
		slog.Debug("removed notification handle", "handle", handle)
	}
	conn.symbolLock.Lock()
	for _, symbol := range conn.symbols {
		if symbol.Handle != 0 {
			slog.Debug("releasing symbol handle", "handle", symbol.Handle)
			handleBytes := make([]byte, 4)
			binary.LittleEndian.PutUint32(handleBytes, symbol.Handle)
			conn.Write(uint32(GroupSymbolReleaseHandle), 0, handleBytes)
		}
	}
	conn.symbolLock.Unlock()
	conn.stateLock.Lock()
	conn.state = connectionStateClosed
	conn.stateLock.Unlock()
	conn.shutdown()
	slog.Debug("waiting for workers to close")
	conn.waitGroup.Wait()
	slog.Debug("ADS connection closed")
	if conn.connection != nil {
		conn.connection.Close()
	}
}
