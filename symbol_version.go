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
	data, err := conn.read(uint32(GroupSymbolVersion), 0, 1, true)
	var version uint8
	if err == nil {
		version, err = parseSingleBytePayload("ReadSymbolVersion", data)
	}
	if err != nil {
		slog.Debug("symbol version read not available", "error", err)
		return
	}

	conn.symbolLock.Lock()
	if !conn.symbolVersionKnown {
		conn.symbolVersion = version
		conn.symbolVersionKnown = true
	}
	if conn.symbolVersionWatchHandle != 0 {
		conn.symbolLock.Unlock()
		return
	}
	conn.symbolLock.Unlock()

	handle, err := conn.addDeviceNotification(
		uint32(GroupSymbolVersion),
		0,
		1,
		TransModeServerOnChange,
		100*time.Millisecond,
		100*time.Millisecond, true,
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
	pending := conn.pendingNotifications[handle]
	for _, item := range pending {
		conn.pendingBytes -= len(item.content)
	}
	delete(conn.pendingNotifications, handle)
	conn.symbolLock.Unlock()

	conn.onSymbolVersionChanged(version)
	for _, item := range pending {
		_ = callback(conn.ctx, item.timestamp, item.content)
	}
	slog.Debug("symbol version watcher registered", "handle", handle, "version", version)
}

func (conn *Connection) onSymbolVersionChanged(version uint8) {
	conn.symbolLock.Lock()
	if !conn.symbolVersionKnown {
		conn.symbolVersion = version
		conn.symbolVersionKnown = true
		conn.symbolLock.Unlock()
		return
	}
	if conn.symbolVersion == version {
		conn.symbolLock.Unlock()
		return
	}
	conn.symbolVersion = version
	conn.versionDirty = true
	if conn.symbolVersionRefreshing.Load() {
		conn.symbolLock.Unlock()
		return
	}
	conn.symbolVersionRefreshing.Store(true)
	conn.symbolLock.Unlock()
	if !conn.startBackground(func() {
		for {
			conn.symbolLock.Lock()
			dirty := conn.versionDirty
			conn.versionDirty = false
			if !dirty {
				conn.symbolVersionRefreshing.Store(false)
				conn.symbolLock.Unlock()
				return
			}
			conn.symbolLock.Unlock()
			if err := conn.refreshMetadataAndSubscriptions(); err != nil {
				slog.Error("symbol metadata refresh failed", "error", err)
				conn.symbolLock.Lock()
				conn.symbolVersionRefreshing.Store(false)
				conn.symbolLock.Unlock()
				return
			}
		}
	}) {
		conn.symbolVersionRefreshing.Store(false)
	}
}
func (conn *Connection) refreshMetadataAndSubscriptions() error {
	conn.connectLock.Lock()
	defer conn.connectLock.Unlock()
	conn.generationLock.Lock()
	defer conn.generationLock.Unlock()
	if err := conn.ensureConnected(); err != nil {
		return err
	}
	conn.setState(connectionStateConnecting)
	datatypes, symbols, err := conn.fetchMetadata()
	if err != nil {
		conn.onTransportError(err)
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
	return conn.publishConnected()
}

func (conn *Connection) resetNotificationStateLocked() {
	conn.namedHandles = nil
	conn.pendingBytes = 0
	conn.notificationGeneration++
	conn.unknownCleanup = make(map[uint32]bool)
	conn.pendingNotifications = make(map[uint32][]pendingNotification)
	conn.activeNotifications = make(map[uint32]NotificationCallback)
	conn.notificationToSubID = make(map[uint32]uint64)
	conn.symbolVersionWatchHandle = 0

	for _, spec := range conn.subscriptions {
		if spec.delivery != nil {
			spec.delivery.cancel()
		}
		spec.adsHandle = 0
		spec.group = uint32(GroupSymbolValueByHandle)
	}
	for name, symbol := range conn.symbols {
		copySymbol := cloneSymbol(symbol)
		copySymbol.Handle = 0
		conn.symbols[name] = copySymbol
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
		if err := conn.deleteDeviceNotification(handle, true); err != nil {
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
		conn.symbolLock.Lock()
		current, exists := conn.subscriptions[spec.id]
		oldDelivery := spec.delivery
		conn.symbolLock.Unlock()
		if !exists || current != spec {
			continue
		}
		if oldDelivery != nil {
			oldDelivery.cancel()
			<-oldDelivery.done
		}
		symbol, err := conn.lookupRestoreSymbol(spec.symbolName)
		if err == nil && symbol.Length > notificationQueueBytes {
			err = fmt.Errorf("notification exceeds queue byte limit")
		}
		callback := spec.callback
		if err == nil && spec.factory != nil {
			callback, err = spec.factory(symbol, conn.datatypeSnapshot())
		}
		if err != nil {
			conn.symbolLock.Lock()
			spec.err = err
			conn.symbolLock.Unlock()
			continue
		}
		handle, err := conn.addRestoreNotification(uint32(GroupSymbolValueByHandle), symbol.Handle, symbol.Length, spec.mode, spec.maxDelay, spec.cycleTime)
		if err != nil {
			conn.symbolLock.Lock()
			spec.err = err
			conn.symbolLock.Unlock()
			continue
		}
		var delivery *notificationDelivery
		if spec.factory != nil {
			delivery = conn.newDelivery(spec, callback)
			callback = delivery.callback(spec)
		}
		conn.symbolLock.Lock()
		current, exists = conn.subscriptions[spec.id]
		if !exists || current != spec || (spec.ctx != nil && spec.ctx.Err() != nil) {
			conn.symbolLock.Unlock()
			if delivery != nil {
				delivery.cancel()
			}
			_ = conn.deleteDeviceNotification(handle, true)
			continue
		}
		spec.group = uint32(GroupSymbolValueByHandle)
		spec.offset = symbol.Handle
		spec.length = symbol.Length
		spec.adsHandle = handle
		spec.err = nil
		spec.callback = callback
		spec.delivery = delivery
		conn.activeNotifications[handle] = callback
		conn.notificationToSubID[handle] = spec.id
		conn.drainPendingLocked(handle, callback)
		conn.symbolLock.Unlock()
	}
}

func (conn *Connection) lookupRestoreSymbol(symbolName string) (*Symbol, error) {
	if conn.testRestoreLookupSymbolFn != nil {
		return conn.testRestoreLookupSymbolFn(symbolName)
	}
	return conn.lookupSymbol(symbolName, true)
}

func (conn *Connection) addRestoreNotification(group, offset, length uint32, mode TransMode, maxDelay, cycleTime time.Duration) (uint32, error) {
	if conn.testRestoreAddNotificationFn != nil {
		return conn.testRestoreAddNotificationFn(group, offset, length, mode, maxDelay, cycleTime)
	}
	return conn.addDeviceNotification(group, offset, length, mode, maxDelay, cycleTime, true)
}
