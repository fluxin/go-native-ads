package ads

import (
	"fmt"
	"reflect"
	"sync"
)

// BatchWriter reuses its command and payload buffers. Concurrent calls are serialized.
type BatchWriter[T any] struct {
	batchPlan
	mu       sync.Mutex
	commands []sumWriteSubCommand
	data     []byte
}

func NewBatchWriter[T any](conn *Connection, handles ...any) (*BatchWriter[T], error) {
	if conn == nil {
		return nil, fmt.Errorf("nil connection")
	}
	if err := conn.beginOperation(); err != nil {
		return nil, err
	}
	defer conn.endOperation()
	infos, fields, err := buildBatch(conn, reflect.TypeFor[T](), handles)
	if err != nil {
		return nil, err
	}
	return &BatchWriter[T]{batchPlan: batchPlan{conn: conn, handles: infos, fields: fields}, commands: make([]sumWriteSubCommand, len(infos))}, nil
}

// Write validates and encodes all fields before sending. The PLC may accept only
// some subcommands; BatchWriteError reports failures without implying rollback.
func (bw *BatchWriter[T]) Write(source T) error {
	bw.mu.Lock()
	defer bw.mu.Unlock()
	if err := bw.conn.beginOperation(); err != nil {
		return err
	}
	defer bw.conn.endOperation()
	total := uint64(0)
	if err := bw.batchPlan.bind(); err != nil {
		return err
	}
	for i := range bw.handles {
		total += uint64(bw.handles[i].length)
	}
	if total+uint64(12*len(bw.handles))+48 > uint64(bw.conn.frameLimit()) {
		return fmt.Errorf("batch payload exceeds frame limit")
	}
	if cap(bw.data) < int(total) {
		bw.data = make([]byte, total)
	} else {
		bw.data = bw.data[:total]
		clear(bw.data)
	}
	value := reflect.ValueOf(source)
	offset := 0
	for i, h := range bw.handles {
		data := bw.data[offset : offset+int(h.length)]
		offset += int(h.length)
		if err := h.codec.encode(value.FieldByIndex(bw.fields[i].index), data); err != nil {
			return err
		}
		bw.commands[i] = sumWriteSubCommand{Group: uint32(GroupSymbolValueByHandle), Offset: h.handle, Length: h.length, Data: data}
	}
	results, err := bw.conn.sumWrite(bw.commands, true)
	if err != nil {
		return err
	}
	var failures []BatchFieldError
	for i, h := range bw.handles {
		if results[i].Error != ReturnCodeNoErrors {
			failures = append(failures, BatchFieldError{Field: bw.fields[i].name, Symbol: h.symbolName, ADSCode: results[i].Error})
		}
	}
	if len(failures) > 0 {
		return &BatchWriteError{failures, len(bw.handles)}
	}
	return nil
}

type BatchWriteError struct {
	FieldErrors []BatchFieldError
	TotalFields int
}

func (e *BatchWriteError) Error() string {
	return fmt.Sprintf("batch write failed for %d/%d fields", len(e.FieldErrors), e.TotalFields)
}
func (e *BatchWriteError) GetFieldErrors() []BatchFieldError { return e.FieldErrors }
