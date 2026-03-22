package ads

import (
	"fmt"
	"reflect"
)

// BatchWriter provides reusable batch writing of struct fields to PLC.
// Handles are passed as pointers and map to struct fields in declaration order.
type BatchWriter[T any] struct {
	conn     *Connection
	handles  []handleInfo         // Cached handle metadata
	commands []sumWriteSubCommand // Pre-sized command buffer (reused)
	fields   []fieldInfo          // Cached struct field info
}

// NewBatchWriter creates a batch writer that maps struct fields to PLC handles.
// Handles are passed as pointers and must match struct field declaration order.
func NewBatchWriter[T any](conn *Connection, handles ...any) (*BatchWriter[T], error) {
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
	}

	// Count total leaf fields
	flatFieldCount := countLeafFields(structType)

	// Validate handle count matches field count
	if len(handles) != flatFieldCount {
		return nil, fmt.Errorf("handle count mismatch: got %d handles, struct has %d fields",
			len(handles), flatFieldCount)
	}

	// Pre-size command buffer (reused across writes)
	commands := make([]sumWriteSubCommand, len(handleInfos))

	// Cache struct field info for encoding
	fields := getAllFieldInfo(structType)

	return &BatchWriter[T]{
		conn:     conn,
		handles:  handleInfos,
		commands: commands,
		fields:   fields,
	}, nil
}

// Write encodes the source struct and writes all fields to PLC in one batch.
// All fields are written atomically in the same ADS transaction.
func (bw *BatchWriter[T]) Write(source T) error {
	sourceValue := reflect.ValueOf(source)

	// Encode each field into command buffer
	for i, info := range bw.handles {
		if err := ensureHandleInfoBound(&bw.handles[i]); err != nil {
			return fmt.Errorf("handle bind failed for %s: %w", bw.handles[i].symbolName, err)
		}
		info = bw.handles[i]
		field := bw.fields[i]
		fieldValue := sourceValue.FieldByIndex(field.index)

		// Allocate data buffer for this field
		data := make([]byte, info.length)
		tempSymbol := &Symbol{
			DataType: info.dataType,
			Length:   info.length,
		}

		// Encode field value
		if err := encodePrimitiveField(fieldValue, tempSymbol, data, bw.conn.datatypes); err != nil {
			return fmt.Errorf("failed to encode field %s (%s): %w", field.name, info.symbolName, err)
		}

		// Build command
		bw.commands[i] = sumWriteSubCommand{
			Group:  uint32(GroupSymbolValueByHandle),
			Offset: info.handle,
			Length: info.length,
			Data:   data,
		}
	}

	// Execute batch write
	sumResults, err := bw.conn.SumWrite(bw.commands)
	if err != nil {
		return fmt.Errorf("batch write failed: %w", err)
	}

	// Validate result count
	if len(sumResults) != len(bw.handles) {
		return fmt.Errorf("result count mismatch: got %d results for %d handles",
			len(sumResults), len(bw.handles))
	}

	// Check for errors
	errors := make([]BatchFieldError, 0)
	for i, info := range bw.handles {
		if sumResults[i].Error != ReturnCodeNoErrors {
			field := bw.fields[i]
			errors = append(errors, BatchFieldError{
				Field:   field.name,
				Symbol:  info.symbolName,
				ADSCode: sumResults[i].Error,
				Message: fmt.Sprintf("ADS error %d", sumResults[i].Error),
			})
		}
	}

	if len(errors) > 0 {
		return &BatchWriteError{
			FieldErrors: errors,
			TotalFields: len(bw.handles),
		}
	}

	return nil
}

// BatchWriteError represents errors from a batch write operation.
type BatchWriteError struct {
	FieldErrors []BatchFieldError
	TotalFields int
}

func (e *BatchWriteError) Error() string {
	return fmt.Sprintf("batch write failed for %d/%d fields", len(e.FieldErrors), e.TotalFields)
}

// GetFieldErrors returns all field-specific errors from the batch write.
func (e *BatchWriteError) GetFieldErrors() []BatchFieldError {
	return e.FieldErrors
}

// Note: handleInfo, extractHandleInfo, countLeafFields, getAllFieldInfo are defined in batch_reader.go
// They are reused here for consistency.
