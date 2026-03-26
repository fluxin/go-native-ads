package ads

import (
	"fmt"
	"log/slog"
	"reflect"
	"time"
)

// Handle represents a typed reference to a PLC variable.
// The type parameter T is bound at handle creation and validated against
// the PLC variable's type. All subsequent operations use this type.
// Supports primitives, structs, and arrays.
type Handle[T any] struct {
	conn       *Connection
	handle     uint32  // ADS handle value (used as offset in commands)
	length     uint32  // Size in bytes
	dataType   string  // ADS type name (e.g., "INT", "REAL", or struct/array info)
	symbolName string  // Original symbol name (for error messages)
	symbol     *Symbol // Full symbol info for struct/array operations
	bindEpoch  uint64
}

// GetHandle acquires a typed handle to a PLC variable.
// This performs a one-time string lookup and type validation.
// The returned handle can be reused for multiple operations without
// additional string lookups or type checking.
// Supports primitives, structs, and arrays.
func GetHandle[T any](conn *Connection, symbolName string) (*Handle[T], error) {
	// Look up symbol in cache
	symbol, err := conn.GetSymbol(symbolName)
	if err != nil {
		return nil, fmt.Errorf("failed to get symbol %s: %w", symbolName, err)
	}

	// Validate Go type T matches ADS type for primitives
	var zero T
	goType := reflect.TypeOf(zero)

	// Validate scalar/time/duration handles against PLC primitive ADS types.
	// Structs and arrays are validated by shape during encode/decode.
	if isDurationType(goType) {
		if err := validateDurationType(symbol.DataType, conn.datatypes); err != nil {
			return nil, fmt.Errorf("type mismatch for %s: %w", symbolName, err)
		}
	} else if isPrimitiveType(goType) {
		adsTypeForValidation := symbol.DataType
		if enumInfo, err := conn.GetEnum(symbol.DataType); err == nil {
			adsTypeForValidation = enumInfo.BaseType
		}
		if err := validatePrimitiveType(goType, adsTypeForValidation, conn.datatypes); err != nil {
			return nil, fmt.Errorf("type mismatch for %s: %w", symbolName, err)
		}
	} else if isTimeType(goType) {
		if err := validateTimeType(symbol.DataType, conn.datatypes); err != nil {
			return nil, fmt.Errorf("type mismatch for %s: %w", symbolName, err)
		}
	}

	return &Handle[T]{
		conn:       conn,
		handle:     symbol.Handle,
		length:     symbol.Length,
		dataType:   symbol.DataType,
		symbolName: symbolName,
		symbol:     symbol,
		bindEpoch:  conn.CurrentEpoch(),
	}, nil
}

func (h *Handle[T]) ensureBound() error {
	current := h.conn.CurrentEpoch()
	if h.bindEpoch == current && h.handle != 0 {
		return nil
	}
	symbol, err := h.conn.GetSymbol(h.symbolName)
	if err != nil {
		return err
	}
	h.handle = symbol.Handle
	h.length = symbol.Length
	h.dataType = symbol.DataType
	h.symbol = symbol
	h.bindEpoch = current
	return nil
}

// isPrimitiveType checks if a Go type is a primitive (not struct or array)
func isPrimitiveType(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Struct, reflect.Array, reflect.Slice:
		return false
	default:
		return true
	}
}

// isTimeType checks if a Go type is time.Time
func isTimeType(t reflect.Type) bool {
	return t.String() == "time.Time"
}

// isDurationType checks if a Go type is time.Duration
func isDurationType(t reflect.Type) bool {
	return t.String() == "time.Duration"
}

// Read fetches the current value from the PLC.
// Uses direct ADS Read command (same as original API).
// Supports primitives, structs, and arrays.
func (h *Handle[T]) Read() (T, error) {
	var result T
	if err := h.ensureBound(); err != nil {
		return result, fmt.Errorf("read bind failed for %s: %w", h.symbolName, err)
	}

	// Execute direct read (same as original ReadSymbol implementation)
	data, err := h.conn.Read(uint32(GroupSymbolValueByHandle), h.handle, h.length)
	if err != nil {
		return result, fmt.Errorf("read failed for %s: %w", h.symbolName, err)
	}

	// Decode result based on type
	resultValue := reflect.ValueOf(&result).Elem()
	goType := resultValue.Type()

	if isPrimitiveType(goType) || isTimeType(goType) {
		// Resolve custom types (enums) to base types before decoding
		baseType, err := resolveType(h.conn, h.dataType)
		if err != nil && !isTimeType(goType) {
			// For non-time types, require successful type resolution
			return result, fmt.Errorf("failed to resolve type for %s: %w", h.symbolName, err)
		}
		if err != nil {
			// For time types, fall back to original type
			baseType = h.dataType
		}

		// Decode primitive type
		tempSymbol := &Symbol{
			DataType: baseType,
			Length:   h.length,
		}
		if err := decodePrimitiveField(resultValue, tempSymbol, data, h.conn.datatypes); err != nil {
			return result, fmt.Errorf("failed to decode %s: %w", h.symbolName, err)
		}
	} else if goType.Kind() == reflect.Struct {
		// Decode struct
		if err := decodeStructValue(resultValue, h.symbol, data, h.conn.datatypes); err != nil {
			return result, fmt.Errorf("failed to decode struct %s: %w", h.symbolName, err)
		}
	} else if goType.Kind() == reflect.Array {
		// Decode array
		if err := decodeArrayField(resultValue, h.symbol, data, h.conn.datatypes); err != nil {
			return result, fmt.Errorf("failed to decode array %s: %w", h.symbolName, err)
		}
	} else {
		return result, fmt.Errorf("unsupported type %s for %s", goType.Kind(), h.symbolName)
	}

	return result, nil
}

// Write sends a new value to the PLC.
// Uses direct ADS Write command (same as original API).
// Supports primitives, structs, and arrays.
func (h *Handle[T]) Write(value T) error {
	if err := h.ensureBound(); err != nil {
		return fmt.Errorf("write bind failed for %s: %w", h.symbolName, err)
	}

	// Encode value based on type
	val := reflect.ValueOf(value)
	goType := val.Type()
	data := make([]byte, h.length)

	if isPrimitiveType(goType) || isTimeType(goType) {
		// Resolve custom types (enums) to base types before encoding
		baseType, err := resolveType(h.conn, h.dataType)
		if err != nil {
			baseType = h.dataType // Fall back to original type if resolution fails
		}

		// Encode primitive type
		tempSymbol := &Symbol{
			DataType: baseType,
			Length:   h.length,
		}
		if err := encodePrimitiveField(val, tempSymbol, data, h.conn.datatypes); err != nil {
			return fmt.Errorf("failed to encode %s: %w", h.symbolName, err)
		}
	} else if goType.Kind() == reflect.Struct {
		// Encode struct
		if err := encodeStructValue(val, h.symbol, data, h.conn.datatypes); err != nil {
			return fmt.Errorf("failed to encode struct %s: %w", h.symbolName, err)
		}
	} else if goType.Kind() == reflect.Array {
		// Encode array
		if err := encodeArrayField(val, h.symbol, data, h.conn.datatypes); err != nil {
			return fmt.Errorf("failed to encode array %s: %w", h.symbolName, err)
		}
	} else {
		return fmt.Errorf("unsupported type %s for %s", goType.Kind(), h.symbolName)
	}

	// Execute direct write (same as original WriteSymbol implementation)
	err := h.conn.Write(uint32(GroupSymbolValueByHandle), h.handle, data)
	if err != nil {
		return fmt.Errorf("write failed for %s: %w", h.symbolName, err)
	}

	return nil
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

	// CycleTime is the PLC scan interval for cyclic modes (TransModeServerCycle /
	// TransModeClientCycle). Ignored for on-change modes.
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

// Subscription represents an active PLC notification.
// Call Cancel to deregister it.
type Subscription struct {
	conn      *Connection
	id        uint64
	adsHandle uint32 // notification handle returned by AddDeviceNotification
}

// Cancel deregisters the notification from the PLC and stops updates.
func (s *Subscription) Cancel() error {
	return s.conn.cancelSubscription(s.id)
}

// Subscribe registers a notification for this handle's variable.
// Updates are decoded and sent to the provided channel.
// Pass nil opts to use defaults (on-change, 100ms max delay, 100ms cycle).
func (h *Handle[T]) Subscribe(updates chan<- Update[T], opts *SubscribeOptions) (*Subscription, error) {
	if err := h.ensureBound(); err != nil {
		return nil, fmt.Errorf("subscribe bind failed for %s: %w", h.symbolName, err)
	}

	o := SubscribeOptions{}
	if opts != nil {
		o = *opts
	}
	o = o.withDefaults()

	adsHandle, err := h.conn.AddDeviceNotification(
		uint32(GroupSymbolValueByHandle),
		h.handle,
		h.length,
		o.Mode,
		o.MaxDelay,
		o.CycleTime,
	)
	if err != nil {
		return nil, fmt.Errorf("subscribe failed for %s: %w", h.symbolName, err)
	}

	callback := createNotificationCallback[T](h.symbol, h.conn.datatypes, updates)

	h.conn.symbolLock.Lock()
	h.conn.nextSubID++
	subID := h.conn.nextSubID
	h.conn.activeNotifications[adsHandle] = callback
	h.conn.notificationToSubID[adsHandle] = subID
	h.conn.subscriptions[subID] = &subscriptionSpec{
		id:         subID,
		symbolName: h.symbolName,
		group:      uint32(GroupSymbolValueByHandle),
		offset:     h.handle,
		length:     h.length,
		mode:       o.Mode,
		maxDelay:   o.MaxDelay,
		cycleTime:  o.CycleTime,
		callback:   callback,
		adsHandle:  adsHandle,
	}
	pending := h.conn.pendingNotifications[adsHandle]
	delete(h.conn.pendingNotifications, adsHandle)
	h.conn.symbolLock.Unlock()

	for _, item := range pending {
		if err := callback(h.conn.ctx, item.timestamp, item.content); err != nil {
			slog.Error("failed to deliver queued notification", "symbol", h.symbolName, "handle", adsHandle, "error", err)
		}
	}

	slog.Debug("subscription created", "symbol", h.symbolName, "adsHandle", adsHandle, "mode", o.Mode)
	return &Subscription{conn: h.conn, id: subID, adsHandle: adsHandle}, nil
}

// The following methods implement HandleInfoGetter, used by NewBatchReader/NewBatchWriter
// to extract metadata from typed handles without reflection on unexported fields.

func (h *Handle[T]) SymbolName() string      { return h.symbolName }
func (h *Handle[T]) ADSType() string         { return h.dataType }
func (h *Handle[T]) HandleValue() uint32     { return h.handle }
func (h *Handle[T]) Length() uint32          { return h.length }
func (h *Handle[T]) Connection() *Connection { return h.conn }
func (h *Handle[T]) SymbolInfo() *Symbol     { return h.symbol }

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
