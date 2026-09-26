package ads

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// Attribute is a PLC metadata attribute. Metadata returned by Connection is immutable.
type Attribute struct{ Name, Value string }

// RPCParameterFlags describe how a parameter participates in the ADS call.
type RPCParameterFlags uint32

const (
	RPCIn                  RPCParameterFlags = 1
	RPCOut                 RPCParameterFlags = 2
	RPCByReference         RPCParameterFlags = 4
	RPCParameterAttributes RPCParameterFlags = 0x40
)

// RPCParameter retains the server's declaration order, direction and ABI metadata.
type RPCParameter struct {
	Name, DataType, Comment      string
	Size, AlignSize, ADSDataType uint32
	Flags                        RPCParameterFlags
	TypeGUID                     [16]byte
	// LengthIsParameterIndex is one-based in the complete parameter list;
	// zero means no linked count. The count is in referenced elements.
	LengthIsParameterIndex uint16
	Attributes             []Attribute
}

// RPCMethod describes an uploaded PLC method. Parameter order is significant.
type RPCMethod struct {
	Name, ReturnType, Comment                                                   string
	Version, VTableIndex, ReturnSize, ReturnAlignSize, ReturnADSDataType, Flags uint32
	ReturnTypeGUID                                                              [16]byte
	Parameters                                                                  []RPCParameter
	Attributes                                                                  []Attribute
}

func readAttributes(buf *bytes.Buffer) ([]Attribute, error) {
	var count uint16
	if err := binary.Read(buf, binary.LittleEndian, &count); err != nil {
		return nil, fmt.Errorf("attribute count: %w", err)
	}
	if int(count) > buf.Len()/4 {
		return nil, fmt.Errorf("truncated attributes")
	}
	attrs := make([]Attribute, 0, count)
	for range count {
		if buf.Len() < 2 {
			return nil, fmt.Errorf("attribute lengths missing")
		}
		lengths := buf.Next(2)
		name, err := readMetadataString(buf, uint16(lengths[0]))
		if err != nil {
			return nil, err
		}
		value, err := readMetadataString(buf, uint16(lengths[1]))
		if err != nil {
			return nil, err
		}
		attrs = append(attrs, Attribute{name, value})
	}
	return attrs, nil
}

func parseRPCMethods(buf *bytes.Buffer, budget *int) ([]RPCMethod, error) {
	var count uint16
	if err := binary.Read(buf, binary.LittleEndian, &count); err != nil {
		return nil, fmt.Errorf("RPC method count: %w", err)
	}
	methods := make([]RPCMethod, 0)
	names := make(map[string]bool)
	for range count {
		*budget--
		if *budget < 0 {
			return nil, fmt.Errorf("RPC metadata exceeds node limit")
		}
		entry, err := metadataEntry(buf, 59)
		if err != nil {
			return nil, fmt.Errorf("RPC method: %w", err)
		}
		var h struct {
			Length, Version, VTable, ReturnSize, ReturnAlign, Reserved uint32
			GUID                                                       [16]byte
			ADSDataType, Flags                                         uint32
			NameLen, TypeLen, CommentLen, Parameters                   uint16
		}
		if err = binary.Read(entry, binary.LittleEndian, &h); err != nil {
			return nil, err
		}
		m := RPCMethod{Version: h.Version, VTableIndex: h.VTable, ReturnSize: h.ReturnSize, ReturnAlignSize: h.ReturnAlign, ReturnTypeGUID: h.GUID, ReturnADSDataType: h.ADSDataType, Flags: h.Flags}
		if m.Name, err = readMetadataString(entry, h.NameLen); err != nil {
			return nil, err
		}
		if m.ReturnType, err = readMetadataString(entry, h.TypeLen); err != nil {
			return nil, err
		}
		if m.Comment, err = readMetadataString(entry, h.CommentLen); err != nil {
			return nil, err
		}
		if m.Name == "" || names[m.Name] {
			return nil, fmt.Errorf("invalid or duplicate RPC method %q", m.Name)
		}
		names[m.Name] = true
		paramNames := make(map[string]bool)
		for range h.Parameters {
			*budget--
			if *budget < 0 {
				return nil, fmt.Errorf("RPC metadata exceeds node limit")
			}
			param, err := metadataEntry(entry, 51)
			if err != nil {
				return nil, fmt.Errorf("RPC %s parameter: %w", m.Name, err)
			}
			var p struct {
				Length, Size, Align, ADSDataType, Flags, Reserved uint32
				GUID                                              [16]byte
				LengthIs, NameLen, TypeLen, CommentLen            uint16
			}
			if err = binary.Read(param, binary.LittleEndian, &p); err != nil {
				return nil, err
			}
			v := RPCParameter{Size: p.Size, AlignSize: p.Align, ADSDataType: p.ADSDataType, Flags: RPCParameterFlags(p.Flags), TypeGUID: p.GUID, LengthIsParameterIndex: p.LengthIs}
			if v.Name, err = readMetadataString(param, p.NameLen); err != nil {
				return nil, err
			}
			if v.DataType, err = readMetadataString(param, p.TypeLen); err != nil {
				return nil, err
			}
			if v.Comment, err = readMetadataString(param, p.CommentLen); err != nil {
				return nil, err
			}
			if v.Name == "" || paramNames[v.Name] {
				return nil, fmt.Errorf("invalid or duplicate RPC parameter %q", v.Name)
			}
			paramNames[v.Name] = true
			if v.Flags&RPCParameterAttributes != 0 {
				if v.Attributes, err = readAttributes(param); err != nil {
					return nil, err
				}
			}
			m.Parameters = append(m.Parameters, v)
		}
		if m.Flags&8 != 0 {
			if m.Attributes, err = readAttributes(entry); err != nil {
				return nil, err
			}
		}
		methods = append(methods, m)
	}
	return methods, nil
}

func parseDatatypeTail(buf *bytes.Buffer, dt *SymbolUploadDataType, budget *int) (err error) {
	flags := dt.DatatypeEntry.Flags
	if hasDatatypeFlag(flags, datatypeFlagTypeGUID) {
		if buf.Len() < 16 {
			return fmt.Errorf("datatype tail missing type GUID")
		}
		copy(dt.TypeGUID[:], buf.Next(16))
	}
	if hasDatatypeFlag(flags, datatypeFlagCopyMask) {
		if uint64(buf.Len()) < uint64(dt.DatatypeEntry.Size) {
			return fmt.Errorf("datatype tail missing copy mask")
		}
		buf.Next(int(dt.DatatypeEntry.Size))
	}
	if hasDatatypeFlag(flags, datatypeFlagMethodInfos) {
		if dt.Methods, err = parseRPCMethods(buf, budget); err != nil {
			return err
		}
	}
	if hasDatatypeFlag(flags, datatypeFlagAttributes) {
		if dt.Attributes, err = readAttributes(buf); err != nil {
			return err
		}
	}
	if hasDatatypeFlag(flags, datatypeFlagEnumInfos) {
		dt.EnumMembers, err = parseEnumMembers(buf, dt.DataType, int(dt.DatatypeEntry.Size))
		if err != nil {
			return err
		}
		if hasDatatypeFlag(flags, datatypeFlagExtendedEnumInfos) {
			return skipExtendedEnumInfos(buf, len(dt.EnumMembers))
		}
	}
	return nil
}
