package ads

import (
	"fmt"
	"strings"
)

// =============================================================================
// Code Generation Types
// =============================================================================

// GeneratedTypeInfo holds information about a generated Go type
type GeneratedTypeInfo struct {
	Name     string // Original symbol name (e.g., "MAIN.eeks")
	GoName   string // Go-friendly name (e.g., "MainEeks")
	Type     string // ADS type name
	GoType   string // Go type string
	IsStruct bool
	IsArray  bool
	Fields   []GeneratedFieldInfo
}

// GeneratedFieldInfo holds information about a generated struct field
type GeneratedFieldInfo struct {
	Name   string
	GoName string
	Type   string // ADS type
	GoType string
	Tag    string // ads tag value
	Offset uint32 // byte offset in struct
}

// =============================================================================
// Public API - Connection Methods
// =============================================================================

// GenerateType generates a complete Go type definition for an ADS symbol.
// Returns the generated Go code as a string.
//
// Example:
//
//	code, err := conn.GenerateType("MAIN.eeks")
//	// Returns:
//	// type MainEeks struct {
//	//     IntField ads.Int16 `ads:"int_field"`
//	//     RealField ads.Float32 `ads:"real_field"`
//	// }
func (conn *Connection) GenerateType(symbolName string) (string, error) {
	symbol, err := conn.GetSymbol(symbolName)
	if err != nil {
		return "", fmt.Errorf("failed to get symbol %s: %w", symbolName, err)
	}

	info, err := conn.analyzeSymbol(symbol)
	if err != nil {
		return "", fmt.Errorf("failed to analyze symbol %s: %w", symbolName, err)
	}

	return conn.generateTypeCode(info)
}

// GenerateStructBody generates just the struct body (fields) for an ADS symbol.
// This is useful for embedding or when you want to define your own type name.
//
// Example:
//
//	body, err := conn.GenerateStructBody("MAIN.eeks")
//	// Returns:
//	//     IntField ads.Int16 `ads:"int_field"`
//	//     RealField ads.Float32 `ads:"real_field"`
func (conn *Connection) GenerateStructBody(symbolName string) (string, error) {
	symbol, err := conn.GetSymbol(symbolName)
	if err != nil {
		return "", fmt.Errorf("failed to get symbol %s: %w", symbolName, err)
	}

	return conn.generateStructBody(symbol, ""), nil
}

// AnalyzeType returns detailed information about an ADS symbol's type structure
// without generating code. Useful for programmatic type inspection.
func (conn *Connection) AnalyzeType(symbolName string) (*GeneratedTypeInfo, error) {
	symbol, err := conn.GetSymbol(symbolName)
	if err != nil {
		return nil, fmt.Errorf("failed to get symbol %s: %w", symbolName, err)
	}

	return conn.analyzeSymbol(symbol)
}

// =============================================================================
// Internal Implementation
// =============================================================================

// analyzeSymbol analyzes an ADS symbol and returns type information
func (conn *Connection) analyzeSymbol(symbol *Symbol) (*GeneratedTypeInfo, error) {
	info := &GeneratedTypeInfo{
		Name:   symbol.FullName,
		GoName: goName(symbol.FullName),
		Type:   symbol.DataType,
	}

	// Resolve custom types
	baseType, err := resolveType(conn, symbol.DataType)
	if err != nil {
		baseType = symbol.DataType // Fallback to original type
	}

	if Registry.IsKnownType(baseType) {
		// Primitive or basic type
		if enumGoType := conn.enumGoType(symbol.DataType); enumGoType != "" {
			info.GoType = enumGoType
		} else {
			info.GoType = Registry.GetGoType(baseType)
		}
		info.IsStruct = false
		info.IsArray = false
	} else if isArraySymbol(symbol) {
		// Array type
		info.IsArray = true
		info.IsStruct = false

		// Analyze array element type
		if len(symbol.Children) > 0 {
			// Get first element to determine element type
			var firstChild *Symbol
			for _, child := range symbol.Children {
				firstChild = child
				break
			}
			if firstChild != nil {
				elemBaseType, _ := resolveType(conn, firstChild.DataType)
				if elemBaseType == "" {
					elemBaseType = firstChild.DataType // Fallback
				}
				elemGoType := Registry.GetGoType(elemBaseType)
				if enumGoType := conn.enumGoType(firstChild.DataType); enumGoType != "" {
					elemGoType = enumGoType
				}
				if !Registry.IsKnownType(elemBaseType) {
					// Array of structs - need to define element type
					elemGoType = goName(firstChild.DataType)
				}
				arraySize := len(symbol.Children)
				info.GoType = fmt.Sprintf("[%d]%s", arraySize, elemGoType)
			}
		}
	} else {
		// Struct type
		info.IsStruct = true
		info.GoType = info.GoName

		// Analyze struct fields
		fields := getFieldsByOffset(symbol)
		for _, field := range fields {
			fieldInfo := conn.analyzeField(field)
			info.Fields = append(info.Fields, fieldInfo)
		}
	}

	return info, nil
}

// analyzeField analyzes a single field and returns field information
func (conn *Connection) analyzeField(field *Symbol) GeneratedFieldInfo {
	baseType, _ := resolveType(conn, field.DataType)
	if baseType == "" {
		baseType = field.DataType // Fallback
	}
	goType := Registry.GetGoType(baseType)
	if enumGoType := conn.enumGoType(field.DataType); enumGoType != "" {
		goType = enumGoType
	}

	// Handle nested structs
	if !Registry.IsKnownType(baseType) && len(field.Children) > 0 && !isArraySymbol(field) {
		// Nested struct - use type name
		goType = goName(field.DataType)
	}

	// Handle arrays within structs
	if isArraySymbol(field) && len(field.Children) > 0 {
		var firstChild *Symbol
		for _, child := range field.Children {
			firstChild = child
			break
		}
		if firstChild != nil {
			elemBaseType, _ := resolveType(conn, firstChild.DataType)
			if elemBaseType == "" {
				elemBaseType = firstChild.DataType // Fallback
			}
			elemGoType := Registry.GetGoType(elemBaseType)
			if !Registry.IsKnownType(elemBaseType) {
				elemGoType = goName(firstChild.DataType)
			}
			arraySize := len(field.Children)
			goType = fmt.Sprintf("[%d]%s", arraySize, elemGoType)
		}
	}

	return GeneratedFieldInfo{
		Name:   field.Name,
		GoName: goName(field.Name),
		Type:   field.DataType,
		GoType: goType,
		Tag:    field.Name,
		Offset: field.Offset,
	}
}

// generateTypeCode generates the complete Go code for a type
func (conn *Connection) generateTypeCode(info *GeneratedTypeInfo) (string, error) {
	if info.IsStruct {
		return conn.generateStructCode(info)
	}

	// For non-struct types, generate a type alias
	if info.IsArray {
		return fmt.Sprintf("// %s represents TwinCAT array type %s\n"+
			"// Symbol: %s\n"+
			"type %s %s\n",
			info.GoName, info.Type, info.Name, info.GoName, info.GoType), nil
	}

	// Primitive type alias
	return fmt.Sprintf("// %s represents TwinCAT %s variable\n"+
		"// Symbol: %s\n"+
		"type %s %s\n",
		info.GoName, info.Type, info.Name, info.GoName, info.GoType), nil
}

// generateStructCode generates Go code for a struct type
func (conn *Connection) generateStructCode(info *GeneratedTypeInfo) (string, error) {
	var builder strings.Builder

	fmt.Fprintf(&builder, "// %s represents TwinCAT type %s\n", info.GoName, info.Type)
	fmt.Fprintf(&builder, "// Symbol: %s\n", info.Name)
	fmt.Fprintf(&builder, "type %s struct {\n", info.GoName)

	for _, field := range info.Fields {
		fmt.Fprintf(&builder, "\t%s %s `ads:\"%s\"`\n",
			field.GoName, field.GoType, field.Tag)
	}

	builder.WriteString("}\n")

	return builder.String(), nil
}

// generateStructBody generates just the struct body (fields)
func (conn *Connection) generateStructBody(symbol *Symbol, indent string) string {
	var builder strings.Builder

	fields := getFieldsByOffset(symbol)
	for _, field := range fields {
		goType := conn.generateGoTypeString(field, indent+"    ")
		fmt.Fprintf(&builder, "%s    %s %s `ads:\"%s\"`\n",
			indent, goName(field.Name), goType, field.Name)
	}

	return builder.String()
}

// generateGoTypeString converts an ADS symbol to a Go type string
func (conn *Connection) generateGoTypeString(symbol *Symbol, indent string) string {
	baseType, _ := resolveType(conn, symbol.DataType)
	if baseType == "" {
		baseType = symbol.DataType // Fallback
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
				elemType := conn.generateGoTypeString(firstChild, indent)
				return fmt.Sprintf("[%d]%s", size, elemType)
			}
		}
		return "[]interface{}"
	}

	// Handle nested structs
	if !Registry.IsKnownType(baseType) && len(symbol.Children) > 0 {
		nested := conn.generateStructBody(symbol, indent)
		return fmt.Sprintf("struct {\n%s    }", nested)
	}

	// Primitive types
	if enumGoType := conn.enumGoType(symbol.DataType); enumGoType != "" {
		return enumGoType
	}
	return Registry.GetGoType(baseType)
}

func (conn *Connection) enumGoType(dataType string) string {
	if _, err := conn.GetEnum(dataType); err == nil {
		return goName(dataType)
	}
	return ""
}

// =============================================================================
// Helper Functions
// =============================================================================

// goName converts TwinCAT naming to Go naming
func goName(name string) string {
	parts := strings.Split(name, ".")
	var builder strings.Builder
	for _, part := range parts {
		builder.WriteString(capitalize(part))
	}
	return builder.String()
}

// capitalize capitalizes the first letter of a string
func capitalize(s string) string {
	if len(s) == 0 {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// isArraySymbol checks if a symbol represents an array
