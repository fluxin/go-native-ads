package ads

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log/slog"
	"sort"
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
	LBound   uint32
	Elements uint32
}

type SymbolUploadDataType struct {
	DatatypeEntry datatypeEntry
	Name          string
	DataType      string
	Comment       string
	Children      map[string]*SymbolUploadDataType
	EnumMembers   []EnumMember
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

func parseUploadSymbolInfoSymbols(data []byte, datatypes map[string]SymbolUploadDataType) (symbols map[string]*Symbol, err error) {
	symbols = map[string]*Symbol{}
	buf := bytes.NewBuffer(data)

	for buf.Len() > 0 {
		startLen := buf.Len()
		entry := symbolEntry{}
		err := binary.Read(buf, binary.LittleEndian, &entry)
		if err != nil {
			return nil, fmt.Errorf("failed to read symbol entry: %w", err)
		}

		name := make([]byte, entry.NameLength)
		dt := make([]byte, entry.TypeLength)
		comment := make([]byte, entry.CommentLength)

		err = binary.Read(buf, binary.LittleEndian, name)
		if err != nil {
			return nil, fmt.Errorf("failed to read symbol name: %w", err)
		}
		buf.Next(1)

		err = binary.Read(buf, binary.LittleEndian, dt)
		if err != nil {
			return nil, fmt.Errorf("failed to read symbol type: %w", err)
		}
		buf.Next(1)

		err = binary.Read(buf, binary.LittleEndian, comment)
		if err != nil {
			return nil, fmt.Errorf("failed to read symbol comment: %w", err)
		}
		buf.Next(1)
		item := symbolUploadSymbol{}
		item.Name = string(name)
		item.DataType = string(dt)
		if len(item.DataType) >= 6 {
			if item.DataType[:6] == "STRING" {
				item.DataType = "STRING"
			}
		}
		item.Comment = string(comment)
		item.SymbolEntry = entry
		endLen := buf.Len()
		symbol := addSymbol(item, datatypes)

		symbols[item.Name] = symbol
		addChildren(symbol, symbols)

		buf.Next(int(item.SymbolEntry.EntryLength) - (startLen - endLen))
	}
	return
}

func addChildren(symbol *Symbol, symbols map[string]*Symbol) {
	for _, child := range symbol.Children {
		if _, ok := symbols[child.FullName]; !ok {
			symbols[child.FullName] = child
			addChildren(child, symbols)
		}
	}
}

func addSymbol(symbol symbolUploadSymbol, datatypes map[string]SymbolUploadDataType) *Symbol {
	sym := &Symbol{}
	sym.Name = symbol.Name
	sym.LastUpdateTime = time.Now()
	sym.MinUpdateInterval = time.Millisecond * 50
	sym.FullName = symbol.Name
	sym.DataType = symbol.DataType
	sym.Comment = symbol.Comment
	sym.Length = symbol.SymbolEntry.Size

	sym.Group = symbol.SymbolEntry.IGroup
	sym.Offset = symbol.SymbolEntry.IOffs

	dt, ok := datatypes[symbol.DataType]
	if ok {
		sym.Children = dt.addOffset(sym, datatypes, sym.Group, sym.Offset)
	}

	return sym
}

func (data *SymbolUploadDataType) addOffset(parent *Symbol, datatypes map[string]SymbolUploadDataType, group uint32, offset uint32) (childs map[string]*Symbol) {
	childs = map[string]*Symbol{}

	var path string

	for key, segment := range data.Children {

		if !strings.HasPrefix(segment.Name, "[") {
			path = fmt.Sprint(parent.FullName, ".", segment.Name)
		} else {
			path = fmt.Sprint(parent.FullName, segment.Name)
		}

		child := Symbol{}
		child.Name = segment.Name
		child.LastUpdateTime = time.Now()
		child.MinUpdateInterval = time.Millisecond * 50
		child.FullName = path
		child.DataType = segment.DataType
		child.Comment = segment.Comment
		child.Length = segment.DatatypeEntry.Size
		// Uppdate with area and offset
		child.Group = group
		child.Offset = segment.DatatypeEntry.Offs

		child.Parent = parent

		// Check if subitems exist
		dt, ok := datatypes[segment.DataType]
		if ok {
			//log.Warn("Found sub ",segment.DataType);
			child.Children = dt.addOffset(&child, datatypes, child.Group, child.Offset)

		}

		childs[key] = &child
	}

	return
}

func parseUploadSymbolInfoDataTypes(data []byte) (datatypes map[string]SymbolUploadDataType, err error) {
	buf := bytes.NewBuffer(data)
	datatypes = make(map[string]SymbolUploadDataType)
	for buf.Len() > 0 {
		header, _ := decodeSymbolUploadDataType(buf, "")
		datatypes[header.Name] = header
	}
	return
}

func decodeSymbolUploadDataType(data *bytes.Buffer, parent string) (header SymbolUploadDataType, err error) {

	dtEntry := datatypeEntry{}
	header = SymbolUploadDataType{}

	totalSize := data.Len()

	if totalSize < 48 {
		return header, fmt.Errorf("%s - Wrong size < 48 bytes", parent)
	}

	err = binary.Read(data, binary.LittleEndian, &dtEntry)
	if err != nil {
		return header, fmt.Errorf("failed to read datatype entry: %w", err)
	}

	name := make([]byte, dtEntry.NameLength)
	dt := make([]byte, dtEntry.TypeLength)
	comment := make([]byte, dtEntry.CommentLength)

	err = binary.Read(data, binary.LittleEndian, name)
	if err != nil {
		return header, fmt.Errorf("failed to read datatype name: %w", err)
	}
	data.Next(1)

	err = binary.Read(data, binary.LittleEndian, dt)
	if err != nil {
		return header, fmt.Errorf("failed to read datatype type: %w", err)
	}
	data.Next(1)

	err = binary.Read(data, binary.LittleEndian, comment)
	if err != nil {
		return header, fmt.Errorf("failed to read datatype comment: %w", err)
	}
	data.Next(1)

	header.Name = string(name)
	header.DataType = string(dt)
	header.Comment = string(comment)

	header.DatatypeEntry = dtEntry

	if len(header.DataType) > 6 {
		if header.DataType[:6] == "STRING" {
			header.DataType = "STRING"
		}
	}

	childLen := int(dtEntry.EntryLength) - (totalSize - data.Len())
	if childLen <= 0 {
		return
	}

	childs := make([]byte, childLen)
	n, err := data.Read(childs)
	if err != nil {
		slog.Error("error reading childs", "read_bytes", n, "expected_bytes", childLen, "error", err)
	}

	if len(childs) == 0 {
		return
	}

	buf := bytes.NewBuffer(childs)
	if header.Children == nil {
		header.Children = map[string]*SymbolUploadDataType{}
	}
	if header.DatatypeEntry.ArrayDim > 0 {
		// Children is an array
		var arrayInfo datatypeArrayInfo
		arrayLevels := []datatypeArrayInfo{}

		for i := 0; i < int(header.DatatypeEntry.ArrayDim); i++ {
			err = binary.Read(buf, binary.LittleEndian, &arrayInfo)
			if err != nil {
				slog.Error("error reading array", "error", err)
			}
			arrayLevels = append(arrayLevels, arrayInfo)
		}
		header.Children = makeArrayChildren(arrayLevels, header.DataType, header.DatatypeEntry.Size)
	} else {
		// Children is standard variables
		for j := 0; j < (int)(dtEntry.SubItems); j++ {
			child, err := decodeSymbolUploadDataType(buf, header.Name)
			if err != nil {
				slog.Error("error reading array", "error", err)
			}
			header.Children[child.Name] = &child
		}
	}

	if hasDatatypeFlag(dtEntry.Flags, datatypeFlagEnumInfos) {
		enumMembers, err := parseEnumMembersFromDatatypeTail(buf, dtEntry.Flags, int(dtEntry.Size), header.DataType)
		if err != nil {
			slog.Debug("failed parsing enum members", "type", header.Name, "error", err)
		} else {
			header.EnumMembers = enumMembers
		}
	}

	return
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
	for i := 0; i < count; i++ {
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

func parseEnumMembersFromDatatypeTail(buf *bytes.Buffer, flags uint32, valueSize int, baseType string) ([]EnumMember, error) {
	// Tail layout can contain optional sections before enum infos.
	if hasDatatypeFlag(flags, datatypeFlagTypeGUID) {
		if buf.Len() < 16 {
			return nil, fmt.Errorf("datatype tail missing type guid")
		}
		buf.Next(16)
	}
	if hasDatatypeFlag(flags, datatypeFlagCopyMask) {
		if buf.Len() < valueSize {
			return nil, fmt.Errorf("datatype tail missing copy mask")
		}
		buf.Next(valueSize)
	}
	if hasDatatypeFlag(flags, datatypeFlagMethodInfos) {
		if err := skipDatatypeMethodInfos(buf); err != nil {
			return nil, err
		}
	}
	if hasDatatypeFlag(flags, datatypeFlagAttributes) {
		if err := skipDatatypeAttributes(buf); err != nil {
			return nil, err
		}
	}

	members, err := parseEnumMembers(buf, baseType, valueSize)
	if err != nil {
		return nil, err
	}

	if hasDatatypeFlag(flags, datatypeFlagExtendedEnumInfos) {
		_ = skipExtendedEnumInfos(buf, len(members))
	}
	return members, nil
}

func skipDatatypeMethodInfos(buf *bytes.Buffer) error {
	if buf.Len() < 2 {
		return fmt.Errorf("datatype method info count missing")
	}
	count := int(binary.LittleEndian.Uint16(buf.Next(2)))
	for i := 0; i < count; i++ {
		if buf.Len() < 4 {
			return fmt.Errorf("datatype method %d entry length missing", i)
		}
		entryLen := int(binary.LittleEndian.Uint32(buf.Next(4)))
		if entryLen < 4 || buf.Len() < entryLen-4 {
			return fmt.Errorf("datatype method %d entry truncated", i)
		}
		buf.Next(entryLen - 4)
	}
	return nil
}

func skipDatatypeAttributes(buf *bytes.Buffer) error {
	if buf.Len() < 2 {
		return fmt.Errorf("datatype attribute count missing")
	}
	count := int(binary.LittleEndian.Uint16(buf.Next(2)))
	for i := 0; i < count; i++ {
		if buf.Len() < 2 {
			return fmt.Errorf("datatype attribute %d lengths missing", i)
		}
		nameLen := int(buf.Next(1)[0])
		valueLen := int(buf.Next(1)[0])
		need := nameLen + 1 + valueLen + 1
		if buf.Len() < need {
			return fmt.Errorf("datatype attribute %d payload truncated", i)
		}
		buf.Next(need)
	}
	return nil
}

func skipExtendedEnumInfos(buf *bytes.Buffer, count int) error {
	for i := 0; i < count; i++ {
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

func makeArrayChildren(levels []datatypeArrayInfo, dt string, size uint32) (childs map[string]*SymbolUploadDataType) {
	childs = map[string]*SymbolUploadDataType{}

	if len(levels) < 1 {
		return
	}

	level := levels[:1][0]
	subChildren := makeArrayChildren(levels[1:], dt, size)

	var offset uint32

	for i := level.LBound; i < level.LBound+level.Elements; i++ {
		name := fmt.Sprint("[", i, "]")

		child := SymbolUploadDataType{}
		child.Name = name
		child.DataType = dt
		child.DatatypeEntry.Offs = offset
		child.DatatypeEntry.Size = size / level.Elements
		child.Children = subChildren

		//child.Walk("")

		childs[name] = &child
		offset += size / level.Elements
	}

	return
}

// isArraySymbol checks if a symbol represents an array
// Arrays have children with names like "[0]", "[1]", etc.
func isArraySymbol(symbol *Symbol) bool {
	if len(symbol.Children) == 0 {
		return false
	}
	for name := range symbol.Children {
		return len(name) > 2 && name[0] == '[' && name[len(name)-1] == ']'
	}
	return false
}

// getFieldsByOffset returns symbol children sorted by offset
func getFieldsByOffset(symbol *Symbol) []*Symbol {
	fields := make([]*Symbol, 0, len(symbol.Children))
	for _, child := range symbol.Children {
		fields = append(fields, child)
	}
	sort.Slice(fields, func(i, j int) bool {
		return fields[i].Offset < fields[j].Offset
	})
	return fields
}
