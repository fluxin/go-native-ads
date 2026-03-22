package ads

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"reflect"
	"time"
)

// encodeStructValue encodes a Go struct value into binary ADS format
func encodeStructValue(val reflect.Value, symbol *Symbol, data []byte, datatypes map[string]SymbolUploadDataType) error {
	if val.Kind() == reflect.Pointer {
		val = val.Elem()
	}

	if val.Kind() != reflect.Struct {
		return fmt.Errorf("expected struct, got %s", val.Kind())
	}

	structType := val.Type()

	// Build map of Go field names to their indices
	goFields := make(map[string]int)
	for i := 0; i < structType.NumField(); i++ {
		field := structType.Field(i)
		adsName := field.Name

		// Check for ads struct tag
		if tag := field.Tag.Get("ads"); tag != "" {
			adsName = tag
		}

		goFields[adsName] = i
	}

	// Encode each ADS child field
	for childName, child := range symbol.Children {
		goFieldIdx, ok := goFields[childName]
		if !ok {
			return generateFieldMismatchError(symbol, childName, val, datatypes)
		}

		fieldValue := val.Field(goFieldIdx)
		if err := encodeField(fieldValue, child, data, datatypes); err != nil {
			return fmt.Errorf("failed to encode field %s: %w", childName, err)
		}
	}

	return nil
}

// encodeField encodes a single field value into binary format at the correct offset
func encodeField(fieldVal reflect.Value, child *Symbol, data []byte, datatypes map[string]SymbolUploadDataType) error {
	offset := int(child.Offset)

	// Handle arrays
	if len(child.Children) > 0 && isArraySymbol(child) {
		return encodeArrayField(fieldVal, child, data[offset:], datatypes)
	}

	// Handle nested structs
	if len(child.Children) > 0 {
		return encodeStructValue(fieldVal, child, data[offset:], datatypes)
	}

	// Handle primitive types
	return encodePrimitiveField(fieldVal, child, data[offset:offset+int(child.Length)], datatypes)
}

// encodeArrayField encodes an array field
func encodeArrayField(v reflect.Value, child *Symbol, data []byte, datatypes map[string]SymbolUploadDataType) error {
	if v.Kind() != reflect.Array && v.Kind() != reflect.Slice {
		return fmt.Errorf("expected array or slice, got %s", v.Kind())
	}
	elements := getFieldsByOffset(child)
	if len(elements) == 0 {
		return fmt.Errorf("array %s has no element metadata", child.Name)
	}

	// Get array element info
	var elemSize uint32
	for _, elemChild := range elements {
		elemSize = elemChild.Length
		break // All elements have same type/size
	}

	if v.Len() != len(elements) {
		return generateArrayMismatchError(child.Name, child, v.Len(), len(elements), datatypes)
	}

	// Encode each element
	for i := 0; i < v.Len(); i++ {
		elemValue := v.Index(i)
		elemChild := elements[i]

		offset := int(elemChild.Offset)
		if err := encodePrimitiveField(elemValue, elemChild, data[offset:offset+int(elemSize)], datatypes); err != nil {
			return fmt.Errorf("failed to encode array element %d: %w", i, err)
		}
	}

	return nil
}

// encodePrimitiveField encodes a primitive value directly to binary
func encodePrimitiveField(v reflect.Value, child *Symbol, buf []byte, datatypes map[string]SymbolUploadDataType) error {
	// Resolve type (only if resolved type is not empty)
	dt := child.DataType
	if typeInfo, ok := datatypes[dt]; ok && typeInfo.DataType != "" {
		dt = typeInfo.DataType
	}

	switch v.Kind() {
	case reflect.Bool:
		if v.Bool() {
			buf[0] = 1
		} else {
			buf[0] = 0
		}
	case reflect.Int8:
		buf[0] = byte(int8(v.Int()))
	case reflect.Int16:
		binary.LittleEndian.PutUint16(buf, uint16(v.Int()))
	case reflect.Int32:
		binary.LittleEndian.PutUint32(buf, uint32(v.Int()))
	case reflect.Int64:
		binary.LittleEndian.PutUint64(buf, uint64(v.Int()))
	case reflect.Uint8:
		buf[0] = uint8(v.Uint())
	case reflect.Uint16:
		binary.LittleEndian.PutUint16(buf, uint16(v.Uint()))
	case reflect.Uint32:
		binary.LittleEndian.PutUint32(buf, uint32(v.Uint()))
	case reflect.Uint64:
		binary.LittleEndian.PutUint64(buf, v.Uint())
	case reflect.Float32:
		bits := math.Float32bits(float32(v.Float()))
		binary.LittleEndian.PutUint32(buf, bits)
	case reflect.Float64:
		bits := math.Float64bits(v.Float())
		binary.LittleEndian.PutUint64(buf, bits)
	case reflect.String:
		str := v.String()
		copy(buf, str)
		// Remaining bytes are already zero from make()
	case reflect.Struct:
		// Handle time.Time
		if v.Type().String() == "time.Time" {
			return encodeTimeValue(v, buf, dt)
		}
		return fmt.Errorf("unsupported struct type for encoding: %s", v.Type())
	default:
		return fmt.Errorf("unsupported kind for encoding: %s", v.Kind())
	}

	return nil
}

// encodeTimeValue encodes a time.Time value to binary
func encodeTimeValue(v reflect.Value, buf []byte, dt string) error {
	t := v.Interface().(time.Time)

	switch dt {
	case "TIME":
		// Milliseconds since midnight minus 1 hour (TwinCAT epoch)
		midnight := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
		ms := uint32(t.Sub(midnight).Milliseconds())
		binary.LittleEndian.PutUint32(buf, ms)
	case "TOD", "TIME_OF_DAY":
		// Same as TIME but usually just time portion
		midnight := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
		ms := uint32(t.Sub(midnight).Milliseconds())
		binary.LittleEndian.PutUint32(buf, ms)
	case "DATE":
		// Seconds since Unix epoch
		sec := uint32(t.Unix())
		binary.LittleEndian.PutUint32(buf, sec)
	case "DT", "DATE_AND_TIME":
		// Seconds since Unix epoch
		sec := uint32(t.Unix())
		binary.LittleEndian.PutUint32(buf, sec)
	default:
		return fmt.Errorf("unsupported time type: %s", dt)
	}

	return nil
}

// decodeStructValue decodes binary ADS data into a Go struct value
func decodeStructValue(v reflect.Value, symbol *Symbol, data []byte, datatypes map[string]SymbolUploadDataType) error {
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		return fmt.Errorf("expected struct, got %s", v.Kind())
	}

	t := v.Type()

	// Build map of Go field names to their indices
	goFields := make(map[string]int)
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		adsName := field.Name

		// Check for ads struct tag
		if tag := field.Tag.Get("ads"); tag != "" {
			adsName = tag
		}

		goFields[adsName] = i
	}

	// Decode each ADS child field
	for childName, child := range symbol.Children {
		goFieldIdx, ok := goFields[childName]
		if !ok {
			return generateFieldMismatchError(symbol, childName, v, datatypes)
		}

		fieldValue := v.Field(goFieldIdx)
		if err := decodeField(fieldValue, child, data, datatypes); err != nil {
			return fmt.Errorf("failed to decode field %s: %w", childName, err)
		}
	}

	return nil
}

// decodeField decodes a single field from binary data at the correct offset
func decodeField(v reflect.Value, child *Symbol, data []byte, datatypes map[string]SymbolUploadDataType) error {
	offset := int(child.Offset)

	// Handle arrays
	if len(child.Children) > 0 && isArraySymbol(child) {
		return decodeArrayField(v, child, data[offset:], datatypes)
	}

	// Handle nested structs
	if len(child.Children) > 0 {
		return decodeStructValue(v, child, data[offset:], datatypes)
	}

	// Handle primitive types
	return decodePrimitiveField(v, child, data[offset:offset+int(child.Length)], datatypes)
}

// decodeArrayField decodes an array field from binary data
func decodeArrayField(v reflect.Value, child *Symbol, data []byte, datatypes map[string]SymbolUploadDataType) error {
	if v.Kind() != reflect.Array && v.Kind() != reflect.Slice {
		return fmt.Errorf("expected array or slice, got %s", v.Kind())
	}
	elements := getFieldsByOffset(child)
	if len(elements) == 0 {
		return fmt.Errorf("array %s has no element metadata", child.Name)
	}

	// Get array element info
	var elemSize uint32
	for _, elemChild := range elements {
		elemSize = elemChild.Length
		break
	}

	if v.Len() != len(elements) {
		return generateArrayMismatchError(child.Name, child, v.Len(), len(elements), datatypes)
	}

	// Decode each element
	for i := 0; i < v.Len(); i++ {
		elemValue := v.Index(i)
		elemChild := elements[i]

		offset := int(elemChild.Offset)
		if err := decodePrimitiveField(elemValue, elemChild, data[offset:offset+int(elemSize)], datatypes); err != nil {
			return fmt.Errorf("failed to decode array element %d: %w", i, err)
		}
	}

	return nil
}

// decodePrimitiveField decodes a primitive value directly from binary
func decodePrimitiveField(v reflect.Value, child *Symbol, buf []byte, datatypes map[string]SymbolUploadDataType) error {
	// Resolve type (only if resolved type is not empty)
	dt := child.DataType
	if typeInfo, ok := datatypes[dt]; ok && typeInfo.DataType != "" {
		dt = typeInfo.DataType
	}

	switch v.Kind() {
	case reflect.Bool:
		v.SetBool(buf[0] != 0)
	case reflect.Int8:
		v.SetInt(int64(int8(buf[0])))
	case reflect.Int16:
		v.SetInt(int64(int16(binary.LittleEndian.Uint16(buf))))
	case reflect.Int32:
		v.SetInt(int64(int32(binary.LittleEndian.Uint32(buf))))
	case reflect.Int64:
		v.SetInt(int64(binary.LittleEndian.Uint64(buf)))
	case reflect.Uint8:
		v.SetUint(uint64(buf[0]))
	case reflect.Uint16:
		v.SetUint(uint64(binary.LittleEndian.Uint16(buf)))
	case reflect.Uint32:
		v.SetUint(uint64(binary.LittleEndian.Uint32(buf)))
	case reflect.Uint64:
		v.SetUint(binary.LittleEndian.Uint64(buf))
	case reflect.Float32:
		bits := binary.LittleEndian.Uint32(buf)
		v.SetFloat(float64(math.Float32frombits(bits)))
	case reflect.Float64:
		bits := binary.LittleEndian.Uint64(buf)
		v.SetFloat(math.Float64frombits(bits))
	case reflect.String:
		// Find null terminator
		idx := bytes.IndexByte(buf, 0)
		if idx < 0 {
			idx = len(buf)
		}
		v.SetString(string(buf[:idx]))
	case reflect.Struct:
		// Handle time.Time
		if v.Type().String() == "time.Time" {
			return decodeTimeValue(v, buf, dt)
		}
		return fmt.Errorf("unsupported struct type for decoding: %s", v.Type())
	default:
		return fmt.Errorf("unsupported kind for decoding: %s", v.Kind())
	}

	return nil
}

// decodeTimeValue decodes a binary time value to time.Time
func decodeTimeValue(v reflect.Value, buf []byte, dt string) error {
	switch dt {
	case "TIME":
		ms := binary.LittleEndian.Uint32(buf)
		// TwinCAT TIME is milliseconds since midnight minus 1 hour
		t := time.Unix(0, int64(ms)*int64(time.Millisecond)-int64(time.Hour))
		v.Set(reflect.ValueOf(t))
	case "TOD", "TIME_OF_DAY":
		ms := binary.LittleEndian.Uint32(buf)
		t := time.Unix(0, int64(ms)*int64(time.Millisecond)-int64(time.Hour))
		v.Set(reflect.ValueOf(t))
	case "DATE":
		sec := binary.LittleEndian.Uint32(buf)
		t := time.Unix(int64(sec), 0).UTC()
		v.Set(reflect.ValueOf(t))
	case "DT", "DATE_AND_TIME":
		sec := binary.LittleEndian.Uint32(buf)
		t := time.Unix(int64(sec), 0).UTC()
		v.Set(reflect.ValueOf(t))
	default:
		return fmt.Errorf("unsupported time type: %s", dt)
	}
	return nil
}
