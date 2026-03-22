package ads

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

func (conn *Connection) ReadSymbolVersion() (uint8, error) {
	data, err := conn.Read(uint32(GroupSymbolVersion), 0, 1)
	if err != nil {
		return 0, err
	}
	return parseSingleBytePayload("ReadSymbolVersion", data)
}

func parseSingleBytePayload(op string, data []byte) (uint8, error) {
	if len(data) != 1 {
		return 0, fmt.Errorf("invalid %s payload length: %d", op, len(data))
	}
	return data[0], nil
}

func (conn *Connection) ensureSymbolVersionWatcher() {
	version, err := conn.ReadSymbolVersion()
	if err != nil {
		slog.Debug("symbol version read not available", "error", err)
		return
	}

	conn.symbolLock.Lock()
	conn.symbolVersion = version
	conn.symbolVersionKnown = true
	if conn.symbolVersionWatchHandle != 0 {
		conn.symbolLock.Unlock()
		return
	}
	conn.symbolLock.Unlock()

	handle, err := conn.AddDeviceNotification(
		uint32(GroupSymbolVersion),
		0,
		1,
		TransModeServerOnChange,
		100*time.Millisecond,
		100*time.Millisecond,
	)
	if err != nil {
		slog.Debug("symbol version watcher unavailable", "error", err)
		return
	}

	callback := func(ctx context.Context, timestamp uint64, content []byte) error {
		_ = ctx
		_ = timestamp
		newVersion, err := parseSingleBytePayload("SymbolVersionNotification", content)
		if err != nil {
			return err
		}
		conn.onSymbolVersionChanged(newVersion)
		return nil
	}

	conn.symbolLock.Lock()
	conn.symbolVersionWatchHandle = handle
	conn.activeNotifications[handle] = callback
	conn.symbolLock.Unlock()

	slog.Debug("symbol version watcher registered", "handle", handle, "version", version)
}

func (conn *Connection) onSymbolVersionChanged(newVersion uint8) {
	conn.symbolLock.Lock()
	if !conn.symbolVersionKnown {
		conn.symbolVersion = newVersion
		conn.symbolVersionKnown = true
		conn.symbolLock.Unlock()
		return
	}
	oldVersion := conn.symbolVersion
	if oldVersion == newVersion {
		conn.symbolLock.Unlock()
		return
	}
	conn.symbolVersion = newVersion
	conn.symbolLock.Unlock()

	if !conn.symbolVersionRefreshing.CompareAndSwap(false, true) {
		return
	}

	go func(from uint8, to uint8) {
		defer conn.symbolVersionRefreshing.Store(false)
		slog.Info("symbol version changed; refreshing metadata", "from", from, "to", to)
		if err := conn.refreshMetadataAndSubscriptions(); err != nil {
			slog.Error("symbol version refresh failed", "from", from, "to", to, "error", err)
		}
	}(oldVersion, newVersion)
}

func (conn *Connection) refreshMetadataAndSubscriptions() error {
	datatypes, symbols, err := conn.fetchMetadata()
	if err != nil {
		return err
	}
	conn.cleanupActiveNotificationHandles()

	conn.symbolLock.Lock()
	conn.datatypes = datatypes
	conn.symbols = symbols
	conn.resetNotificationStateLocked()
	subs := conn.subscriptionSnapshotLocked()
	conn.symbolLock.Unlock()

	conn.restoreSubscriptions(subs)
	conn.epoch.Add(1)
	conn.ensureSymbolVersionWatcher()
	return nil
}

func (conn *Connection) resetNotificationStateLocked() {
	conn.pendingNotifications = make(map[uint32][]pendingNotification)
	conn.activeNotifications = make(map[uint32]NotificationCallback)
	conn.notificationToSubID = make(map[uint32]uint64)
	conn.symbolVersionWatchHandle = 0

	for _, spec := range conn.subscriptions {
		spec.adsHandle = 0
		spec.group = uint32(GroupSymbolValueByHandle)
	}
	for _, symbol := range conn.symbols {
		symbol.Handle = 0
	}
}

func (conn *Connection) activeNotificationHandlesSnapshot() []uint32 {
	conn.symbolLock.Lock()
	defer conn.symbolLock.Unlock()
	handles := make([]uint32, 0, len(conn.activeNotifications))
	for handle := range conn.activeNotifications {
		handles = append(handles, handle)
	}
	return handles
}

func (conn *Connection) cleanupActiveNotificationHandles() {
	conn.stateLock.Lock()
	state := conn.state
	conn.stateLock.Unlock()
	if state != connectionStateConnected && state != connectionStateConnecting && state != connectionStateReconnecting {
		return
	}
	handles := conn.activeNotificationHandlesSnapshot()
	for _, handle := range handles {
		if err := conn.DeleteDeviceNotification(handle); err != nil {
			slog.Debug("best-effort delete notification failed", "handle", handle, "error", err)
		}
	}
}

func (conn *Connection) subscriptionSnapshotLocked() []*subscriptionSpec {
	subs := make([]*subscriptionSpec, 0, len(conn.subscriptions))
	for _, spec := range conn.subscriptions {
		subs = append(subs, spec)
	}
	return subs
}

func (conn *Connection) restoreSubscriptions(subs []*subscriptionSpec) {
	for _, spec := range subs {
		symbol, err := conn.lookupRestoreSymbol(spec.symbolName)
		if err != nil {
			slog.Error("failed to rebind subscription symbol", "id", spec.id, "symbol", spec.symbolName, "error", err)
			continue
		}

		handle, err := conn.addRestoreNotification(
			uint32(GroupSymbolValueByHandle),
			symbol.Handle,
			symbol.Length,
			spec.mode,
			spec.maxDelay,
			spec.cycleTime,
		)
		if err != nil {
			slog.Error("failed to restore subscription", "id", spec.id, "symbol", spec.symbolName, "error", err)
			continue
		}

		conn.symbolLock.Lock()
		spec.group = uint32(GroupSymbolValueByHandle)
		spec.offset = symbol.Handle
		spec.length = symbol.Length
		spec.adsHandle = handle
		conn.activeNotifications[handle] = spec.callback
		conn.notificationToSubID[handle] = spec.id
		conn.symbolLock.Unlock()
	}
}

func (conn *Connection) lookupRestoreSymbol(symbolName string) (*Symbol, error) {
	if conn.testRestoreLookupSymbolFn != nil {
		return conn.testRestoreLookupSymbolFn(symbolName)
	}
	return conn.GetSymbol(symbolName)
}

func (conn *Connection) addRestoreNotification(group, offset, length uint32, mode TransMode, maxDelay, cycleTime time.Duration) (uint32, error) {
	if conn.testRestoreAddNotificationFn != nil {
		return conn.testRestoreAddNotificationFn(group, offset, length, mode, maxDelay, cycleTime)
	}
	return conn.AddDeviceNotification(group, offset, length, mode, maxDelay, cycleTime)
}
