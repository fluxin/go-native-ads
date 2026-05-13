package ads

import (
	"fmt"
	"log/slog"
	"reflect"
)

// handleInfo holds the metadata needed for batch operations.
// This is a non-generic internal type used by batch readers/writers.
type handleInfo struct {
	conn       *Connection
	handle     uint32
	length     uint32
	dataType   string
	symbolName string
	bindEpoch  uint64
}

// BatchReader provides reusable batch reading of struct fields from PLC.
// Handles are passed as pointers and map to struct fields in declaration order.
type BatchReader[T any] struct {
	conn     *Connection
	handles  []handleInfo        // Cached handle metadata
	commands []sumReadSubCommand // Pre-built ADS commands
	fields   []fieldInfo         // Cached struct field info
}

// fieldInfo holds information about a struct field for batch operations.
type fieldInfo struct {
	name  string
	index []int // Full index path for nested fields
}

// NewBatchReader creates a batch reader that maps struct fields to PLC handles.
// Handles are passed as pointers and must match struct field declaration order.
func NewBatchReader[T any](conn *Connection, handles ...any) (*BatchReader[T], error) {
	var zero T
	structType := reflect.TypeOf(zero)

	// Validate T is a struct
	if structType.Kind() != reflect.Struct {
		return nil, fmt.Errorf("type parameter must be a struct, got %s", structType.Kind())
	}

	// Extract handle info from typed handles
	handleInfos := make([]handleInfo, len(handles))
	for i, h := range handles {
		info, err := extractHandleInfo(h)
		if err != nil {
			return nil, fmt.Errorf("handle %d: %w", i, err)
		}
		handleInfos[i] = info
		slog.Debug("batch reader mapped handle", "index", i, "symbol", info.symbolName, "group", GroupSymbolValueByHandle, "offset", info.handle, "length", info.length)
	}

	// Count total leaf fields
	flatFieldCount := countLeafFields(structType)

	// Validate handle count matches field count
	if len(handles) != flatFieldCount {
		return nil, fmt.Errorf("handle count mismatch: got %d handles, struct has %d fields",
			len(handles), flatFieldCount)
	}

	// Build pre-constructed commands
	commands := make([]sumReadSubCommand, len(handleInfos))
	for i, info := range handleInfos {
		commands[i] = sumReadSubCommand{
			Group:  uint32(GroupSymbolValueByHandle),
			Offset: info.handle,
			Length: info.length,
		}
	}

	// Cache struct field info for decoding
	fields := getAllFieldInfo(structType)

	return &BatchReader[T]{
		conn:     conn,
		handles:  handleInfos,
		commands: commands,
		fields:   fields,
	}, nil
}

// HandleInfoGetter interface for extracting handle metadata.
// This is implemented by all Handle[T] types.
type HandleInfoGetter interface {
	HandleValue() uint32
	Length() uint32
	ADSType() string
	SymbolName() string
	Connection() *Connection
}

// extractHandleInfo extracts metadata from a typed Handle using type assertion.
// Uses the HandleInfoGetter interface to avoid reflection on unexported fields.
func extractHandleInfo(h any) (handleInfo, error) {
	// Use type assertion to get the HandleInfoGetter interface
	getter, ok := h.(HandleInfoGetter)
	if !ok {
		return handleInfo{}, fmt.Errorf("handle must be a *Handle[T] type, got %T", h)
	}

	return handleInfo{
		conn:       getter.Connection(),
		handle:     getter.HandleValue(),
		length:     getter.Length(),
		dataType:   getter.ADSType(),
		symbolName: getter.SymbolName(),
		bindEpoch:  getter.Connection().CurrentEpoch(),
	}, nil
}

func ensureHandleInfoBound(info *handleInfo) error {
	epoch := info.conn.CurrentEpoch()
	if info.bindEpoch == epoch && info.handle != 0 {
		return nil
	}
	symbol, err := info.conn.GetSymbol(info.symbolName)
	if err != nil {
		return err
	}
	info.handle = symbol.Handle
	info.length = symbol.Length
	info.dataType = symbol.DataType
	info.bindEpoch = epoch
	return nil
}

// Read executes the batch read and populates the target struct.
// All fields are updated atomically from the same PLC scan cycle.
func (br *BatchReader[T]) Read(target *T) error {
	targetValue := reflect.ValueOf(target).Elem()

	// Execute batch read using pre-built commands
	for i := range br.handles {
		if err := ensureHandleInfoBound(&br.handles[i]); err != nil {
			return fmt.Errorf("handle bind failed for %s: %w", br.handles[i].symbolName, err)
		}
		br.commands[i] = sumReadSubCommand{
			Group:  uint32(GroupSymbolValueByHandle),
			Offset: br.handles[i].handle,
			Length: br.handles[i].length,
		}
	}

	sumResults, err := br.conn.SumRead(br.commands)
	if err != nil {
		return fmt.Errorf("batch read failed: %w", err)
	}

	// Validate result count
	if len(sumResults) != len(br.handles) {
		return fmt.Errorf("result count mismatch: got %d results for %d handles",
			len(sumResults), len(br.handles))
	}

	// Decode each field
	errors := make([]BatchFieldError, 0)

	for i, info := range br.handles {
		field := br.fields[i]
		fieldValue := targetValue.FieldByIndex(field.index)

		// Check for ADS error
		if sumResults[i].Error != ReturnCodeNoErrors {
			errors = append(errors, BatchFieldError{
				Field:   field.name,
				Symbol:  info.symbolName,
				ADSCode: sumResults[i].Error,
				Message: fmt.Sprintf("ADS error %d", sumResults[i].Error),
			})
			continue
		}

		// Decode value into field
		tempSymbol := &Symbol{
			DataType: info.dataType,
			Length:   info.length,
		}

		if err := decodePrimitiveField(fieldValue, tempSymbol, sumResults[i].Data, br.conn.datatypes); err != nil {
			errors = append(errors, BatchFieldError{
				Field:   field.name,
				Symbol:  info.symbolName,
				Message: fmt.Sprintf("decode failed: %v", err),
			})
		}
	}

	if len(errors) > 0 {
		return &BatchReadError{
			FieldErrors: errors,
			TotalFields: len(br.handles),
		}
	}

	return nil
}

// countLeafFields returns the total number of leaf fields in a struct type.
func countLeafFields(t reflect.Type) int {
	if t.Kind() != reflect.Struct {
		return 1
	}

	count := 0
	for field := range t.Fields() {
		field := field
		if field.Type.Kind() == reflect.Struct && field.Type.NumField() > 0 {
			count += countLeafFields(field.Type)
		} else {
			count++
		}
	}
	return count
}

// getAllFieldInfo returns field information for all leaf fields.
func getAllFieldInfo(t reflect.Type) []fieldInfo {
	fields := make([]fieldInfo, 0)
	collectFieldInfo(t, []int{}, &fields)
	return fields
}

func collectFieldInfo(t reflect.Type, prefix []int, fields *[]fieldInfo) {
	if t.Kind() != reflect.Struct {
		return
	}

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		index := append(append([]int{}, prefix...), i)

		if field.Type.Kind() == reflect.Struct && field.Type.NumField() > 0 {
			collectFieldInfo(field.Type, index, fields)
		} else {
			*fields = append(*fields, fieldInfo{
				name:  field.Name,
				index: index,
			})
		}
	}
}

// BatchFieldError represents an error for a specific field in a batch operation.
type BatchFieldError struct {
	Field   string
	Symbol  string
	ADSCode ReturnCode
	Message string
}

// BatchReadError represents errors from a batch read operation.
type BatchReadError struct {
	FieldErrors []BatchFieldError
	TotalFields int
}

func (e *BatchReadError) Error() string {
	return fmt.Sprintf("batch read failed for %d/%d fields", len(e.FieldErrors), e.TotalFields)
}

// GetFieldErrors returns all field-specific errors from the batch read.
func (e *BatchReadError) GetFieldErrors() []BatchFieldError {
	return e.FieldErrors
}
