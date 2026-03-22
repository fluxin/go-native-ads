package ads

import (
	"reflect"
	"slices"
	"time"
)

// TwinCAT types that can be mapped to Go types
type (
	// Int8 represents TwinCAT SINT (-128 to 127)
	Int8 = int8
	// Int16 represents TwinCAT INT (-32768 to 32767)
	Int16 = int16
	// Int32 represents TwinCAT DINT (-2147483648 to 2147483647)
	Int32 = int32
	// Int64 represents TwinCAT LINT (-9223372036854775808 to 9223372036854775807)
	Int64 = int64
	// Uint8 represents TwinCAT BYTE/USINT (0 to 255)
	Uint8 = uint8
	// Uint16 represents TwinCAT WORD/UINT (0 to 65535)
	Uint16 = uint16
	// Uint32 represents TwinCAT DWORD/UDINT (0 to 4294967295)
	Uint32 = uint32
	// Uint64 represents TwinCAT LWORD/ULINT (0 to 18446744073709551615)
	Uint64 = uint64
	// Float32 represents TwinCAT REAL
	Float32 = float32
	// Float64 represents TwinCAT LREAL
	Float64 = float64
	// Bool represents TwinCAT BOOL
	Bool = bool
	// String represents TwinCAT STRING and time types (TIME, TOD, DATE, DT)
	String = string
)

// ADSValue is a constraint for types that can be written to ADS symbols
// Includes: SINT, INT, DINT, LINT, BYTE/USINT, WORD/UINT, DWORD/UDINT, LWORD/ULINT,
// REAL, LREAL, BOOL, STRING, TIME, TOD, DATE, DT
type ADSValue interface {
	~int8 | ~int16 | ~int32 | ~int64 | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~float32 | ~float64 | ~bool | ~string | time.Time
}

// TypeInfo holds comprehensive information about an ADS type
type TypeInfo struct {
	// GoType is the Go type name (e.g., "int16", "float32")
	GoType string
	// ADSType is the primary ADS type name (e.g., "INT", "REAL")
	ADSType string
	// Aliases are alternative ADS names for this type (e.g., "UINT" is alias for "WORD")
	Aliases []string
	// Size is the size in bytes
	Size int
	// Kind is the reflect.Kind for this type
	Kind reflect.Kind
}

// TypeRegistry provides a centralized registry for all ADS type mappings
type TypeRegistry struct {
	// byGoType maps Go type names to their info
	byGoType map[string]*TypeInfo
	// byADSType maps ADS type names to their info
	byADSType map[string]*TypeInfo
	// byKind maps reflect.Kind to ADS type info (for primitive validation)
	byKind map[reflect.Kind]*TypeInfo
	// knownTypes is a set of all supported ADS base types for quick lookup
	knownTypes map[string]bool
}

// Registry is the singleton type registry instance
var Registry = &TypeRegistry{
	byGoType:   make(map[string]*TypeInfo),
	byADSType:  make(map[string]*TypeInfo),
	byKind:     make(map[reflect.Kind]*TypeInfo),
	knownTypes: make(map[string]bool),
}

func init() {
	Registry.registerTypes()
}

// registerTypes initializes all type mappings
func (r *TypeRegistry) registerTypes() {
	types := []*TypeInfo{
		// Integer types
		{GoType: "int8", ADSType: "SINT", Aliases: nil, Size: 1, Kind: reflect.Int8},
		{GoType: "int16", ADSType: "INT", Aliases: nil, Size: 2, Kind: reflect.Int16},
		{GoType: "int32", ADSType: "DINT", Aliases: nil, Size: 4, Kind: reflect.Int32},
		{GoType: "int64", ADSType: "LINT", Aliases: nil, Size: 8, Kind: reflect.Int64},

		// Unsigned integer types
		{GoType: "uint8", ADSType: "BYTE", Aliases: []string{"USINT"}, Size: 1, Kind: reflect.Uint8},
		{GoType: "uint16", ADSType: "WORD", Aliases: []string{"UINT"}, Size: 2, Kind: reflect.Uint16},
		{GoType: "uint32", ADSType: "DWORD", Aliases: []string{"UDINT"}, Size: 4, Kind: reflect.Uint32},
		{GoType: "uint64", ADSType: "LWORD", Aliases: []string{"ULINT"}, Size: 8, Kind: reflect.Uint64},

		// Floating point types
		{GoType: "float32", ADSType: "REAL", Aliases: nil, Size: 4, Kind: reflect.Float32},
		{GoType: "float64", ADSType: "LREAL", Aliases: nil, Size: 8, Kind: reflect.Float64},

		// Boolean type
		{GoType: "bool", ADSType: "BOOL", Aliases: nil, Size: 1, Kind: reflect.Bool},

		// String type
		{GoType: "string", ADSType: "STRING", Aliases: nil, Size: 0, Kind: reflect.String},

		// Time types (all map to time.Time)
		{GoType: "time.Time", ADSType: "TIME", Aliases: nil, Size: 4, Kind: reflect.Struct},
		{GoType: "time.Time", ADSType: "TOD", Aliases: []string{"TIME_OF_DAY"}, Size: 4, Kind: reflect.Struct},
		{GoType: "time.Time", ADSType: "DATE", Aliases: nil, Size: 4, Kind: reflect.Struct},
		{GoType: "time.Time", ADSType: "DT", Aliases: []string{"DATE_AND_TIME"}, Size: 4, Kind: reflect.Struct},
	}

	for _, t := range types {
		// Register by Go type
		r.byGoType[t.GoType] = t

		// Register by ADS type
		r.byADSType[t.ADSType] = t
		r.knownTypes[t.ADSType] = true

		// Register aliases
		for _, alias := range t.Aliases {
			r.byADSType[alias] = t
			r.knownTypes[alias] = true
		}

		// Register by kind (for primitive types, map directly)
		if t.GoType != "time.Time" {
			r.byKind[t.Kind] = t
		}
	}
}

// GetByGoType returns type info for a Go type name
func (r *TypeRegistry) GetByGoType(goType string) (*TypeInfo, bool) {
	info, ok := r.byGoType[goType]
	return info, ok
}

// GetByADSType returns type info for an ADS type name
func (r *TypeRegistry) GetByADSType(adsType string) (*TypeInfo, bool) {
	info, ok := r.byADSType[adsType]
	return info, ok
}

// GetByKind returns type info for a reflect.Kind
func (r *TypeRegistry) GetByKind(kind reflect.Kind) (*TypeInfo, bool) {
	info, ok := r.byKind[kind]
	return info, ok
}

// IsKnownType checks if an ADS type name is known
func (r *TypeRegistry) IsKnownType(adsType string) bool {
	return r.knownTypes[adsType]
}

// IsAlias checks if an ADS type is an alias of an expected type
func (r *TypeRegistry) IsAlias(actualType, expectedType string) bool {
	if actualType == expectedType {
		return true
	}

	// Get info for expected type
	expectedInfo, ok := r.byADSType[expectedType]
	if !ok {
		return false
	}

	// Check if actual type is in the aliases list
	return slices.Contains(expectedInfo.Aliases, actualType)
}

// GetGoType returns the Go type name for an ADS type
func (r *TypeRegistry) GetGoType(adsType string) string {
	if info, ok := r.byADSType[adsType]; ok {
		return info.GoType
	}
	return "interface{}"
}

// GetADSType returns the ADS type name for a Go type
func (r *TypeRegistry) GetADSType(goType string) string {
	if info, ok := r.byGoType[goType]; ok {
		return info.ADSType
	}
	return ""
}
