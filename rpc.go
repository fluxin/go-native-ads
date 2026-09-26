package ads

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
)

var ErrRPCSignatureChanged = errors.New("RPC signature changed; regenerate or rebind the client")

// UnsupportedRPCError rejects a signature whose wire semantics are not supported.
type UnsupportedRPCError struct{ Method, Parameter, Reason string }

func (e *UnsupportedRPCError) Error() string {
	return fmt.Sprintf("unsupported RPC %s parameter %q: %s", e.Method, e.Parameter, e.Reason)
}

// RPCOptions pins a generated client's complete method/type signature.
type RPCOptions struct{ Signature string }

// RPC is a typed method binding. Concurrent Calls have independent payloads.
// A call is sent at most once. An error after sending does not prove the PLC
// did not execute it; cancellation only stops the local wait.
type RPC[I, O any] struct {
	conn                        *Connection
	instance, method, signature string
	mu                          sync.Mutex
	epoch                       uint64
	input, output               *rpcRecordCodec
}

// BindRPC validates a method. Inputs and outputs are structs with
// ads tags matching parameter names. The result's ads:"$return" field holds the
// PLC return value. Use struct{} for an empty input or output record.
// References use values, not Go pointers. Length-linked buffers use slices;
// their lengths must match the linked input count (in elements).
// Handles are acquired lazily by Call, then owned until the connection generation ends.
func (conn *Connection) BindRPC[I, O any](instance, method string, options ...RPCOptions) (*RPC[I, O], error) {
	if len(options) > 1 {
		return nil, fmt.Errorf("at most one RPCOptions is allowed")
	}
	r := &RPC[I, O]{conn: conn, instance: instance, method: method}
	if len(options) == 1 {
		r.signature = options[0].Signature
	}
	if err := conn.beginOperation(); err != nil {
		return nil, err
	}
	defer conn.endOperation()
	_, _, err := r.binding()
	if err != nil {
		return nil, err
	}
	return r, nil
}

func (r *RPC[I, O]) binding() (*rpcRecordCodec, *rpcRecordCodec, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	epoch := r.conn.CurrentEpoch()
	if r.input != nil && r.epoch == epoch {
		return r.input, r.output, nil
	}
	method, types, err := r.conn.rpcMetadata(r.instance, r.method)
	if err != nil {
		return nil, nil, err
	}
	in, out, signature, err := rpcSchema(method, types, r.conn.frameLimit())
	if err != nil {
		return nil, nil, err
	}
	if r.signature != "" && signature != r.signature {
		return nil, nil, fmt.Errorf("%w: %s#%s", ErrRPCSignatureChanged, r.instance, r.method)
	}
	input, err := compileRPCRecord(reflect.TypeFor[I](), reflect.TypeFor[I](), in, types)
	if err != nil {
		return nil, nil, fmt.Errorf("RPC input: %w", err)
	}
	output, err := compileRPCRecord(reflect.TypeFor[O](), reflect.TypeFor[I](), out, types)
	if err != nil {
		return nil, nil, fmt.Errorf("RPC output: %w", err)
	}
	r.signature = signature
	r.input = input
	r.output = output
	r.epoch = epoch
	return input, output, nil
}

func (r *RPC[I, O]) Call(ctx context.Context, input I) (O, error) {
	var zero O
	if ctx == nil {
		return zero, fmt.Errorf("nil RPC context")
	}
	if r == nil || r.conn == nil {
		return zero, ErrNotConnected
	}
	// Bound the whole call, including generation and shared-acquisition waits.
	ctx, cancel := context.WithTimeout(ctx, r.conn.requestTimeout())
	defer cancel()
	if err := r.conn.beginOperationContext(ctx); err != nil {
		return zero, err
	}
	defer r.conn.endOperation()
	in, out, err := r.binding()
	if err != nil {
		return zero, err
	}
	value := reflect.ValueOf(input)
	inputSizes, inputSize, err := in.sizes(value, true, r.conn.frameLimit())
	if err != nil {
		return zero, err
	}
	outputSizes, outputSize, err := out.sizes(value, false, r.conn.frameLimit())
	if err != nil {
		return zero, err
	}
	data := make([]byte, inputSize)
	if err = in.encode(value, inputSizes, data); err != nil {
		return zero, err
	}
	handle, err := r.conn.acquireNamedHandle(ctx, r.instance+"#"+r.method, true)
	if err != nil {
		return zero, err
	}
	response, err := r.conn.writeReadContext(ctx, uint32(GroupSymbolValueByHandle), handle, uint32(outputSize), data, true)
	if err != nil {
		return zero, fmt.Errorf("RPC %s#%s: %w", r.instance, r.method, err)
	}
	var result O
	if err = out.decode(reflect.ValueOf(&result).Elem(), outputSizes, response); err != nil {
		return zero, err
	}
	return result, nil
}

// RPCMethods returns method metadata for a function-block instance. Treat the
// result as immutable, like uploaded symbol/datatype metadata.
func (conn *Connection) RPCMethods(instance string) ([]RPCMethod, error) {
	conn.generationLock.RLock()
	defer conn.generationLock.RUnlock()
	methods, _, err := conn.rpcMethods(instance)
	return methods, err
}
func (conn *Connection) rpcMethods(instance string) ([]RPCMethod, map[string]SymbolUploadDataType, error) {
	conn.symbolLock.Lock()
	defer conn.symbolLock.Unlock()
	symbol, ok := conn.symbols[instance]
	if !ok {
		return nil, nil, fmt.Errorf("RPC instance %s not found", instance)
	}
	name := symbol.DataType
	for range 64 {
		dt, ok := conn.datatypes[name]
		if !ok {
			break
		}
		if len(dt.Methods) > 0 {
			return dt.Methods, conn.datatypes, nil
		}
		if dt.DataType == name {
			break
		}
		name = dt.DataType
	}
	return nil, nil, fmt.Errorf("no RPC methods on %s", instance)
}
func (conn *Connection) rpcMetadata(instance, name string) (RPCMethod, map[string]SymbolUploadDataType, error) {
	methods, types, err := conn.rpcMethods(instance)
	if err != nil {
		return RPCMethod{}, nil, err
	}
	for _, m := range methods {
		if m.Name == name {
			return m, types, nil
		}
	}
	return RPCMethod{}, nil, fmt.Errorf("RPC method %s#%s not found", instance, name)
}

const rpcRecordType = "$rpc-record"

// ADS transmits parameter values contiguously in declaration order, with the
// return value preceding outputs. AlignSize describes the PLC ABI, not padding
// between ADS values. Padding inside each value comes from datatype metadata.
func rpcSchema(m RPCMethod, types map[string]SymbolUploadDataType, limit uint32) (*Symbol, *Symbol, string, error) {
	reject := func(param, reason string) (*Symbol, *Symbol, string, error) {
		return nil, nil, "", &UnsupportedRPCError{m.Name, param, reason}
	}
	if m.Flags&4 != 0 || m.Flags & ^uint32(1|2|8) != 0 {
		return reject("", "method flags do not describe a callable PLC method")
	}
	if hasPackingAttribute(m.Attributes) {
		return reject("", "custom RPC packing is not supported")
	}
	in := &Symbol{FullName: m.Name + ".Input", DataType: rpcRecordType, Children: make(map[string]*Symbol)}
	out := &Symbol{FullName: m.Name + ".Result", DataType: rpcRecordType, Children: make(map[string]*Symbol)}
	budget := maxMetadataNodes
	add := func(record *Symbol, name, dt, comment string, size, align uint32) error {
		if size == 0 {
			return fmt.Errorf("zero-sized RPC value %s", name)
		}
		if align != 0 && (align > 8 || align&(align-1) != 0) {
			return fmt.Errorf("unsupported RPC alignment %d", align)
		}
		if uint64(record.Length)+uint64(size)+48 > uint64(limit) {
			return fmt.Errorf("RPC payload exceeds frame limit")
		}
		if _, exists := record.Children[name]; exists {
			return fmt.Errorf("duplicate RPC field %s", name)
		}
		value := &Symbol{FullName: record.FullName + "." + name, Name: name, DataType: dt, Comment: comment, Length: size, Offset: record.Length}
		resolved := dt
		seen := make(map[string]bool)
		for depth := 0; depth < 64; depth++ {
			definition, ok := types[resolved]
			if !ok {
				break
			}
			if seen[resolved] {
				return fmt.Errorf("cyclic RPC datatype %s", dt)
			}
			seen[resolved] = true
			if definition.DatatypeEntry.Size != size {
				return fmt.Errorf("RPC size disagrees with datatype %s", resolved)
			}
			if hasPackingAttribute(definition.Attributes) {
				return fmt.Errorf("custom packing on %s is not supported for RPC", resolved)
			}
			if len(definition.Children) > 0 {
				children, err := materializeChildren(&definition, value, types, 0, &budget)
				if err != nil {
					return err
				}
				value.Children = children
				break
			}
			if definition.DataType == resolved {
				break
			}
			resolved = definition.DataType
		}
		var validate func(*Symbol) error
		validate = func(v *Symbol) error {
			if d, ok := types[v.DataType]; ok && hasPackingAttribute(d.Attributes) {
				return fmt.Errorf("custom packing on %s is not supported for RPC", v.DataType)
			}
			if len(v.Children) > 0 {
				for _, c := range v.Children {
					if err := validate(c); err != nil {
						return err
					}
				}
				return nil
			}
			base, err := resolveDataType(v.DataType, types)
			if err != nil {
				return err
			}
			info, ok := Registry.GetByADSType(base)
			if !ok || v.Length == 0 || (info.Size != 0 && uint32(info.Size) != v.Length) {
				return fmt.Errorf("invalid RPC value width for %s", v.DataType)
			}
			return nil
		}
		if err := validate(value); err != nil {
			return err
		}
		record.Children[name] = value
		record.Length += size
		return nil
	}
	if m.ReturnSize == 0 && m.ReturnType != "" && !strings.EqualFold(m.ReturnType, "VOID") {
		return reject("$return", "non-void return has zero size")
	}
	if m.ReturnSize > 0 {
		if err := add(out, "$return", m.ReturnType, "PLC return value", m.ReturnSize, m.ReturnAlignSize); err != nil {
			return reject("$return", err.Error())
		}
	}
	for i, p := range m.Parameters {
		if p.Flags & ^(RPCIn|RPCOut|RPCByReference|RPCParameterAttributes) != 0 || p.Flags&(RPCIn|RPCOut) == 0 {
			return reject(p.Name, "missing direction, array-dimension or unknown parameter flags")
		}
		dt, size, length, err := rpcParameterValue(m, i, types)
		if err != nil {
			return reject(p.Name, err.Error())
		}
		if p.Flags&RPCIn != 0 {
			if err := add(in, p.Name, dt, p.Comment, size, p.AlignSize); err != nil {
				return reject(p.Name, err.Error())
			}
			in.Children[p.Name].rpcLength = length
		}
		if p.Flags&RPCOut != 0 {
			if err := add(out, p.Name, dt, p.Comment, size, p.AlignSize); err != nil {
				return reject(p.Name, err.Error())
			}
			out.Children[p.Name].rpcLength = length
		}
	}
	hash := sha256.New()
	signatureMethod := m
	signatureMethod.Comment = ""
	signatureMethod.Version = 0
	signatureMethod.VTableIndex = 0
	signatureMethod.ReturnTypeGUID = [16]byte{}
	signatureMethod.ReturnADSDataType = 0
	signatureMethod.Attributes = nil
	signatureMethod.Flags &^= 8
	signatureMethod.Parameters = append([]RPCParameter(nil), m.Parameters...)
	for i := range signatureMethod.Parameters {
		p := &signatureMethod.Parameters[i]
		p.Comment = ""
		p.ADSDataType = 0
		p.TypeGUID = [16]byte{}
		p.Attributes = nil
		p.Flags &^= RPCParameterAttributes
	}
	raw, _ := json.Marshal(signatureMethod)
	hash.Write(raw)
	var visit func(*Symbol)
	visit = func(s *Symbol) {
		fmt.Fprintf(hash, "%q:%q:%d:%d;", s.Name, s.DataType, s.Offset, s.Length)
		if s.rpcLength != "" {
			fmt.Fprintf(hash, "length:%q;", s.rpcLength)
		}
		name := s.DataType
		for range 64 {
			dt, ok := types[name]
			if !ok {
				break
			}
			if len(dt.EnumMembers) > 0 {
				members := append([]EnumMember(nil), dt.EnumMembers...)
				sort.Slice(members, func(i, j int) bool { return members[i].Name < members[j].Name })
				raw, _ := json.Marshal(members)
				hash.Write(raw)
				break
			}
			if dt.DataType == name {
				break
			}
			name = dt.DataType
		}
		for _, c := range getFieldsByOffset(s) {
			visit(c)
		}
	}
	visit(in)
	visit(out)
	return in, out, hex.EncodeToString(hash.Sum(nil)), nil
}
func hasLengthAttribute(attrs []Attribute) bool {
	for _, a := range attrs {
		if strings.EqualFold(a.Name, "TcRpcLengthIs") {
			return true
		}
	}
	return false
}
func hasPackingAttribute(attrs []Attribute) bool {
	for _, a := range attrs {
		if strings.EqualFold(a.Name, "pack_mode") {
			return true
		}
	}
	return false
}
