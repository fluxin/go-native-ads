package ads

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
)

type symbolCache struct {
	ordered []*Symbol
	codecs  sync.Map
}

var symbolCacheLock sync.Mutex

func cacheFor(symbol *Symbol) *symbolCache {
	symbolCacheLock.Lock()
	defer symbolCacheLock.Unlock()
	if symbol.cache == nil {
		fields := make([]*Symbol, 0, len(symbol.Children))
		for _, child := range symbol.Children {
			fields = append(fields, child)
		}
		sort.Slice(fields, func(i, j int) bool {
			if fields[i].Offset == fields[j].Offset {
				return fields[i].Name < fields[j].Name
			}
			return fields[i].Offset < fields[j].Offset
		})
		symbol.cache = &symbolCache{ordered: fields}
	}
	return symbol.cache
}

type codecNode struct {
	record   bool
	typ      reflect.Type
	dt       string
	offset   int
	size     int
	index    int
	children []*codecNode
}

func codecFor(t reflect.Type, s *Symbol, types map[string]SymbolUploadDataType) (*codecNode, error) {
	if s == nil || t == nil {
		return nil, fmt.Errorf("missing codec type or symbol")
	}
	cache := cacheFor(s)
	if cached, ok := cache.codecs.Load(t); ok {
		return cached.(*codecNode), nil
	}
	node, err := compileCodec(t, s, types, 0)
	if err != nil {
		return nil, err
	}
	actual, _ := cache.codecs.LoadOrStore(t, node)
	return actual.(*codecNode), nil
}
func compileCodec(t reflect.Type, s *Symbol, types map[string]SymbolUploadDataType, depth int) (*codecNode, error) {
	if depth > 64 {
		return nil, fmt.Errorf("type nesting exceeds limit")
	}
	node := &codecNode{typ: t, size: int(s.Length)}
	mismatch := func() (*codecNode, error) {
		return nil, fmt.Errorf("type mismatch for %s: Go %s vs ADS %s (%d bytes)", s.FullName, t, s.DataType, s.Length)
	}
	if len(s.Children) > 0 || s.DataType == rpcRecordType {
		node.record = true
		if isArraySymbol(s) {
			if t.Kind() != reflect.Array && t.Kind() != reflect.Slice {
				return mismatch()
			}
			fields := getFieldsByOffset(s)
			if t.Kind() == reflect.Array && t.Len() != len(fields) {
				return mismatch()
			}
			for i, child := range fields {
				n, err := compileCodec(t.Elem(), child, types, depth+1)
				if err != nil {
					return nil, err
				}
				n.index = i
				n.offset = int(child.Offset)
				node.children = append(node.children, n)
			}
		} else {
			if t.Kind() != reflect.Struct || isTimeType(t) {
				return mismatch()
			}
			fields, err := codecStructFields(t)
			if err != nil {
				return nil, err
			}
			if len(fields) != len(s.Children) {
				return mismatch()
			}
			for _, child := range getFieldsByOffset(s) {
				i, ok := fields[child.Name]
				if !ok {
					return mismatch()
				}
				n, err := compileCodec(t.Field(i).Type, child, types, depth+1)
				if err != nil {
					return nil, err
				}
				n.index = i
				n.offset = int(child.Offset)
				node.children = append(node.children, n)
			}
		}
		for _, child := range node.children {
			if child.offset < 0 || child.size < 0 || child.offset > node.size || child.size > node.size-child.offset {
				return nil, fmt.Errorf("field outside symbol %s", s.FullName)
			}
		}
		return node, nil
	}
	dt, err := resolveDataType(s.DataType, types)
	if err != nil {
		return nil, err
	}
	node.dt = dt
	if isDurationType(t) {
		err = validateDurationType(dt, types)
	} else if isTimeType(t) {
		err = validateTimeType(dt, types)
	} else {
		err = validatePrimitiveType(t, dt, types)
	}
	if err != nil {
		return mismatch()
	}
	info, ok := Registry.GetByADSType(dt)
	if !ok || (info.Size != 0 && info.Size != node.size) || node.size <= 0 {
		return mismatch()
	}
	return node, nil
}
func resolveDataType(name string, types map[string]SymbolUploadDataType) (string, error) {
	seen := make(map[string]bool)
	for len(seen) < 64 {
		if strings.HasPrefix(name, "STRING(") {
			name = "STRING"
		}
		if Registry.IsKnownType(name) {
			return name, nil
		}
		if seen[name] {
			return "", fmt.Errorf("cyclic ADS type alias %s", name)
		}
		seen[name] = true
		dt, ok := types[name]
		if !ok || dt.DataType == "" {
			return "", fmt.Errorf("unknown ADS type %s", name)
		}
		name = dt.DataType
	}
	return "", fmt.Errorf("ADS alias depth exceeds limit")
}
func (node *codecNode) encode(v reflect.Value, data []byte) error {
	if len(data) < node.size {
		return fmt.Errorf("short encode buffer: need %d got %d", node.size, len(data))
	}
	if !node.record {
		return encodePrimitive(v, data[:node.size], node.dt)
	}
	if v.Kind() == reflect.Slice && v.Len() != len(node.children) {
		return fmt.Errorf("slice length mismatch")
	}
	for _, child := range node.children {
		var value reflect.Value
		if v.Kind() == reflect.Struct {
			value = v.Field(child.index)
		} else {
			value = v.Index(child.index)
		}
		if err := child.encode(value, data[child.offset:child.offset+child.size]); err != nil {
			return err
		}
	}
	return nil
}
func (node *codecNode) decode(v reflect.Value, data []byte) error {
	if len(data) < node.size {
		return fmt.Errorf("short decode buffer: need %d got %d", node.size, len(data))
	}
	if !v.CanSet() {
		return fmt.Errorf("decode destination is not settable")
	}
	if !node.record {
		return decodePrimitive(v, data[:node.size], node.dt)
	}
	if v.Kind() == reflect.Slice {
		if v.Len() != len(node.children) {
			v.Set(reflect.MakeSlice(v.Type(), len(node.children), len(node.children)))
		}
	}
	for _, child := range node.children {
		var value reflect.Value
		if v.Kind() == reflect.Struct {
			value = v.Field(child.index)
		} else {
			value = v.Index(child.index)
		}
		if err := child.decode(value, data[child.offset:child.offset+child.size]); err != nil {
			return err
		}
	}
	return nil
}
func isTimeType(t reflect.Type) bool     { return t == reflect.TypeFor[time.Time]() }
func isDurationType(t reflect.Type) bool { return t == reflect.TypeFor[time.Duration]() }
func isPrimitiveType(t reflect.Type) bool {
	return t != nil && t.Kind() != reflect.Struct && t.Kind() != reflect.Array && t.Kind() != reflect.Slice
}

func codecStructFields(t reflect.Type) (map[string]int, error) {
	if t.Kind() != reflect.Struct || isTimeType(t) {
		return nil, fmt.Errorf("expected record struct, got %s", t)
	}
	fields := make(map[string]int, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		name := field.Tag.Get("ads")
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		if field.PkgPath != "" {
			return nil, fmt.Errorf("unexported field %s", field.Name)
		}
		if _, exists := fields[name]; exists {
			return nil, fmt.Errorf("duplicate ADS field %s", name)
		}
		fields[name] = i
	}
	return fields, nil
}
