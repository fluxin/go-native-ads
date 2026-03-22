package ads

import (
	"fmt"
)

// resolveType resolves custom types (enums) to their base types
func resolveType(conn *Connection, dataType string) (string, error) {
	// Empty type - can't resolve
	if dataType == "" {
		return "", fmt.Errorf("empty data type")
	}

	// Check if it's a known base type
	if Registry.IsKnownType(dataType) {
		return dataType, nil
	}

	// Check if it's a custom type in the datatypes map
	if conn.datatypes != nil {
		if dtInfo, ok := conn.datatypes[dataType]; ok {
			// Recursively resolve the base type
			return resolveType(conn, dtInfo.DataType)
		}
	}

	return "", fmt.Errorf("unknown data type: %s", dataType)
}
