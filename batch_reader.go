package ads

import (
	"fmt"
	"reflect"
	"sync"
)

type handleInfo struct {
	conn       *Connection
	handle     uint32
	length     uint32
	dataType   string
	symbolName string
	bindEpoch  uint64
	symbol     *Symbol
	typ        reflect.Type
	codec      *codecNode
}
type fieldInfo struct {
	name  string
	index []int
	typ   reflect.Type
}
type HandleInfoGetter interface {
	HandleValue() uint32
	Length() uint32
	ADSType() string
	SymbolName() string
	Connection() *Connection
}

func extractHandleInfo(h any) (handleInfo, error) {
	getter, ok := h.(HandleInfoGetter)
	if !ok || (reflect.ValueOf(h).Kind() == reflect.Pointer && reflect.ValueOf(h).IsNil()) {
		return handleInfo{}, fmt.Errorf("expected nonnil typed handle")
	}
	if getter.Connection() == nil {
		return handleInfo{}, fmt.Errorf("handle has no connection")
	}
	// Copy identity only. A numeric handle is meaningful only with its actual binding epoch.
	return handleInfo{conn: getter.Connection(), symbolName: getter.SymbolName()}, nil
}
func ensureHandleInfoBound(info *handleInfo) error {
	epoch := info.conn.CurrentEpoch()
	if info.bindEpoch == epoch && info.handle != 0 && (info.typ == nil || info.codec != nil) {
		return nil
	}
	symbol, err := info.conn.lookupSymbol(info.symbolName, true)
	if err != nil {
		return err
	}
	var plan *codecNode
	if info.typ != nil {
		plan, err = codecFor(info.typ, symbol, info.conn.datatypeSnapshot())
		if err != nil {
			return err
		}
	}
	info.handle = symbol.Handle
	info.length = symbol.Length
	info.dataType = symbol.DataType
	info.bindEpoch = epoch
	info.symbol = symbol
	info.codec = plan
	return nil
}
func getAllFieldInfo(t reflect.Type) []fieldInfo {
	var fields []fieldInfo
	collectFieldInfo(t, nil, &fields)
	return fields
}
func countLeafFields(t reflect.Type) int { return len(getAllFieldInfo(t)) }
func collectFieldInfo(t reflect.Type, prefix []int, fields *[]fieldInfo) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		path := append(append([]int(nil), prefix...), i)
		if f.Type.Kind() == reflect.Struct && !isTimeType(f.Type) {
			collectFieldInfo(f.Type, path, fields)
		} else {
			*fields = append(*fields, fieldInfo{f.Name, path, f.Type})
		}
	}
}
func buildBatch(conn *Connection, t reflect.Type, handles []any) ([]handleInfo, []fieldInfo, error) {
	if t.Kind() != reflect.Struct || isTimeType(t) {
		return nil, nil, fmt.Errorf("batch type must be a struct")
	}
	fields := getAllFieldInfo(t)
	if len(handles) == 0 || len(handles) > 500 || len(handles) != len(fields) {
		return nil, nil, fmt.Errorf("batch requires 1..500 handles matching %d fields", len(fields))
	}
	infos := make([]handleInfo, len(handles))
	for i, h := range handles {
		info, err := extractHandleInfo(h)
		if err != nil {
			return nil, nil, err
		}
		if info.conn != conn {
			return nil, nil, fmt.Errorf("handle %d belongs to another connection", i)
		}
		f := t
		for _, index := range fields[i].index {
			sf := f.Field(index)
			if sf.PkgPath != "" {
				return nil, nil, fmt.Errorf("unexported batch field %s", sf.Name)
			}
			f = sf.Type
		}
		info.typ = fields[i].typ
		if err := ensureHandleInfoBound(&info); err != nil {
			return nil, nil, fmt.Errorf("field %s: %w", fields[i].name, err)
		}
		infos[i] = info
	}
	return infos, fields, nil
}

// BatchReader reads fields in one ADS sum request. Concurrent calls are serialized.
type BatchReader[T any] struct {
	conn     *Connection
	mu       sync.Mutex
	handles  []handleInfo
	commands []sumReadSubCommand
	fields   []fieldInfo
}

func NewBatchReader[T any](conn *Connection, handles ...any) (*BatchReader[T], error) {
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
	return &BatchReader[T]{conn: conn, handles: infos, fields: fields, commands: make([]sumReadSubCommand, len(infos))}, nil
}

// Read replaces target only if every field succeeds; it does not promise a PLC scan snapshot.
func (br *BatchReader[T]) Read(target *T) error {
	if target == nil {
		return fmt.Errorf("nil batch target")
	}
	br.mu.Lock()
	defer br.mu.Unlock()
	if err := br.conn.beginOperation(); err != nil {
		return err
	}
	defer br.conn.endOperation()
	for i := range br.handles {
		if err := ensureHandleInfoBound(&br.handles[i]); err != nil {
			return err
		}
		h := br.handles[i]
		br.commands[i] = sumReadSubCommand{Group: uint32(GroupSymbolValueByHandle), Offset: h.handle, Length: h.length}
	}
	results, err := br.conn.sumRead(br.commands, true)
	if err != nil {
		return err
	}
	var out T
	value := reflect.ValueOf(&out).Elem()
	var failures []BatchFieldError
	for i, h := range br.handles {
		field := br.fields[i]
		if results[i].Error != ReturnCodeNoErrors {
			failures = append(failures, BatchFieldError{Field: field.name, Symbol: h.symbolName, ADSCode: results[i].Error})
			continue
		}
		if err := h.codec.decode(value.FieldByIndex(field.index), results[i].Data); err != nil {
			failures = append(failures, BatchFieldError{Field: field.name, Symbol: h.symbolName, Message: err.Error()})
		}
	}
	if len(failures) > 0 {
		return &BatchReadError{failures, len(br.handles)}
	}
	*target = out
	return nil
}

type BatchFieldError struct {
	Field   string
	Symbol  string
	ADSCode ReturnCode
	Message string
}
type BatchReadError struct {
	FieldErrors []BatchFieldError
	TotalFields int
}

func (e *BatchReadError) Error() string {
	return fmt.Sprintf("batch read failed for %d/%d fields", len(e.FieldErrors), e.TotalFields)
}
func (e *BatchReadError) GetFieldErrors() []BatchFieldError { return e.FieldErrors }
