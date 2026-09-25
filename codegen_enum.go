package ads

import (
	"fmt"
	"strings"
)

// GenerateEnum emits a named enum and its constants.
func (conn *Connection) GenerateEnum(typeName string) (string, error) {
	enumInfo, err := conn.GetEnum(typeName)
	if err != nil {
		return "", err
	}
	goType := Registry.GetGoType(enumInfo.BaseType)
	if goType == "interface{}" {
		return "", fmt.Errorf("unsupported enum base type %s", enumInfo.BaseType)
	}

	goTypeName := goName(enumInfo.Name)
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("// %s represents TwinCAT enum %s\n", goTypeName, enumInfo.Name))
	builder.WriteString(fmt.Sprintf("type %s %s\n\n", goTypeName, goType))
	builder.WriteString("const (\n")
	for _, name := range sortedEnumValueNames(enumInfo.Values) {
		builder.WriteString(fmt.Sprintf("\t%s %s = %d\n", goTypeName+"_"+goName(name), goTypeName, enumInfo.Values[name]))
	}
	builder.WriteString(")\n")
	return builder.String(), nil
}
