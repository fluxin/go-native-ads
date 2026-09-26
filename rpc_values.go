package ads

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// rpcParameterValue lowers a top-level reference to its transmitted value.
// Pointer storage width is never used as a pointee's wire width. LengthIs is
// one-based in the complete parameter list, and counts elements, not bytes.
func rpcParameterValue(m RPCMethod, index int, types map[string]SymbolUploadDataType) (string, uint32, string, error) {
	p := m.Parameters[index]
	lengthIndex := int(p.LengthIsParameterIndex) - 1
	attributeSeen := false
	for _, attr := range p.Attributes {
		if !strings.EqualFold(attr.Name, "TcRpcLengthIs") {
			continue
		}
		if attributeSeen {
			return "", 0, "", fmt.Errorf("duplicate TcRpcLengthIs")
		}
		attributeSeen = true
		found := -1
		for i, candidate := range m.Parameters {
			if strings.EqualFold(candidate.Name, strings.TrimSpace(attr.Value)) {
				if found >= 0 {
					return "", 0, "", fmt.Errorf("ambiguous length parameter")
				}
				found = i
			}
		}
		if found < 0 || (lengthIndex >= 0 && found != lengthIndex) {
			return "", 0, "", fmt.Errorf("TcRpcLengthIs name/index mismatch")
		}
		lengthIndex = found
	}
	length := ""
	if lengthIndex >= 0 {
		if lengthIndex >= len(m.Parameters) || lengthIndex == index {
			return "", 0, "", fmt.Errorf("invalid length parameter index")
		}
		count := m.Parameters[lengthIndex]
		if count.Flags & ^RPCParameterAttributes != RPCIn || count.LengthIsParameterIndex != 0 || hasLengthAttribute(count.Attributes) {
			return "", 0, "", fmt.Errorf("length must reference an input-only integer value")
		}
		base, err := resolveDataType(count.DataType, types)
		if err != nil {
			return "", 0, "", err
		}
		switch base {
		case "SINT", "USINT", "INT", "UINT", "DINT", "UDINT", "LINT", "ULINT":
		default:
			return "", 0, "", fmt.Errorf("length parameter %s is not an integer count", count.Name)
		}
		length = count.Name
	}
	name := p.DataType
	seen := make(map[string]bool)
	for range 64 {
		if seen[name] {
			return "", 0, "", fmt.Errorf("cyclic reference datatype %s", name)
		}
		seen[name] = true
		def, hasDefinition := types[name]
		if hasDefinition && hasPackingAttribute(def.Attributes) {
			return "", 0, "", fmt.Errorf("custom reference packing is not supported")
		}
		target, pointer := rpcReferenceTarget(name)
		if target == "" && hasDefinition && def.DatatypeEntry.Flags&datatypeFlagReferenceTo != 0 {
			target = def.DataType
			if target == "" || target == name {
				return "", 0, "", fmt.Errorf("unresolved reference datatype %s", name)
			}
		}
		if target != "" {
			if pointer && length == "" {
				return "", 0, "", fmt.Errorf("pointer buffer requires TcRpcLengthIs")
			}
			if _, nested := rpcReferenceTarget(target); nested || strings.HasPrefix(strings.ToUpper(target), "REFERENCE TO ") {
				return "", 0, "", fmt.Errorf("nested pointers/references are not supported")
			}
			if strings.EqualFold(target, "VOID") {
				if length == "" {
					return "", 0, "", fmt.Errorf("unbounded void pointer")
				}
				target = "BYTE"
			}
			size, err := rpcValueSize(target, types)
			return target, size, length, err
		}
		if !hasDefinition || def.DataType == "" || def.DataType == name || len(def.Children) > 0 {
			break
		}
		if hasPackingAttribute(def.Attributes) {
			return "", 0, "", fmt.Errorf("custom reference packing is not supported")
		}
		name = def.DataType
	}
	// Some uploads already describe the referenced value and only carry the
	// ByReference flag. Keep the ordinary datatype/width validation for these.
	if length != "" && p.Flags&RPCByReference == 0 {
		return "", 0, "", fmt.Errorf("length-linked value must be a pointer or reference")
	}
	return p.DataType, p.Size, length, nil
}

func rpcReferenceTarget(name string) (string, bool) {
	name = strings.TrimSpace(name)
	upper := strings.ToUpper(name)
	for _, prefix := range []string{"REFERENCE TO ", "POINTER TO "} {
		if strings.HasPrefix(upper, prefix) {
			return strings.TrimSpace(name[len(prefix):]), prefix == "POINTER TO "
		}
	}
	if upper == "PVOID" {
		return "VOID", true
	}
	return "", false
}

func rpcValueSize(name string, types map[string]SymbolUploadDataType) (uint32, error) {
	seen := make(map[string]bool)
	var aliasSize uint32
	for range 64 {
		if seen[name] {
			break
		}
		seen[name] = true
		if target, _ := rpcReferenceTarget(name); target != "" {
			return 0, fmt.Errorf("nested pointers/references are not supported")
		}
		if info, ok := Registry.GetByADSType(name); ok {
			if info.Size > 0 {
				return uint32(info.Size), nil
			}
			if aliasSize > 0 {
				return aliasSize, nil
			}
		}
		if strings.HasPrefix(name, "STRING(") && strings.HasSuffix(name, ")") {
			n, err := strconv.ParseUint(name[7:len(name)-1], 10, 32)
			if err == nil && n < 1<<32-1 {
				return uint32(n + 1), nil
			}
		}
		def, ok := types[name]
		if !ok || def.DatatypeEntry.Size == 0 {
			break
		}
		if def.DatatypeEntry.Flags&datatypeFlagReferenceTo != 0 {
			return 0, fmt.Errorf("nested pointers/references are not supported")
		}
		if len(def.Children) > 0 || def.DataType == "" || def.DataType == name {
			return def.DatatypeEntry.Size, nil
		}
		aliasSize = def.DatatypeEntry.Size
		name = def.DataType
	}
	return 0, fmt.Errorf("cannot determine referenced value size for %s", name)
}

// Only the RPC record owns dynamic parameter boundaries. Element codecs remain
// shared with symbol IO, so structs, arrays, strings and enums have one encoding.
type rpcRecordCodec struct{ fields []rpcFieldCodec }
type rpcFieldCodec struct {
	name       string
	index      int
	countIndex int // input struct index, -1 for a fixed value
	codec      *codecNode
}

func compileRPCRecord(t, inputType reflect.Type, schema *Symbol, types map[string]SymbolUploadDataType) (*rpcRecordCodec, error) {
	fields, err := codecStructFields(t)
	if err != nil {
		return nil, err
	}
	inputs, err := codecStructFields(inputType)
	if err != nil {
		return nil, err
	}
	if len(fields) != len(schema.Children) {
		return nil, fmt.Errorf("RPC record fields do not match %s", schema.FullName)
	}
	record := &rpcRecordCodec{}
	for _, s := range getFieldsByOffset(schema) {
		i, ok := fields[s.Name]
		if !ok {
			return nil, fmt.Errorf("missing RPC field %s", s.Name)
		}
		field := rpcFieldCodec{name: s.Name, index: i, countIndex: -1}
		typ := t.Field(i).Type
		if s.rpcLength != "" {
			if typ.Kind() != reflect.Slice {
				return nil, fmt.Errorf("RPC buffer %s must be a slice", s.Name)
			}
			typ = typ.Elem()
			field.countIndex, ok = inputs[s.rpcLength]
			if !ok {
				return nil, fmt.Errorf("missing RPC count %s", s.rpcLength)
			}
		}
		field.codec, err = codecFor(typ, s, types)
		if err != nil {
			return nil, err
		}
		record.fields = append(record.fields, field)
	}
	return record, nil
}

// sizes snapshots lengths before encoding/sending, rejects overflow before
// allocation, and validates every input slice without modifying caller memory.
func (r *rpcRecordCodec) sizes(input reflect.Value, checkInput bool, limit uint32) ([]int, int, error) {
	sizes := make([]int, len(r.fields))
	total := uint64(0)
	overhead := uint64(40)
	if checkInput {
		overhead = 48
	}
	for i, field := range r.fields {
		count := uint64(1)
		if field.countIndex >= 0 {
			v := input.Field(field.countIndex)
			switch v.Kind() {
			case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				if v.Int() < 0 {
					return nil, 0, fmt.Errorf("negative RPC count for %s", field.name)
				}
				count = uint64(v.Int())
			case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				count = v.Uint()
			default:
				return nil, 0, fmt.Errorf("invalid RPC count type for %s", field.name)
			}
			if checkInput && uint64(input.Field(field.index).Len()) != count {
				return nil, 0, fmt.Errorf("RPC buffer %s length must match count %d", field.name, count)
			}
		}
		if total+overhead > uint64(limit) || count > (uint64(limit)-total-overhead)/uint64(field.codec.size) {
			return nil, 0, fmt.Errorf("RPC payload exceeds frame limit")
		}
		size := count * uint64(field.codec.size)
		total += size
		if total > uint64(^uint(0)>>1) {
			return nil, 0, fmt.Errorf("RPC payload exceeds Go allocation limit")
		}
		sizes[i] = int(size)
	}
	return sizes, int(total), nil
}

func (r *rpcRecordCodec) encode(value reflect.Value, sizes []int, data []byte) error {
	offset := 0
	for i, field := range r.fields {
		v := value.Field(field.index)
		b := data[offset : offset+sizes[i]]
		if field.countIndex < 0 {
			if err := field.codec.encode(v, b); err != nil {
				return err
			}
		} else {
			for j := 0; j < v.Len(); j++ {
				if err := field.codec.encode(v.Index(j), b[j*field.codec.size:(j+1)*field.codec.size]); err != nil {
					return err
				}
			}
		}
		offset += sizes[i]
	}
	return nil
}

func (r *rpcRecordCodec) decode(value reflect.Value, sizes []int, data []byte) error {
	expected := 0
	for _, size := range sizes {
		expected += size
	}
	if len(data) != expected {
		return fmt.Errorf("RPC response size: expected %d, got %d", expected, len(data))
	}
	offset := 0
	for i, field := range r.fields {
		v := value.Field(field.index)
		b := data[offset : offset+sizes[i]]
		if field.countIndex < 0 {
			if err := field.codec.decode(v, b); err != nil {
				return err
			}
		} else {
			n := sizes[i] / field.codec.size
			v.Set(reflect.MakeSlice(v.Type(), n, n))
			for j := 0; j < n; j++ {
				if err := field.codec.decode(v.Index(j), b[j*field.codec.size:(j+1)*field.codec.size]); err != nil {
					return err
				}
			}
		}
		offset += sizes[i]
	}
	return nil
}
