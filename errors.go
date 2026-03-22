package ads

import (
	"fmt"
	"reflect"
	"strings"
)

// generateStructExample creates a complete Go struct definition from ADS symbol
func generateStructExample(symbol *Symbol, datatypes map[string]SymbolUploadDataType, indent string) string {
	var builder strings.Builder

	// Get fields sorted by offset
	fields := getFieldsByOffset(symbol)

	for _, field := range fields {
		goType := adsTypeToGoTypeString(field, datatypes, indent+"    ")
		fmt.Fprintf(&builder, "%s    %s %s `ads:\"%s\"`\n",
			indent, field.Name, goType, field.Name)
	}

	return builder.String()
}

// adsTypeToGoTypeString converts ADS type to Go type string with arrays/structs
func adsTypeToGoTypeString(symbol *Symbol, datatypes map[string]SymbolUploadDataType, indent string) string {
	// Resolve type aliases
	adsType := symbol.DataType
	if dtInfo, ok := datatypes[adsType]; ok && dtInfo.DataType != "" {
		adsType = dtInfo.DataType
	}

	// Handle arrays
	if isArraySymbol(symbol) {
		size := len(symbol.Children)
		if size > 0 {
			var firstChild *Symbol
			for _, child := range symbol.Children {
				firstChild = child
				break
			}
			if firstChild != nil {
				elemType := adsTypeToGoTypeString(firstChild, datatypes, indent)
				return fmt.Sprintf("[%d]%s", size, elemType)
			}
		}
		return "[]interface{}"
	}

	// Handle nested structs
	if len(symbol.Children) > 0 {
		nested := generateStructExample(symbol, datatypes, indent)
		return fmt.Sprintf("struct {\n%s    }", nested)
	}

	// Primitive types - use Registry for consistency
	return Registry.GetGoType(adsType)
}

// getYourStructFields extracts all field names from a Go struct value
func getYourStructFields(v reflect.Value) []string {
	t := v.Type()
	fields := make([]string, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		fields[i] = t.Field(i).Name
	}
	return fields
}

// generateFieldMismatchError creates a detailed error for missing struct fields
func generateFieldMismatchError(symbol *Symbol, childName string, v reflect.Value, datatypes map[string]SymbolUploadDataType) error {
	expectedStruct := generateStructExample(symbol, datatypes, "")
	yourFields := getYourStructFields(v)

	return fmt.Errorf("struct field mismatch for '%s':\n"+
		"  Missing Go field for ADS field: %s\n"+
		"  Your struct fields: %v\n\n"+
		"  Expected struct definition:\n"+
		"type %s struct {\n%s}",
		symbol.FullName,
		childName,
		yourFields,
		symbol.Name,
		expectedStruct)
}

// generateArrayMismatchError creates a detailed error for array size mismatches
func generateArrayMismatchError(arrayName string, child *Symbol, actualLen, expectedLen int, datatypes map[string]SymbolUploadDataType) error {
	var elemType string
	for _, elemChild := range child.Children {
		elemType = adsTypeToGoTypeString(elemChild, datatypes, "")
		break
	}
	if elemType == "" {
		elemType = "interface{}"
	}

	return fmt.Errorf("array size mismatch for '%s': Go [%d]%s vs ADS [%d]%s",
		arrayName, actualLen, elemType, expectedLen, elemType)
}
