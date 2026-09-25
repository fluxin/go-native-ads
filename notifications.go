package ads

import (
	"context"
	"fmt"
	"sync"
)

const notificationQueueSize = 64
const notificationQueueBytes = 1 << 20

type notificationDelivery struct {
	ctx         context.Context
	cancel      context.CancelFunc
	queue       chan pendingNotification
	done        chan struct{}
	enqueueLock sync.Mutex
	queuedBytes int
}

func (conn *Connection) newDelivery(spec *subscriptionSpec, callback NotificationCallback) *notificationDelivery {
	parent := spec.ctx
	if parent == nil {
		parent = conn.ctx
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	delivery := &notificationDelivery{ctx: ctx, cancel: cancel, queue: make(chan pendingNotification, notificationQueueSize), done: make(chan struct{})}
	if !conn.startBackground(func() {
		defer close(delivery.done)
		for {
			select {
			case <-ctx.Done():
				return
			case item := <-delivery.queue:
				delivery.enqueueLock.Lock()
				delivery.queuedBytes -= len(item.content)
				delivery.enqueueLock.Unlock()
				if ctx.Err() != nil {
					return
				}
				if err := callback(ctx, item.timestamp, item.content); err != nil && ctx.Err() == nil {
					conn.symbolLock.Lock()
					spec.err = err
					conn.symbolLock.Unlock()
				}
			}
		}
	}) {
		cancel()
		close(delivery.done)
	}
	return delivery
}
func (delivery *notificationDelivery) callback(spec *subscriptionSpec) NotificationCallback {
	return func(_ context.Context, timestamp uint64, content []byte) error {
		delivery.enqueueLock.Lock()
		defer delivery.enqueueLock.Unlock()
		if delivery.ctx.Err() != nil {
			return delivery.ctx.Err()
		}
		if len(delivery.queue) == cap(delivery.queue) || delivery.queuedBytes+len(content) > notificationQueueBytes {
			spec.dropped.Add(1)
			return nil
		}
		// The frame can contain many samples. Retain only this sample in the queue.
		item := pendingNotification{timestamp: timestamp, content: append([]byte(nil), content...)}
		select {
		case delivery.queue <- item:
			delivery.queuedBytes += len(content)
		case <-delivery.ctx.Done():
			return delivery.ctx.Err()
		}
		return nil
	}
}
func (conn *Connection) subscribe(symbol *Symbol, o SubscribeOptions, factory func(*Symbol, map[string]SymbolUploadDataType) (NotificationCallback, error)) (*Subscription, error) {
	if symbol.Length > notificationQueueBytes {
		return nil, fmt.Errorf("notification exceeds queue byte limit")
	}
	conn.restoreLock.Lock()
	defer conn.restoreLock.Unlock()
	callback, err := factory(symbol, conn.datatypeSnapshot())
	if err != nil {
		return nil, err
	}
	handle, err := conn.addDeviceNotification(uint32(GroupSymbolValueByHandle), symbol.Handle, symbol.Length, o.Mode, o.MaxDelay, o.CycleTime, true)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(conn.ctx)
	spec := &subscriptionSpec{symbolName: symbol.FullName, group: uint32(GroupSymbolValueByHandle), offset: symbol.Handle, length: symbol.Length, mode: o.Mode, maxDelay: o.MaxDelay, cycleTime: o.CycleTime, factory: factory, ctx: ctx, cancel: cancel, adsHandle: handle}
	spec.delivery = conn.newDelivery(spec, callback)
	spec.callback = spec.delivery.callback(spec)
	conn.symbolLock.Lock()
	conn.nextSubID++
	spec.id = conn.nextSubID
	conn.subscriptions[spec.id] = spec
	conn.activeNotifications[handle] = spec.callback
	conn.notificationToSubID[handle] = spec.id
	conn.drainPendingLocked(handle, spec.callback)
	conn.symbolLock.Unlock()
	return &Subscription{conn: conn, id: spec.id, adsHandle: handle, spec: spec}, nil
}
func (conn *Connection) drainPendingLocked(handle uint32, callback NotificationCallback) {
	pending := conn.pendingNotifications[handle]
	delete(conn.pendingNotifications, handle)
	for _, item := range pending {
		conn.pendingBytes -= len(item.content)
		_ = callback(conn.ctx, item.timestamp, item.content)
	}
}

// Retry restores failed subscriptions against the current schema. Cancellation wins
// over a retry already in progress. Err remains available if restoration fails.
func (s *Subscription) Retry() error {
	if err := s.conn.beginOperation(); err != nil {
		return err
	}
	defer s.conn.endOperation()
	s.conn.restoreLock.Lock()
	defer s.conn.restoreLock.Unlock()
	s.conn.symbolLock.Lock()
	spec, ok := s.conn.subscriptions[s.id]
	active := ok && spec.adsHandle != 0
	s.conn.symbolLock.Unlock()
	if !ok {
		return fmt.Errorf("subscription is canceled")
	}
	if active {
		return nil
	}
	s.conn.restoreSubscriptions([]*subscriptionSpec{spec})
	return s.Err()
}
func (conn *Connection) cancelSubscription(id uint64) error {
	conn.symbolLock.Lock()
	spec, ok := conn.subscriptions[id]
	if !ok {
		conn.symbolLock.Unlock()
		return nil
	}
	delete(conn.subscriptions, id)
	handle := spec.adsHandle
	spec.adsHandle = 0
	if spec.cancel != nil {
		spec.cancel()
	}
	delivery := spec.delivery
	if delivery != nil {
		delivery.cancel()
	}
	delete(conn.notificationToSubID, handle)
	delete(conn.activeNotifications, handle)
	conn.symbolLock.Unlock()
	if delivery != nil {
		<-delivery.done
	}
	if handle == 0 {
		return nil
	}
	if err := conn.beginOperation(); err != nil {
		return nil
	}
	defer conn.endOperation()
	conn.restoreLock.Lock()
	defer conn.restoreLock.Unlock()
	return conn.deleteDeviceNotification(handle, true)
}
