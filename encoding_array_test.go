package ads

import (
	"encoding/binary"
	"reflect"
	"testing"
)

func TestEncodeDecodeArrayFieldNonZeroLowerBound(t *testing.T) {
	arraySymbol := &Symbol{
		Name:     "arr",
		DataType: "INT",
		Length:   4,
		Children: map[string]*Symbol{
			"[5]": {Name: "[5]", DataType: "INT", Offset: 0, Length: 2},
			"[6]": {Name: "[6]", DataType: "INT", Offset: 2, Length: 2},
		},
	}

	in := [2]int16{100, 200}
	buf := make([]byte, 4)
	if err := encodeArrayField(reflect.ValueOf(in), arraySymbol, buf, nil); err != nil {
		t.Fatalf("encodeArrayField failed: %v", err)
	}

	var out [2]int16
	if err := decodeArrayField(reflect.ValueOf(&out).Elem(), arraySymbol, buf, nil); err != nil {
		t.Fatalf("decodeArrayField failed: %v", err)
	}

	if out != in {
		t.Fatalf("roundtrip mismatch: got %v want %v", out, in)
	}
}

func TestEncodeFieldArrayInStructUsesRelativeElementOffsets(t *testing.T) {
	arrayChild := &Symbol{
		Name:   "test_array_st",
		Offset: 8,
		Length: 6,
		Children: map[string]*Symbol{
			"[0]": {Name: "[0]", DataType: "INT", Offset: 0, Length: 2},
			"[1]": {Name: "[1]", DataType: "INT", Offset: 2, Length: 2},
			"[2]": {Name: "[2]", DataType: "INT", Offset: 4, Length: 2},
		},
	}

	buf := make([]byte, 16)
	in := [3]int16{101, 202, 303}
	if err := encodeField(reflect.ValueOf(in), arrayChild, buf, nil); err != nil {
		t.Fatalf("encodeField failed: %v", err)
	}

	if got := int16(binary.LittleEndian.Uint16(buf[8:10])); got != 101 {
		t.Fatalf("element[0] mismatch at offset 8: got %d", got)
	}
	if got := int16(binary.LittleEndian.Uint16(buf[10:12])); got != 202 {
		t.Fatalf("element[1] mismatch at offset 10: got %d", got)
	}
	if got := int16(binary.LittleEndian.Uint16(buf[12:14])); got != 303 {
		t.Fatalf("element[2] mismatch at offset 12: got %d", got)
	}
}
