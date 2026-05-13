package ads

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"
)

func TestParseEnumMembersINT(t *testing.T) {
	buf := bytes.NewBuffer(nil)
	_ = binary.Write(buf, binary.LittleEndian, uint16(2))
	buf.WriteByte(3)
	buf.WriteString("Off")
	buf.WriteByte(0)
	_ = binary.Write(buf, binary.LittleEndian, int16(0))
	buf.WriteByte(2)
	buf.WriteString("On")
	buf.WriteByte(0)
	_ = binary.Write(buf, binary.LittleEndian, int16(1))

	members, err := parseEnumMembers(buf, "INT", 2)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(members))
	}
	if members[0].Name != "Off" || members[0].Value != 0 {
		t.Fatalf("unexpected first enum member: %+v", members[0])
	}
	if members[1].Name != "On" || members[1].Value != 1 {
		t.Fatalf("unexpected second enum member: %+v", members[1])
	}
}

func TestGetEnum(t *testing.T) {
	conn := &Connection{datatypes: map[string]SymbolUploadDataType{}, symbols: map[string]*Symbol{}}
	conn.datatypes["TESTE"] = SymbolUploadDataType{
		Name:        "TESTE",
		DataType:    "INT",
		EnumMembers: []EnumMember{{Name: "Zero", Value: 0}, {Name: "One", Value: 1}},
	}

	info, err := conn.GetEnum("TESTE")
	if err != nil {
		t.Fatalf("unexpected get enum error: %v", err)
	}
	if info.BaseType != "INT" {
		t.Fatalf("unexpected base type: %s", info.BaseType)
	}
	if got := info.Values["One"]; got != 1 {
		t.Fatalf("expected One=1, got %d", got)
	}
}

func TestParseEnumMembersFromDatatypeTailWithAttributes(t *testing.T) {
	buf := bytes.NewBuffer(nil)
	// Attribute section (count=1): name="strict", value="1"
	_ = binary.Write(buf, binary.LittleEndian, uint16(1))
	buf.WriteByte(byte(len("strict")))
	buf.WriteByte(byte(len("1")))
	buf.WriteString("strict")
	buf.WriteByte(0)
	buf.WriteString("1")
	buf.WriteByte(0)

	// Enum info section (count=2)
	_ = binary.Write(buf, binary.LittleEndian, uint16(2))
	buf.WriteByte(byte(len("enum_member")))
	buf.WriteString("enum_member")
	buf.WriteByte(0)
	_ = binary.Write(buf, binary.LittleEndian, int16(0))
	buf.WriteByte(byte(len("blarhg")))
	buf.WriteString("blarhg")
	buf.WriteByte(0)
	_ = binary.Write(buf, binary.LittleEndian, int16(1))

	members, err := parseEnumMembersFromDatatypeTail(buf, datatypeFlagAttributes|datatypeFlagEnumInfos, 2, "INT")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 enum members, got %d", len(members))
	}
	if members[0].Name != "enum_member" || members[0].Value != 0 {
		t.Fatalf("unexpected member[0]: %+v", members[0])
	}
	if members[1].Name != "blarhg" || members[1].Value != 1 {
		t.Fatalf("unexpected member[1]: %+v", members[1])
	}
}

func TestEnumBaseTypeValidationPath(t *testing.T) {
	type MyEnum int16
	conn := &Connection{
		symbols: map[string]*Symbol{
			"MAIN.enumValue": {FullName: "MAIN.enumValue", DataType: "TESTE", Length: 2, Handle: 10},
		},
		datatypes: map[string]SymbolUploadDataType{
			"TESTE": {
				Name:        "TESTE",
				DataType:    "INT",
				EnumMembers: []EnumMember{{Name: "Zero", Value: 0}},
			},
		},
		state: connectionStateConnected,
	}

	h, err := GetHandle[MyEnum](conn, "MAIN.enumValue")
	if err != nil {
		t.Fatalf("expected named enum type to validate, got error: %v", err)
	}
	if reflect.TypeFor[MyEnum]().Kind() != reflect.Int16 {
		t.Fatalf("test setup failure: MyEnum should be int16 kind")
	}
	if h == nil {
		t.Fatalf("expected handle")
	}
}
