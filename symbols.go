package ads

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

type datatypeEntry struct {
	EntryLength   uint32
	Version       uint32
	HashValue     uint32
	TypeHashValue uint32
	Size          uint32
	Offs          uint32
	DataType      uint32
	Flags         uint32
	NameLength    uint16
	TypeLength    uint16
	CommentLength uint16
	ArrayDim      uint16
	SubItems      uint16
}

type datatypeArrayInfo struct {
	LBound   int32
	Elements uint32
}

type SymbolUploadDataType struct {
	arrayContinuation bool
	DatatypeEntry     datatypeEntry
	Name              string
	DataType          string
	Comment           string
	Children          map[string]*SymbolUploadDataType
	EnumMembers       []EnumMember
	TypeGUID          [16]byte
	Methods           []RPCMethod
	Attributes        []Attribute
}

type EnumMember struct {
	Name  string
	Value int64
}

const (
	datatypeFlagTypeGUID          uint32 = 0x00000080
	datatypeFlagCopyMask          uint32 = 0x00000200
	datatypeFlagMethodInfos       uint32 = 0x00000800
	datatypeFlagAttributes        uint32 = 0x00001000
	datatypeFlagEnumInfos         uint32 = 0x00002000
	datatypeFlagExtendedEnumInfos uint32 = 0x20000000
)

type symbolEntry struct {
	EntryLength   uint32
	IGroup        uint32
	IOffs         uint32
	Size          uint32
	DataType      uint32
	Flags         uint32
	NameLength    uint16
	TypeLength    uint16
	CommentLength uint16
}

type symbolUploadSymbol struct {
	SymbolEntry symbolEntry
	Name        string
	DataType    string
	Comment     string
	Children    map[string]*symbolUploadSymbol
}

type SymbolUploadInfo struct {
	SymbolCount    uint32
	SymbolLength   uint32
	DataTypeCount  uint32
	DataTypeLength uint32
	ExtraCount     uint32
	ExtraLength    uint32
}

type Symbol struct {
	cache             *symbolCache
	arrayContinuation bool
	FullName          string
	LastUpdateTime    time.Time
	MinUpdateInterval time.Duration
	Name              string
	DataType          string
	Comment           string
	Handle            uint32
	Group             uint32
	Offset            uint32
	Length            uint32

	Parent   *Symbol
	Children map[string]*Symbol
}

const maxMetadataNodes = 100000

func readMetadataString(buf *bytes.Buffer, n uint16) (string, error) {
	if buf.Len() < int(n)+1 {
		return "", fmt.Errorf("truncated metadata string")
	}
	value := buf.Next(int(n) + 1)
	if value[len(value)-1] != 0 {
		return "", fmt.Errorf("metadata string lacks terminator")
	}
	return string(value[:len(value)-1]), nil
}
func metadataEntry(buf *bytes.Buffer, min int) (*bytes.Buffer, error) {
	if buf.Len() < 4 {
		return nil, fmt.Errorf("truncated metadata entry length")
	}
	n := binary.LittleEndian.Uint32(buf.Bytes())
	if n < uint32(min) || uint64(n) > uint64(buf.Len()) {
		return nil, fmt.Errorf("invalid metadata entry length %d", n)
	}
	return bytes.NewBuffer(buf.Next(int(n))), nil
}
func parseUploadSymbolInfoSymbols(data []byte, datatypes map[string]SymbolUploadDataType) (map[string]*Symbol, error) {
	symbols := make(map[string]*Symbol)
	buf := bytes.NewBuffer(data)
	budget := maxMetadataNodes
	for buf.Len() > 0 {
		entryBuf, err := metadataEntry(buf, 33)
		if err != nil {
			return nil, err
		}
		var entry symbolEntry
		if err := binary.Read(entryBuf, binary.LittleEndian, &entry); err != nil {
			return nil, err
		}
		name, err := readMetadataString(entryBuf, entry.NameLength)
		if err != nil {
			return nil, err
		}
		dt, err := readMetadataString(entryBuf, entry.TypeLength)
		if err != nil {
			return nil, err
		}
		comment, err := readMetadataString(entryBuf, entry.CommentLength)
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(dt, "STRING(") {
			dt = "STRING"
		}
		symbol := &Symbol{FullName: name, Name: name, DataType: dt, Comment: comment, Length: entry.Size, Group: entry.IGroup, Offset: entry.IOffs}
		budget--
		if budget < 0 {
			return nil, fmt.Errorf("symbol expansion exceeds limit")
		}
		if definition, ok := datatypes[dt]; ok {
			children, err := materializeChildren(&definition, symbol, datatypes, 0, &budget)
			if err != nil {
				return nil, err
			}
			symbol.Children = children
		}
		symbols[name] = symbol
		addChildren(symbol, symbols)
	}
	return symbols, nil
}
func addChildren(symbol *Symbol, symbols map[string]*Symbol) {
	for _, child := range symbol.Children {
		if _, ok := symbols[child.FullName]; !ok {
			symbols[child.FullName] = child
			addChildren(child, symbols)
		}
	}
}
func materializeChildren(data *SymbolUploadDataType, parent *Symbol, types map[string]SymbolUploadDataType, depth int, budget *int) (map[string]*Symbol, error) {
	if len(data.Children) == 0 {
		return nil, nil
	}
	if depth >= 64 {
		return nil, fmt.Errorf("cyclic or excessively nested datatype %s", data.Name)
	}
	children := make(map[string]*Symbol, len(data.Children))
	for key, segment := range data.Children {
		*budget--
		if *budget < 0 {
			return nil, fmt.Errorf("symbol expansion exceeds limit")
		}
		path := parent.FullName + "." + segment.Name
		if strings.HasPrefix(segment.Name, "[") {
			path = parent.FullName + segment.Name
			if parent.arrayContinuation {
				path = strings.TrimSuffix(parent.FullName, "]") + "," + segment.Name[1:]
			}
		}
		child := &Symbol{FullName: path, Name: segment.Name, DataType: segment.DataType, Comment: segment.Comment, Length: segment.DatatypeEntry.Size, Group: parent.Group, Offset: segment.DatatypeEntry.Offs, Parent: parent, arrayContinuation: segment.arrayContinuation}
		if uint64(child.Offset)+uint64(child.Length) > uint64(parent.Length) {
			return nil, fmt.Errorf("child %s outside parent layout", path)
		}
		definition := segment
		if len(segment.Children) == 0 {
			if dt, ok := types[segment.DataType]; ok {
				definition = &dt
			}
		}
		nested, err := materializeChildren(definition, child, types, depth+1, budget)
		if err != nil {
			return nil, err
		}
		child.Children = nested
		children[key] = child
	}
	return children, nil
}
func parseUploadSymbolInfoDataTypes(data []byte) (map[string]SymbolUploadDataType, error) {
	buf := bytes.NewBuffer(data)
	types := make(map[string]SymbolUploadDataType)
	budget := maxMetadataNodes
	for buf.Len() > 0 {
		entry, err := decodeDatatype(buf, 0, &budget)
		if err != nil {
			return nil, err
		}
		types[entry.Name] = entry
	}
	return types, nil
}
func decodeSymbolUploadDataType(buf *bytes.Buffer, parent string) (SymbolUploadDataType, error) {
	budget := maxMetadataNodes
	return decodeDatatype(buf, 0, &budget)
}
func decodeDatatype(data *bytes.Buffer, depth int, budget *int) (header SymbolUploadDataType, err error) {
	if depth >= 64 {
		return header, fmt.Errorf("datatype nesting exceeds limit")
	}
	*budget--
	if *budget < 0 {
		return header, fmt.Errorf("datatype count exceeds limit")
	}
	buf, err := metadataEntry(data, 45)
	if err != nil {
		return header, err
	}
	if err = binary.Read(buf, binary.LittleEndian, &header.DatatypeEntry); err != nil {
		return header, err
	}
	entry := header.DatatypeEntry
	if header.Name, err = readMetadataString(buf, entry.NameLength); err != nil {
		return header, err
	}
	if header.DataType, err = readMetadataString(buf, entry.TypeLength); err != nil {
		return header, err
	}
	if header.Comment, err = readMetadataString(buf, entry.CommentLength); err != nil {
		return header, err
	}
	if strings.HasPrefix(header.DataType, "STRING(") {
		header.DataType = "STRING"
	}
	if entry.ArrayDim > 64 {
		return header, fmt.Errorf("array dimension exceeds limit")
	}
	if entry.ArrayDim > 0 {
		levels := make([]datatypeArrayInfo, entry.ArrayDim)
		if err = binary.Read(buf, binary.LittleEndian, levels); err != nil {
			return header, err
		}
		header.Children, err = arrayChildren(levels, header.DataType, entry.Size, budget)
		if err != nil {
			return header, err
		}
	} else if entry.SubItems > 0 {
		header.Children = make(map[string]*SymbolUploadDataType)
		for i := 0; i < int(entry.SubItems); i++ {
			child, e := decodeDatatype(buf, depth+1, budget)
			if e != nil {
				return header, e
			}
			if _, exists := header.Children[child.Name]; exists {
				return header, fmt.Errorf("duplicate datatype field %s", child.Name)
			}
			header.Children[child.Name] = &child
		}
	}
	if err = parseDatatypeTail(buf, &header, budget); err != nil {
		return header, err
	}
	return header, nil
}

func hasDatatypeFlag(flags uint32, flag uint32) bool {
	return flags&flag == flag
}

func parseEnumMembers(buf *bytes.Buffer, baseType string, valueSize int) ([]EnumMember, error) {
	if valueSize <= 0 {
		return nil, fmt.Errorf("invalid enum value size: %d", valueSize)
	}
	if buf.Len() < 2 {
		return nil, fmt.Errorf("enum member count missing")
	}
	countBytes := buf.Next(2)
	count := int(binary.LittleEndian.Uint16(countBytes))
	members := make([]EnumMember, 0, count)
	for i := range count {
		if buf.Len() < 1 {
			return nil, fmt.Errorf("enum member %d name length missing", i)
		}
		nameLen := int(buf.Next(1)[0])
		need := nameLen + 1 + valueSize
		if buf.Len() < need {
			return nil, fmt.Errorf("enum member %d payload truncated", i)
		}
		nameBytes := buf.Next(nameLen + 1)
		name := strings.TrimRight(string(nameBytes), "\x00")
		valueBytes := buf.Next(valueSize)
		value, err := decodeEnumValue(baseType, valueBytes)
		if err != nil {
			return nil, err
		}
		members = append(members, EnumMember{Name: name, Value: value})
	}
	return members, nil
}

func skipExtendedEnumInfos(buf *bytes.Buffer, count int) error {
	for i := range count {
		if buf.Len() < 2 {
			return fmt.Errorf("extended enum info %d header missing", i)
		}
		entryLen := int(binary.LittleEndian.Uint16(buf.Next(2)))
		if entryLen < 2 || buf.Len() < entryLen-2 {
			return fmt.Errorf("extended enum info %d truncated", i)
		}
		buf.Next(entryLen - 2)
	}
	return nil
}

func decodeEnumValue(baseType string, data []byte) (int64, error) {
	switch baseType {
	case "SINT":
		if len(data) < 1 {
			return 0, fmt.Errorf("enum value too short for SINT")
		}
		return int64(int8(data[0])), nil
	case "USINT", "BYTE":
		if len(data) < 1 {
			return 0, fmt.Errorf("enum value too short for BYTE/USINT")
		}
		return int64(data[0]), nil
	case "INT":
		if len(data) < 2 {
			return 0, fmt.Errorf("enum value too short for INT")
		}
		return int64(int16(binary.LittleEndian.Uint16(data))), nil
	case "UINT", "WORD":
		if len(data) < 2 {
			return 0, fmt.Errorf("enum value too short for UINT/WORD")
		}
		return int64(binary.LittleEndian.Uint16(data)), nil
	case "DINT":
		if len(data) < 4 {
			return 0, fmt.Errorf("enum value too short for DINT")
		}
		return int64(int32(binary.LittleEndian.Uint32(data))), nil
	case "UDINT", "DWORD":
		if len(data) < 4 {
			return 0, fmt.Errorf("enum value too short for UDINT/DWORD")
		}
		return int64(binary.LittleEndian.Uint32(data)), nil
	case "LINT":
		if len(data) < 8 {
			return 0, fmt.Errorf("enum value too short for LINT")
		}
		return int64(binary.LittleEndian.Uint64(data)), nil
	case "ULINT", "LWORD":
		if len(data) < 8 {
			return 0, fmt.Errorf("enum value too short for ULINT/LWORD")
		}
		u := binary.LittleEndian.Uint64(data)
		if u > uint64(^uint64(0)>>1) {
			return 0, fmt.Errorf("enum value %d exceeds int64", u)
		}
		return int64(u), nil
	default:
		return 0, fmt.Errorf("unsupported enum base type: %s", baseType)
	}
}

func makeArrayChildren(levels []datatypeArrayInfo, dt string, size uint32) map[string]*SymbolUploadDataType {
	budget := maxMetadataNodes
	children, _ := arrayChildren(levels, dt, size, &budget)
	return children
}
func arrayChildren(levels []datatypeArrayInfo, dt string, size uint32, budget *int) (map[string]*SymbolUploadDataType, error) {
	if len(levels) == 0 {
		return nil, nil
	}
	level := levels[0]
	if level.Elements == 0 || size%level.Elements != 0 || level.Elements > uint32(*budget) {
		return nil, fmt.Errorf("invalid or excessive array dimensions")
	}
	*budget -= int(level.Elements)
	elementSize := size / level.Elements
	nested, err := arrayChildren(levels[1:], dt, elementSize, budget)
	if err != nil {
		return nil, err
	}
	children := make(map[string]*SymbolUploadDataType, int(level.Elements))
	for i := uint32(0); i < level.Elements; i++ {
		index := int64(level.LBound) + int64(i)
		if index > 2147483647 {
			return nil, fmt.Errorf("array bound overflows int32")
		}
		name := fmt.Sprintf("[%d]", index)
		children[name] = &SymbolUploadDataType{Name: name, DataType: dt, DatatypeEntry: datatypeEntry{Offs: i * elementSize, Size: elementSize}, Children: nested, arrayContinuation: len(levels) > 1}
	}
	return children, nil
}

// isArraySymbol checks if a symbol represents an array
// Arrays have children with names like "[0]", "[1]", etc.
func isArraySymbol(symbol *Symbol) bool {
	if len(symbol.Children) == 0 {
		return false
	}
	for name := range symbol.Children {
		if len(name) < 3 || name[0] != '[' || name[len(name)-1] != ']' {
			return false
		}
	}
	return true
}

// getFieldsByOffset returns symbol children sorted by offset
func getFieldsByOffset(symbol *Symbol) []*Symbol { return cacheFor(symbol).ordered }
