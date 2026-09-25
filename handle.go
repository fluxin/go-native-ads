package ads

import (
	"fmt"
	"reflect"
	"sync"
	"time"
)

// Handle is a reusable typed symbol binding. Its operations are safe concurrently.
type Handle[T any] struct {
	mu sync.Mutex
	symbolBinding
}

func GetHandle[T any](conn *Connection, name string) (*Handle[T], error) {
	if conn == nil {
		return nil, fmt.Errorf("nil connection")
	}
	h := &Handle[T]{symbolBinding: symbolBinding{conn: conn, symbolName: name, typ: reflect.TypeFor[T]()}}
	if err := h.ensureBound(); err != nil {
		return nil, err
	}
	return h, nil
}
func (h *Handle[T]) binding() (*Symbol, *codecNode, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.typ = reflect.TypeFor[T]()
	if err := h.symbolBinding.bind(); err != nil {
		return nil, nil, err
	}
	return h.symbol, h.codec, nil
}

func (h *Handle[T]) ensureBound() error {
	if err := h.conn.beginOperation(); err != nil {
		return err
	}
	defer h.conn.endOperation()
	_, _, err := h.binding()
	return err
}
func (h *Handle[T]) Read() (T, error) {
	var value T
	if err := h.conn.beginOperation(); err != nil {
		return value, err
	}
	defer h.conn.endOperation()
	symbol, plan, err := h.binding()
	if err != nil {
		return value, err
	}
	data, err := h.conn.read(uint32(GroupSymbolValueByHandle), symbol.Handle, symbol.Length, true)
	if err != nil {
		return value, err
	}
	err = plan.decode(reflect.ValueOf(&value).Elem(), data)
	return value, err
}
func (h *Handle[T]) Write(value T) error {
	if err := h.conn.beginOperation(); err != nil {
		return err
	}
	defer h.conn.endOperation()
	symbol, plan, err := h.binding()
	if err != nil {
		return err
	}
	data := make([]byte, symbol.Length)
	if err := plan.encode(reflect.ValueOf(value), data); err != nil {
		return err
	}
	return h.conn.write(uint32(GroupSymbolValueByHandle), symbol.Handle, data, true)
}

// SubscribeOptions configures how the PLC sends notifications.
// All fields are optional; zero values apply the defaults noted below.
type SubscribeOptions struct {
	// Mode controls when the PLC sends updates.
	// Default: TransModeServerOnChange (send on value change).
	Mode TransMode

	// MaxDelay is the maximum time the PLC may buffer updates before sending.
	// Default: 100ms.
	MaxDelay time.Duration

	// CycleTime controls the server sampling interval, including on-change detection.
	// Default: 100ms.
	CycleTime time.Duration
}

func (o *SubscribeOptions) withDefaults() SubscribeOptions {
	out := *o
	if out.Mode == TransModeNoTransmission {
		out.Mode = TransModeServerOnChange
	}
	if out.MaxDelay == 0 {
		out.MaxDelay = 100 * time.Millisecond
	}
	if out.CycleTime == 0 {
		out.CycleTime = 100 * time.Millisecond
	}
	return out
}

// Subscription owns an ordered, bounded notification delivery worker.
type Subscription struct {
	conn      *Connection
	id        uint64
	adsHandle uint32
	spec      *subscriptionSpec
}

// Cancel stops local delivery before deleting the remote registration.
func (s *Subscription) Cancel() error { return s.conn.cancelSubscription(s.id) }

// Err reports the last restoration or decoding failure. A successful restore clears it.
func (s *Subscription) Err() error {
	s.conn.symbolLock.Lock()
	defer s.conn.symbolLock.Unlock()
	return s.spec.err
}

// Dropped counts updates discarded because the bounded delivery queue was full.
func (s *Subscription) Dropped() uint64 { return s.spec.dropped.Load() }
func (h *Handle[T]) Subscribe(updates chan<- Update[T], opts *SubscribeOptions) (*Subscription, error) {
	if updates == nil {
		return nil, fmt.Errorf("nil update channel")
	}
	if err := h.conn.beginOperation(); err != nil {
		return nil, err
	}
	defer h.conn.endOperation()
	symbol, _, err := h.binding()
	if err != nil {
		return nil, err
	}
	o := SubscribeOptions{}
	if opts != nil {
		o = *opts
	}
	o = o.withDefaults()
	factory := func(s *Symbol, types map[string]SymbolUploadDataType) (NotificationCallback, error) {
		plan, err := codecFor(reflect.TypeFor[T](), s, types)
		if err != nil {
			return nil, err
		}
		return notificationCallback(s, plan, updates), nil
	}
	return h.conn.subscribe(symbol, o, factory)
}
func (h *Handle[T]) SymbolName() string      { return h.symbolName }
func (h *Handle[T]) Connection() *Connection { return h.conn }
func (h *Handle[T]) ADSType() string         { h.mu.Lock(); defer h.mu.Unlock(); return h.dataType }
func (h *Handle[T]) HandleValue() uint32     { h.mu.Lock(); defer h.mu.Unlock(); return h.handle }
func (h *Handle[T]) Length() uint32          { h.mu.Lock(); defer h.mu.Unlock(); return h.length }
func (h *Handle[T]) SymbolInfo() *Symbol     { h.mu.Lock(); defer h.mu.Unlock(); return h.symbol }
func validateDurationType(adsType string, datatypes map[string]SymbolUploadDataType) error {
	dt := adsType
	if !Registry.IsKnownType(adsType) {
		if typeInfo, ok := datatypes[adsType]; ok && typeInfo.DataType != "" {
			dt = typeInfo.DataType
		}
	}
	if dt == "TIME" {
		return nil
	}
	return fmt.Errorf("type mismatch: Go type time.Duration does not match ADS type %s (expected TIME)", dt)
}

func validateTimeType(adsType string, datatypes map[string]SymbolUploadDataType) error {
	dt := adsType
	if !Registry.IsKnownType(adsType) {
		if typeInfo, ok := datatypes[adsType]; ok && typeInfo.DataType != "" {
			dt = typeInfo.DataType
		}
	}

	if dt == "TOD" || dt == "TIME_OF_DAY" || dt == "DATE" || dt == "DT" || dt == "DATE_AND_TIME" {
		return nil
	}

	return fmt.Errorf("type mismatch: Go type time.Time does not match ADS type %s", dt)
}

// validatePrimitiveType validates a primitive Go type against an ADS type
func validatePrimitiveType(goType reflect.Type, adsType string, datatypes map[string]SymbolUploadDataType) error {
	// Resolve ADS type through the datatype table only for custom/alias types.
	// If the type is already a known registry type (e.g. BOOL, INT, REAL), don't
	// resolve it — TwinCAT reports BOOL's underlying storage as BYTE in the
	// datatype table, which would cause bool to be wrongly rejected.
	dt := adsType
	if !Registry.IsKnownType(adsType) {
		if typeInfo, ok := datatypes[adsType]; ok && typeInfo.DataType != "" {
			dt = typeInfo.DataType
		}
	}

	// Get type info from registry by kind
	expectedInfo, ok := Registry.GetByKind(goType.Kind())
	if !ok {
		return fmt.Errorf("unsupported Go type: %s", goType.Kind())
	}

	// Check if ADS type matches expected (including aliases)
	if !Registry.IsAlias(dt, expectedInfo.ADSType) {
		// Get all valid types for error message
		validTypes := []string{expectedInfo.ADSType}
		validTypes = append(validTypes, expectedInfo.Aliases...)
		return fmt.Errorf("type mismatch: Go type %s does not match ADS type %s (expected one of: %v)", goType.Kind(), dt, validTypes)
	}

	return nil
}
