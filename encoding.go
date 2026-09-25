package ads

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"reflect"
	"time"
)

func encodeStructValue(v reflect.Value, s *Symbol, data []byte, types map[string]SymbolUploadDataType) error {
	return encodeValue(v, s, data, types)
}
func decodeStructValue(v reflect.Value, s *Symbol, data []byte, types map[string]SymbolUploadDataType) error {
	return decodeValue(v, s, data, types)
}
func encodeArrayField(v reflect.Value, s *Symbol, data []byte, types map[string]SymbolUploadDataType) error {
	return encodeValue(v, s, data, types)
}
func decodeArrayField(v reflect.Value, s *Symbol, data []byte, types map[string]SymbolUploadDataType) error {
	return decodeValue(v, s, data, types)
}
func encodePrimitiveField(v reflect.Value, s *Symbol, data []byte, types map[string]SymbolUploadDataType) error {
	return encodeValue(v, s, data, types)
}
func decodePrimitiveField(v reflect.Value, s *Symbol, data []byte, types map[string]SymbolUploadDataType) error {
	return decodeValue(v, s, data, types)
}
func encodeValue(v reflect.Value, s *Symbol, data []byte, types map[string]SymbolUploadDataType) error {
	if !v.IsValid() {
		return fmt.Errorf("invalid encode value")
	}
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if !v.IsValid() {
		return fmt.Errorf("nil encode value")
	}
	node, err := codecFor(v.Type(), s, types)
	if err != nil {
		return err
	}
	return node.encode(v, data)
}
func decodeValue(v reflect.Value, s *Symbol, data []byte, types map[string]SymbolUploadDataType) error {
	if !v.IsValid() {
		return fmt.Errorf("invalid decode value")
	}
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if !v.IsValid() {
		return fmt.Errorf("nil decode value")
	}
	node, err := codecFor(v.Type(), s, types)
	if err != nil {
		return err
	}
	return node.decode(v, data)
}
func fieldData(s *Symbol, data []byte) ([]byte, error) {
	start, end := uint64(s.Offset), uint64(s.Offset)+uint64(s.Length)
	if end > uint64(len(data)) {
		return nil, fmt.Errorf("field %s outside buffer", s.Name)
	}
	return data[start:end], nil
}
func encodeField(v reflect.Value, s *Symbol, data []byte, types map[string]SymbolUploadDataType) error {
	buf, err := fieldData(s, data)
	if err != nil {
		return err
	}
	return encodeValue(v, s, buf, types)
}
func decodeField(v reflect.Value, s *Symbol, data []byte, types map[string]SymbolUploadDataType) error {
	buf, err := fieldData(s, data)
	if err != nil {
		return err
	}
	return decodeValue(v, s, buf, types)
}
func encodePrimitive(v reflect.Value, buf []byte, dt string) error {
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
		if dt == "TIME" {
			// TIME is a duration in milliseconds (uint32).
			// time.Duration stores nanoseconds as int64.
			duration := time.Duration(v.Int())
			if duration < 0 || duration/time.Millisecond > time.Duration(math.MaxUint32) {
				return fmt.Errorf("TIME outside uint32 milliseconds")
			}
			ms := uint32(duration.Milliseconds())
			binary.LittleEndian.PutUint32(buf, ms)
		} else {
			binary.LittleEndian.PutUint64(buf, uint64(v.Int()))
		}
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
		if len(str) >= len(buf) {
			return fmt.Errorf("STRING needs %d bytes including terminator; capacity %d", len(str)+1, len(buf))
		}
		clear(buf)
		copy(buf, str)
		// Remaining bytes are already zero from make()
	case reflect.Struct:
		// Handle time.Time
		if v.Type().String() == "time.Time" {
			return encodeTimeValue(v, buf, dt)
		}
		return fmt.Errorf("type mismatch for '%s': Go type %s is not compatible with ADS type %s", "value", v.Type(), dt)
	default:
		return fmt.Errorf("type mismatch for '%s': Go type %s is not compatible with ADS type %s", "value", v.Type(), dt)
	}

	return nil
}

func encodeTimeValue(v reflect.Value, buf []byte, dt string) error {
	t := v.Interface().(time.Time)

	switch dt {
	case "TOD", "TIME_OF_DAY":
		// Same as TIME but usually just time portion
		ms := uint32((t.Hour()*3600+t.Minute()*60+t.Second())*1000 + t.Nanosecond()/1000000)
		binary.LittleEndian.PutUint32(buf, ms)
	case "DATE":
		// Seconds since Unix epoch
		if t.Unix() < 0 || t.Unix() > math.MaxUint32 {
			return fmt.Errorf("date outside uint32 seconds")
		}
		sec := uint32(t.Unix())
		binary.LittleEndian.PutUint32(buf, sec)
	case "DT", "DATE_AND_TIME":
		// Seconds since Unix epoch
		if t.Unix() < 0 || t.Unix() > math.MaxUint32 {
			return fmt.Errorf("date outside uint32 seconds")
		}
		sec := uint32(t.Unix())
		binary.LittleEndian.PutUint32(buf, sec)
	default:
		return fmt.Errorf("unsupported time type: %s", dt)
	}

	return nil
}

func decodePrimitive(v reflect.Value, buf []byte, dt string) error {
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
		if dt == "TIME" {
			// TIME is a duration in milliseconds (uint32).
			// Decode to time.Duration (nanoseconds as int64).
			ms := binary.LittleEndian.Uint32(buf)
			v.SetInt(int64(time.Duration(ms) * time.Millisecond))
		} else {
			v.SetInt(int64(binary.LittleEndian.Uint64(buf)))
		}
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
		return fmt.Errorf("type mismatch for '%s': Go type %s is not compatible with ADS type %s", "value", v.Type(), dt)
	default:
		return fmt.Errorf("type mismatch for '%s': Go type %s is not compatible with ADS type %s", "value", v.Type(), dt)
	}

	return nil
}

func decodeTimeValue(v reflect.Value, buf []byte, dt string) error {
	switch dt {
	case "TOD", "TIME_OF_DAY":
		ms := binary.LittleEndian.Uint32(buf)
		t := time.Unix(0, int64(ms)*int64(time.Millisecond)).UTC()
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
