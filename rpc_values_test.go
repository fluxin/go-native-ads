package ads

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func rpcBufferMethod() RPCMethod {
	return RPCMethod{Name: "Buffers", Flags: 1, ReturnType: "DINT", ReturnSize: 4, ReturnAlignSize: 4, Parameters: []RPCParameter{
		{Name: "accepted", DataType: "BOOL", Size: 1, AlignSize: 1, Flags: RPCOut},
		{Name: "count", DataType: "UINT", Size: 2, AlignSize: 2, Flags: RPCIn},
		{Name: "values", DataType: "POINTER TO INT", Size: 8, AlignSize: 8, Flags: RPCIn | RPCOut | RPCByReference, LengthIsParameterIndex: 2},
		{Name: "value", DataType: "REFERENCE TO DINT", Size: 8, AlignSize: 8, Flags: RPCIn | RPCOut | RPCByReference},
		{Name: "bytes", DataType: "POINTER TO BYTE", Size: 8, AlignSize: 8, Flags: RPCOut | RPCByReference, LengthIsParameterIndex: 2},
		{Name: "tail", DataType: "USINT", Size: 1, AlignSize: 1, Flags: RPCOut},
	}}
}

type rpcBufferInput struct {
	Count  uint16  `ads:"count"`
	Values []int16 `ads:"values"`
	Value  int32   `ads:"value"`
}
type rpcBufferOutput struct {
	Return   int32   `ads:"$return"`
	Accepted bool    `ads:"accepted"`
	Values   []int16 `ads:"values"`
	Value    int32   `ads:"value"`
	Bytes    []byte  `ads:"bytes"`
	Tail     uint8   `ads:"tail"`
}

// Independent router contract: count followed by N INTs and one DINT. It
// validates the ADS requested lengths and produces a return plus interleaved
// fixed/dynamic outputs, without using any production layout/codec helpers.
func rpcBufferResponse(data []byte, short bool) []byte {
	bad := []byte{5, 7, 0, 0}
	if len(data) < 22 {
		return bad
	}
	n := int(binary.LittleEndian.Uint16(data[16:]))
	if len(data) != 22+2*n || binary.LittleEndian.Uint32(data[12:]) != uint32(6+2*n) || binary.LittleEndian.Uint32(data[8:]) != uint32(10+3*n) {
		return bad
	}
	out := make([]byte, 10+3*n)
	binary.LittleEndian.PutUint32(out, 12)
	out[4] = 1
	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint16(out[5+2*i:], binary.LittleEndian.Uint16(data[18+2*i:])+1)
	}
	binary.LittleEndian.PutUint32(out[5+2*n:], binary.LittleEndian.Uint32(data[18+2*n:])+1)
	for i := 0; i < n; i++ {
		out[9+2*n+i] = byte(i)
	}
	out[len(out)-1] = 99
	if short {
		out = out[:len(out)-1]
	}
	return fakePayload(out)
}

func bufferSchema(t *testing.T, m RPCMethod, types map[string]SymbolUploadDataType) (*rpcRecordCodec, *rpcRecordCodec, string) {
	t.Helper()
	in, out, sig, err := rpcSchema(m, types, defaultMaxFrameSize)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := compileRPCRecord(reflect.TypeFor[rpcBufferInput](), reflect.TypeFor[rpcBufferInput](), in, types)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := compileRPCRecord(reflect.TypeFor[rpcBufferOutput](), reflect.TypeFor[rpcBufferInput](), out, types)
	if err != nil {
		t.Fatal(err)
	}
	return enc, dec, sig
}
func TestRPCReferenceAndBufferWire(t *testing.T) {
	for _, width := range []uint32{4, 8} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := rpcBufferMethod()
			for _, i := range []int{2, 3, 4} {
				m.Parameters[i].Size = width
				m.Parameters[i].AlignSize = width
			}
			enc, dec, _ := bufferSchema(t, m, nil)
			input := rpcBufferInput{2, []int16{-2, 0x1234}, 0x11223344}
			v := reflect.ValueOf(input)
			sizes, n, err := enc.sizes(v, true, defaultMaxFrameSize)
			if err != nil {
				t.Fatal(err)
			}
			data := make([]byte, n)
			if err = enc.encode(v, sizes, data); err != nil {
				t.Fatal(err)
			}
			want := []byte{2, 0, 254, 255, 0x34, 0x12, 0x44, 0x33, 0x22, 0x11}
			if !bytes.Equal(data, want) {
				t.Fatalf("pointer width leaked into wire: %x", data)
			}
			outputSizes, n, err := dec.sizes(v, false, defaultMaxFrameSize)
			if err != nil || n != 16 {
				t.Fatalf("output size %d %v", n, err)
			}
			var got rpcBufferOutput
			wire := []byte{12, 0, 0, 0, 1, 255, 255, 0x35, 0x12, 0x45, 0x33, 0x22, 0x11, 0, 1, 99}
			if err = dec.decode(reflect.ValueOf(&got).Elem(), outputSizes, wire); err != nil {
				t.Fatal(err)
			}
			expected := rpcBufferOutput{12, true, []int16{-1, 0x1235}, 0x11223345, []byte{0, 1}, 99}
			if !reflect.DeepEqual(got, expected) {
				t.Fatalf("got %+v", got)
			}
			got.Values[0] = 123
			if input.Values[0] != -2 {
				t.Fatal("output aliases caller input")
			}
			if err = dec.decode(reflect.ValueOf(&got).Elem(), outputSizes, append(wire, 0)); err == nil {
				t.Fatal("accepted trailing response bytes")
			}
		})
	}
}
func TestRPCBufferEmptyAndLimits(t *testing.T) {
	enc, dec, _ := bufferSchema(t, rpcBufferMethod(), nil)
	for _, values := range [][]int16{nil, {}} {
		v := reflect.ValueOf(rpcBufferInput{Values: values})
		sizes, n, err := enc.sizes(v, true, defaultMaxFrameSize)
		if err != nil || n != 6 {
			t.Fatalf("empty input %d %v", n, err)
		}
		if err = enc.encode(v, sizes, make([]byte, n)); err != nil {
			t.Fatal(err)
		}
		sizes, n, err = dec.sizes(v, false, defaultMaxFrameSize)
		if err != nil || n != 10 {
			t.Fatal(n, err)
		}
		var output rpcBufferOutput
		if err = dec.decode(reflect.ValueOf(&output).Elem(), sizes, make([]byte, n)); err != nil || len(output.Values) != 0 || len(output.Bytes) != 0 {
			t.Fatal(output, err)
		}
	}
	v := reflect.ValueOf(rpcBufferInput{Count: 2, Values: []int16{1}})
	if _, _, err := enc.sizes(v, true, defaultMaxFrameSize); err == nil {
		t.Fatal("accepted mismatched count")
	}
	v = reflect.ValueOf(rpcBufferInput{Count: 2, Values: []int16{1, 2}})
	if _, _, err := enc.sizes(v, true, 57); err == nil {
		t.Fatal("accepted oversized request")
	}
	if _, _, err := dec.sizes(v, false, 55); err == nil {
		t.Fatal("accepted oversized response")
	}
	for _, test := range []struct {
		typ   string
		value any
	}{
		{"LINT", struct {
			Count int64 `ads:"count"`
		}{-1}},
		{"ULINT", struct {
			Count uint64 `ads:"count"`
		}{math.MaxUint64}},
	} {
		m := RPCMethod{Name: "Read", Flags: 1, Parameters: []RPCParameter{{Name: "count", DataType: test.typ, Size: 8, Flags: RPCIn}, {Name: "data", DataType: "POINTER TO INT", Size: 8, Flags: RPCOut | RPCByReference, LengthIsParameterIndex: 1}}}
		_, out, _, err := rpcSchema(m, nil, defaultMaxFrameSize)
		if err != nil {
			t.Fatal(err)
		}
		type result struct {
			Data []int16 `ads:"data"`
		}
		codec, err := compileRPCRecord(reflect.TypeFor[result](), reflect.TypeOf(test.value), out, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = codec.sizes(reflect.ValueOf(test.value), false, defaultMaxFrameSize); err == nil {
			t.Fatalf("accepted count %+v", test.value)
		}
	}
}
func TestRPCReferenceAliasesAndLengthMetadata(t *testing.T) {
	m := rpcBufferMethod()
	baseline := m
	_, _, a := bufferSchema(t, m, nil)
	m.Parameters = append([]RPCParameter(nil), m.Parameters...)
	m.Parameters[2].LengthIsParameterIndex = 0
	m.Parameters[2].Attributes = []Attribute{{"TcRpcLengthIs", "count"}}
	m.Parameters[2].Flags |= RPCParameterAttributes
	_, _, b := bufferSchema(t, m, nil)
	// Attribute-only links must participate in signature hashing.
	m.Parameters[1].Name = "length"
	m.Parameters[2].Attributes[0].Value = "length"
	_, _, c, err := rpcSchema(m, nil, defaultMaxFrameSize)
	if err != nil || c == b {
		t.Fatal("length semantic change missed", err)
	}
	m = baseline
	m.Parameters = append([]RPCParameter(nil), baseline.Parameters...)
	types := map[string]SymbolUploadDataType{"IntBuffer": {Name: "IntBuffer", DataType: "POINTER TO INT", DatatypeEntry: datatypeEntry{Size: 8}}, "RefValue": {Name: "RefValue", DataType: "REFERENCE TO DINT", DatatypeEntry: datatypeEntry{Size: 8}}}
	m.Parameters[2].DataType = "IntBuffer"
	m.Parameters[3].DataType = "RefValue"
	bufferSchema(t, m, types)
	// A server may report the referenced value directly with ByReference.
	m = rpcBufferMethod()
	m.Parameters[2].DataType = "INT"
	m.Parameters[2].Size = 2
	m.Parameters[3].DataType = "DINT"
	m.Parameters[3].Size = 4
	bufferSchema(t, m, nil)
	if a == "" {
		t.Fatal("missing signature")
	}
}
func TestRPCBufferMetadataRejections(t *testing.T) {
	cases := map[string]func(*RPCMethod){
		"out count":             func(m *RPCMethod) { m.Parameters[1].Flags = RPCOut },
		"inout count":           func(m *RPCMethod) { m.Parameters[1].Flags = RPCIn | RPCOut },
		"float count":           func(m *RPCMethod) { m.Parameters[1].DataType = "REAL"; m.Parameters[1].Size = 4 },
		"out of range":          func(m *RPCMethod) { m.Parameters[2].LengthIsParameterIndex = 99 },
		"self link":             func(m *RPCMethod) { m.Parameters[2].LengthIsParameterIndex = 3 },
		"conflicting attribute": func(m *RPCMethod) { m.Parameters[2].Attributes = []Attribute{{"TcRpcLengthIs", "value"}} },
		"unknown name": func(m *RPCMethod) {
			m.Parameters[2].LengthIsParameterIndex = 0
			m.Parameters[2].Attributes = []Attribute{{"TcRpcLengthIs", "missing"}}
		},
		"no bound":       func(m *RPCMethod) { m.Parameters[2].LengthIsParameterIndex = 0 },
		"nested pointer": func(m *RPCMethod) { m.Parameters[2].DataType = "POINTER TO POINTER TO INT" },
		"unknown target": func(m *RPCMethod) { m.Parameters[2].DataType = "POINTER TO Missing" },
		"array flags":    func(m *RPCMethod) { m.Parameters[2].Flags |= 0x10 },
		"outptr flag":    func(m *RPCMethod) { m.Parameters[2].Flags |= 0x8 },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			m := rpcBufferMethod()
			change(&m)
			_, _, _, err := rpcSchema(m, nil, defaultMaxFrameSize)
			var unsupported *UnsupportedRPCError
			if !errors.As(err, &unsupported) {
				t.Fatalf("expected typed rejection, got %v", err)
			}
		})
	}
}
func TestRPCBuffersTransportAndCodegen(t *testing.T) {
	router := newFakeRouter(t)
	router.rpc.Store(true)
	router.rpcBuffers.Store(true)
	conn := router.connect(t, true)
	binding, err := conn.BindRPC[rpcBufferInput, rpcBufferOutput]("MAIN.rpc", "Buffers")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = binding.Call(t.Context(), rpcBufferInput{Count: 2, Values: []int16{1}}); err == nil {
		t.Fatal("accepted bad length")
	}
	if router.handles.Load() != 0 || router.rpcCalls.Load() != 0 {
		t.Fatal("invalid input performed network work")
	}
	input := rpcBufferInput{2, []int16{-2, 0x1234}, 10}
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			got, err := binding.Call(t.Context(), input)
			if err != nil || got.Value != 11 || !reflect.DeepEqual(got.Values, []int16{-1, 0x1235}) || got.Tail != 99 {
				t.Errorf("result %+v %v", got, err)
			}
		})
	}
	wg.Wait()
	if router.handles.Load() != 1 {
		t.Fatal("buffer calls did not share handle")
	}
	if input.Value != 10 || input.Values[0] != -2 {
		t.Fatal("input was mutated")
	}
	router.rpcShortReply.Store(true)
	got, err := binding.Call(t.Context(), input)
	if err == nil || !reflect.DeepEqual(got, rpcBufferOutput{}) {
		t.Fatal("partial output escaped", got, err)
	}
	router.rpcShortReply.Store(false)
	old := conn.CurrentEpoch()
	router.generation.Add(1)
	router.disconnect()
	waitRPCEpoch(t, conn, old)
	if _, err = binding.Call(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	code, err := conn.GenerateRPCClient("MAIN.rpc", "BuffersClient")
	if err != nil {
		t.Fatal(err)
	}
	source, err := FormatGeneratedCode([]string{code}, "generated")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(source, "[]ads.Int16") || !strings.Contains(source, "[]ads.Uint8") {
		t.Fatal(source)
	}
	_, port, _ := net.SplitHostPort(router.listener.Addr().String())
	test := fmt.Sprintf(`package generated
import("testing"; ads "github.com/fluxin/go-native-ads")
func TestBuffers(t *testing.T){
 conn,err:=ads.NewConnection(t.Context(),ads.ConnectionOptions{IP:"127.0.0.1",Port:%s,AMSPort:851,Transport:ads.ConnectionTransportTCP});if err!=nil{t.Fatal(err)};defer conn.Close()
 if err=conn.Connect();err!=nil{t.Fatal(err)}
 c,err:=NewBuffersClient(conn,"MAIN.rpc");if err!=nil{t.Fatal(err)}
 r,err:=c.Buffers(t.Context(),BuffersClientBuffersInput{Count:2,Values:[]int16{5,7},Value:10})
 if err!=nil||r.ReturnValue!=12||r.Values[0]!=6||r.Values[1]!=8||r.Value!=11||len(r.Bytes)!=2||r.Tail!=99{t.Fatalf("%%+v %%v",r,err)}
}
`, port)
	compileRPCSource(t, source, test)
	old = conn.CurrentEpoch()
	router.rpcChanged.Store(true)
	router.generation.Add(1)
	router.disconnect()
	waitRPCEpoch(t, conn, old)
	before := router.rpcCalls.Load()
	if _, err = binding.Call(t.Context(), input); !errors.Is(err, ErrRPCSignatureChanged) {
		t.Fatal("accepted changed metadata")
	}
	if router.rpcCalls.Load() != before {
		t.Fatal("sent incompatible call")
	}
}

func TestRPCReferenceCompoundCodecs(t *testing.T) {
	const arr = "ARRAY [-1..0] OF INT"
	types, err := parseUploadSymbolInfoDataTypes(datatypeWire(arr, "INT", 4, []datatypeArrayInfo{{LBound: -1, Elements: 2}}))
	if err != nil {
		t.Fatal(err)
	}
	types["E_Mode"] = SymbolUploadDataType{Name: "E_Mode", DataType: "INT", DatatypeEntry: datatypeEntry{Size: 2}, EnumMembers: []EnumMember{{Name: "Run", Value: 1}}}
	types["Payload"] = SymbolUploadDataType{Name: "Payload", DatatypeEntry: datatypeEntry{Size: 8}, Children: map[string]*SymbolUploadDataType{
		"mode":    {Name: "mode", DataType: "E_Mode", DatatypeEntry: datatypeEntry{Size: 2}},
		"samples": {Name: "samples", DataType: arr, DatatypeEntry: datatypeEntry{Size: 4, Offs: 4}},
	}}
	method := RPCMethod{Name: "Compound", Flags: 1, Parameters: []RPCParameter{
		{Name: "count", DataType: "UINT", Size: 2, Flags: RPCIn},
		{Name: "items", DataType: "POINTER TO Payload", Size: 4, Flags: RPCIn | RPCOut | RPCByReference, LengthIsParameterIndex: 1},
		{Name: "fixed", DataType: "REFERENCE TO " + arr, Size: 8, Flags: RPCIn | RPCOut | RPCByReference},
		{Name: "text", DataType: "REFERENCE TO STRING(5)", Size: 4, Flags: RPCIn | RPCOut | RPCByReference},
	}}
	type payload struct {
		Mode    int16    `ads:"mode"`
		Samples [2]int16 `ads:"samples"`
	}
	type input struct {
		Count uint16    `ads:"count"`
		Items []payload `ads:"items"`
		Fixed [2]int16  `ads:"fixed"`
		Text  string    `ads:"text"`
	}
	type output struct {
		Items []payload `ads:"items"`
		Fixed [2]int16  `ads:"fixed"`
		Text  string    `ads:"text"`
	}
	in, out, sig, err := rpcSchema(method, types, defaultMaxFrameSize)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := compileRPCRecord(reflect.TypeFor[input](), reflect.TypeFor[input](), in, types)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := compileRPCRecord(reflect.TypeFor[output](), reflect.TypeFor[input](), out, types)
	if err != nil {
		t.Fatal(err)
	}
	value := input{2, []payload{{1, [2]int16{-1, 2}}, {1, [2]int16{3, 4}}}, [2]int16{5, 6}, "hello"}
	v := reflect.ValueOf(value)
	sizes, n, err := enc.sizes(v, true, defaultMaxFrameSize)
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, n)
	if err = enc.encode(v, sizes, data); err != nil {
		t.Fatal(err)
	}
	want := []byte{2, 0, 1, 0, 0, 0, 255, 255, 2, 0, 1, 0, 0, 0, 3, 0, 4, 0, 5, 0, 6, 0, 'h', 'e', 'l', 'l', 'o', 0}
	if !bytes.Equal(data, want) {
		t.Fatalf("compound wire %x", data)
	}
	sizes, _, err = dec.sizes(v, false, defaultMaxFrameSize)
	if err != nil {
		t.Fatal(err)
	}
	var got output
	if err = dec.decode(reflect.ValueOf(&got).Elem(), sizes, data[2:]); err != nil || !reflect.DeepEqual(got, output{value.Items, value.Fixed, value.Text}) {
		t.Fatal(got, err)
	}
	types["FB_RPC"] = SymbolUploadDataType{Name: "FB_RPC", Methods: []RPCMethod{method}}
	conn := &Connection{datatypes: types, symbols: map[string]*Symbol{"MAIN.rpc": {DataType: "FB_RPC"}}}
	code, err := conn.GenerateRPCClient("MAIN.rpc", "CompoundClient")
	if err != nil {
		t.Fatal(err)
	}
	source, err := FormatGeneratedCode([]string{code}, "generated")
	if err != nil {
		t.Fatal(err)
	}
	compileRPCSource(t, source)
	types["Payload"].Children["samples"].DatatypeEntry.Offs = 2
	_, _, changed, err := rpcSchema(method, types, defaultMaxFrameSize)
	if err != nil || changed == sig {
		t.Fatal("pointee layout change missed", err)
	}
}

func TestRPCBufferRejectsWrongGoType(t *testing.T) {
	in, _, _, err := rpcSchema(rpcBufferMethod(), nil, defaultMaxFrameSize)
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range []reflect.Type{
		reflect.TypeFor[struct {
			Count  uint16   `ads:"count"`
			Values [2]int16 `ads:"values"`
			Value  int32    `ads:"value"`
		}](),
		reflect.TypeFor[struct {
			Count  uint16  `ads:"count"`
			Values []int32 `ads:"values"`
			Value  int32   `ads:"value"`
		}](),
		reflect.TypeFor[struct {
			Count  uint16  `ads:"count"`
			Values []int16 `ads:"values"`
			Value  *int32  `ads:"value"`
		}](),
	} {
		if _, err = compileRPCRecord(typ, typ, in, nil); err == nil {
			t.Fatalf("accepted %s", typ)
		}
	}
}

func FuzzRPCBufferCodec(f *testing.F) {
	f.Add(uint16(2), []byte{1, 0, 255, 255})
	f.Add(uint16(0), []byte{})
	f.Fuzz(func(t *testing.T, n uint16, raw []byte) {
		enc, _, _ := bufferSchema(t, rpcBufferMethod(), nil)
		count := len(raw) / 2
		if count > 1024 {
			count = 1024
		}
		input := rpcBufferInput{Count: n, Values: make([]int16, count), Value: 17}
		for i := range input.Values {
			input.Values[i] = int16(binary.LittleEndian.Uint16(raw[i*2:]))
		}
		v := reflect.ValueOf(input)
		sizes, size, err := enc.sizes(v, true, 4096)
		if int(n) != count || 6+2*count+48 > 4096 {
			if err == nil {
				t.Fatal("accepted invalid buffer")
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		data := make([]byte, size)
		if err = enc.encode(v, sizes, data); err != nil {
			t.Fatal(err)
		}
		var output rpcBufferInput
		if err = enc.decode(reflect.ValueOf(&output).Elem(), sizes, data); err != nil || !reflect.DeepEqual(input, output) {
			t.Fatal("roundtrip", err)
		}
	})
}

func rpcReferenceSmokeMethods() []RPCMethod {
	return []RPCMethod{
		{Name: "Increment", Flags: 1, Parameters: []RPCParameter{{Name: "value", DataType: "DINT", Size: 4, AlignSize: 4, Flags: RPCIn | RPCOut | RPCByReference}}},
		{Name: "SumBuffer", Flags: 1, ReturnType: "DINT", ReturnSize: 4, ReturnAlignSize: 4, Parameters: []RPCParameter{
			{Name: "count", DataType: "UINT", Size: 2, AlignSize: 2, Flags: RPCIn},
			{Name: "values", DataType: "POINTER TO INT", Size: 8, AlignSize: 8, Flags: RPCIn | RPCByReference, LengthIsParameterIndex: 1},
		}},
		{Name: "FillBuffer", Flags: 1, Parameters: []RPCParameter{
			{Name: "count", DataType: "UINT", Size: 2, AlignSize: 2, Flags: RPCIn},
			{Name: "values", DataType: "POINTER TO INT", Size: 8, AlignSize: 8, Flags: RPCOut | RPCByReference, LengthIsParameterIndex: 1},
		}},
	}
}

func TestRPCReferenceMetadataForms(t *testing.T) {
	types := map[string]SymbolUploadDataType{
		"UploadedRef": {Name: "UploadedRef", DataType: "DINT", DatatypeEntry: datatypeEntry{Size: 8, Flags: datatypeFlagReferenceTo}},
		"Text":        {Name: "Text", DataType: "STRING", DatatypeEntry: datatypeEntry{Size: 6}},
	}
	m := rpcBufferMethod()
	m.Parameters[3].DataType = "UploadedRef"
	bufferSchema(t, m, types)
	m.Parameters[4].DataType = "PVOID"
	bufferSchema(t, m, types)
	m.Parameters[3].DataType = "REFERENCE TO Text"
	in, _, _, err := rpcSchema(m, types, defaultMaxFrameSize)
	if err != nil {
		t.Fatal(err)
	}
	if in.Children["value"].Length != 6 {
		t.Fatal("lost string alias width")
	}
	types["Nested"] = SymbolUploadDataType{Name: "Nested", DataType: "UploadedRef", DatatypeEntry: datatypeEntry{Size: 8, Flags: datatypeFlagReferenceTo}}
	m.Parameters[3].DataType = "Nested"
	if _, _, _, err = rpcSchema(m, types, defaultMaxFrameSize); err == nil {
		t.Fatal("accepted nested reference alias")
	}
}
