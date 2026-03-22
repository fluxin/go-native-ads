package ads

import (
	"fmt"
	"sort"
)

type EnumInfo struct {
	Name     string
	BaseType string
	Values   map[string]int64
}

func (conn *Connection) GetEnum(typeName string) (EnumInfo, error) {
	conn.symbolLock.Lock()
	defer conn.symbolLock.Unlock()
	if conn.datatypes == nil {
		return EnumInfo{}, fmt.Errorf("datatype metadata is not loaded")
	}
	dt, ok := conn.datatypes[typeName]
	if !ok {
		return EnumInfo{}, fmt.Errorf("datatype %s not found", typeName)
	}
	if len(dt.EnumMembers) == 0 {
		return EnumInfo{}, fmt.Errorf("datatype %s is not an enum", typeName)
	}
	baseType, err := resolveType(conn, dt.DataType)
	if err != nil {
		baseType = dt.DataType
	}
	if baseType == "" {
		return EnumInfo{}, fmt.Errorf("enum %s has empty base type", typeName)
	}

	values := make(map[string]int64, len(dt.EnumMembers))
	for _, member := range dt.EnumMembers {
		values[member.Name] = member.Value
	}

	return EnumInfo{Name: dt.Name, BaseType: baseType, Values: values}, nil
}

func sortedEnumValueNames(values map[string]int64) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		vi, vj := values[names[i]], values[names[j]]
		if vi == vj {
			return names[i] < names[j]
		}
		return vi < vj
	})
	return names
}
