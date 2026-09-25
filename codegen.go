package ads

import (
	"fmt"
	"go/token"
	"strconv"
	"strings"
	"unicode"
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
	conn.generationLock.RLock()
	defer conn.generationLock.RUnlock()
	symbol, err := conn.symbolMetadata(symbolName)
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
	conn.generationLock.RLock()
	defer conn.generationLock.RUnlock()
	symbol, err := conn.symbolMetadata(symbolName)
	if err != nil {
		return "", fmt.Errorf("failed to get symbol %s: %w", symbolName, err)
	}

	info, err := conn.analyzeSymbol(symbol)
	if err != nil {
		return "", err
	}
	if !info.IsStruct {
		return "", fmt.Errorf("symbol is not a struct")
	}
	return conn.generateStructBody(symbol, ""), nil
}

// GenerateTypeFrom generates a Go type definition from an existing Symbol,
// using the specified Go name instead of deriving it from the symbol path.
// This is useful for generating dependent struct types referenced by arrays
// or other structs.
func (conn *Connection) GenerateTypeFrom(symbol *Symbol, goTypeName string) (string, error) {
	conn.generationLock.RLock()
	defer conn.generationLock.RUnlock()
	info, err := conn.analyzeSymbol(symbol)
	if err != nil {
		return "", fmt.Errorf("failed to analyze symbol: %w", err)
	}
	if !token.IsIdentifier(goTypeName) || token.Lookup(goTypeName).IsKeyword() {
		return "", fmt.Errorf("invalid Go type name %q", goTypeName)
	}
	info.GoName = goTypeName
	if info.IsStruct {
		info.GoType = goTypeName
	}
	return conn.generateTypeCode(info)
}

// AnalyzeType returns detailed information about an ADS symbol's type structure
// without generating code. Useful for programmatic type inspection.
func (conn *Connection) AnalyzeType(symbolName string) (*GeneratedTypeInfo, error) {
	conn.generationLock.RLock()
	defer conn.generationLock.RUnlock()
	symbol, err := conn.symbolMetadata(symbolName)
	if err != nil {
		return nil, fmt.Errorf("failed to get symbol %s: %w", symbolName, err)
	}

	return conn.analyzeSymbol(symbol)
}

// =============================================================================
// Internal Implementation
// =============================================================================

// analyzeSymbol analyzes an ADS symbol and returns type information
func (conn *Connection) symbolMetadata(name string) (*Symbol, error) {
	conn.symbolLock.Lock()
	defer conn.symbolLock.Unlock()
	symbol, ok := conn.symbols[name]
	if !ok {
		return nil, fmt.Errorf("symbol %s does not exist", name)
	}
	return symbol, nil
}
func (conn *Connection) analyzeSymbol(symbol *Symbol) (*GeneratedTypeInfo, error) {
	if symbol == nil {
		return nil, fmt.Errorf("nil symbol")
	}
	info := &GeneratedTypeInfo{Name: symbol.FullName, GoName: goName(symbol.FullName), Type: symbol.DataType}
	if isArraySymbol(symbol) {
		info.IsArray = true
		info.GoType = conn.generateGoTypeString(symbol, "")
	} else if len(symbol.Children) > 0 {
		info.IsStruct = true
		info.GoType = info.GoName
		for _, child := range getFieldsByOffset(symbol) {
			info.Fields = append(info.Fields, conn.analyzeField(child))
		}
	} else {
		info.GoType = conn.generateGoTypeString(symbol, "")
	}
	if strings.Contains(info.GoType, "interface{}") {
		return nil, fmt.Errorf("unsupported ADS type %s", symbol.DataType)
	}
	for _, field := range info.Fields {
		if strings.Contains(field.GoType, "interface{}") {
			return nil, fmt.Errorf("unsupported ADS type %s", field.Type)
		}
	}
	return info, nil
}
func (conn *Connection) analyzeField(field *Symbol) GeneratedFieldInfo {
	return GeneratedFieldInfo{Name: field.Name, GoName: goName(field.Name), Type: field.DataType, GoType: conn.generateGoTypeString(field, "    "), Tag: field.Name, Offset: field.Offset}
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

	// Keep time types as aliases so their codec identity is retained.
	if info.GoType == "time.Time" || info.GoType == "time.Duration" {
		infoCopy := *info
		info = &infoCopy
		info.GoType = "= " + info.GoType
	}
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
		fmt.Fprintf(&builder, "\t%s %s %s\n",
			field.GoName, field.GoType, strconv.Quote("ads:"+strconv.Quote(field.Tag)))
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
		fmt.Fprintf(&builder, "%s    %s %s %s\n",
			indent, goName(field.Name), goType, strconv.Quote("ads:"+strconv.Quote(field.Name)))
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
			firstChild := getFieldsByOffset(symbol)[0]
			if firstChild != nil {
				elemType := conn.generateGoTypeString(firstChild, indent)
				return fmt.Sprintf("[%d]%s", size, elemType)
			}
		}
		return "[]interface{}"
	}

	// Handle nested structs
	if len(symbol.Children) > 0 {
		nested := conn.generateStructBody(symbol, indent)
		return fmt.Sprintf("struct {\n%s    }", nested)
	}

	// Primitive types
	if enumGoType := conn.enumGoType(symbol.DataType); enumGoType != "" {
		return enumGoType
	}
	return qualifiedGoType(Registry.GetGoType(baseType))
}

func (conn *Connection) enumGoType(dataType string) string {
	if _, err := conn.GetEnum(dataType); err == nil {
		return goName(dataType)
	}
	return ""
}

// qualifiedGoType converts a bare Go type name (e.g. "int16") to its
// ads-package-qualified form (e.g. "ads.Int16") for use in generated code
// that lives in an external package importing ads.
func qualifiedGoType(goType string) string {
	if q, ok := goTypeQualified[goType]; ok {
		return q
	}
	return goType // time.Time, interface{}, struct names, etc. stay as-is
}

var goTypeQualified = map[string]string{
	"int8":    "ads.Int8",
	"int16":   "ads.Int16",
	"int32":   "ads.Int32",
	"int64":   "ads.Int64",
	"uint8":   "ads.Uint8",
	"uint16":  "ads.Uint16",
	"uint32":  "ads.Uint32",
	"uint64":  "ads.Uint64",
	"float32": "ads.Float32",
	"float64": "ads.Float64",
	"bool":    "ads.Bool",
	"string":  "ads.String",
}

// =============================================================================
// Helper Functions
// =============================================================================

// goName converts TwinCAT naming to Go naming
func goName(name string) string {
	var out strings.Builder
	upper := true
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			upper = true
			continue
		}
		if out.Len() == 0 && !unicode.IsLetter(r) {
			out.WriteByte('X')
		}
		if upper {
			r = unicode.ToUpper(r)
			upper = false
		}
		out.WriteRune(r)
	}
	if out.Len() == 0 {
		return "X"
	}
	return out.String()
}
func capitalize(s string) string { return goName(s) }
