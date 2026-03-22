package ads

import (
	"strings"
	"testing"
)

func TestGenerateTypeUsesEnumTypeAlias(t *testing.T) {
	conn := &Connection{
		state: connectionStateConnected,
		symbols: map[string]*Symbol{
			"MAIN.theetest": {
				FullName: "MAIN.theetest",
				Name:     "theetest",
				DataType: "TESTE",
				Handle:   1,
				Length:   2,
			},
		},
		datatypes: map[string]SymbolUploadDataType{
			"TESTE": {
				Name:        "TESTE",
				DataType:    "INT",
				EnumMembers: []EnumMember{{Name: "Zero", Value: 0}, {Name: "One", Value: 1}},
			},
		},
	}

	code, err := conn.GenerateType("MAIN.theetest")
	if err != nil {
		t.Fatalf("GenerateType failed: %v", err)
	}
	if !strings.Contains(code, "type MAINTheetest TESTE") {
		t.Fatalf("expected generated code to use enum type alias, got:\n%s", code)
	}
}

func TestGenerateStructBodyUsesEnumFieldType(t *testing.T) {
	conn := &Connection{
		state: connectionStateConnected,
		symbols: map[string]*Symbol{
			"MAIN.container": {
				FullName: "MAIN.container",
				Name:     "container",
				DataType: "CONTAINER",
				Handle:   2,
				Length:   2,
				Children: map[string]*Symbol{
					"mode": {
						FullName: "MAIN.container.mode",
						Name:     "mode",
						DataType: "TESTE",
						Length:   2,
						Offset:   0,
					},
				},
			},
		},
		datatypes: map[string]SymbolUploadDataType{
			"TESTE": {
				Name:        "TESTE",
				DataType:    "INT",
				EnumMembers: []EnumMember{{Name: "Zero", Value: 0}},
			},
		},
	}

	code, err := conn.GenerateType("MAIN.container")
	if err != nil {
		t.Fatalf("GenerateType failed: %v", err)
	}
	if !strings.Contains(code, "Mode TESTE") {
		t.Fatalf("expected struct field to use enum type, got:\n%s", code)
	}
}
