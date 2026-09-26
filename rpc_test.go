package ads

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func rpcTestMethod() RPCMethod {
	return RPCMethod{Name: "Mixed", Version: 1, Flags: 1, ReturnType: "DINT", ReturnSize: 4, ReturnAlignSize: 4, Parameters: []RPCParameter{
		{Name: "small", DataType: "SINT", Size: 1, AlignSize: 1, Flags: RPCIn},
		{Name: "large", DataType: "DINT", Size: 4, AlignSize: 4, Flags: RPCIn},
		{Name: "accepted", DataType: "BOOL", Size: 1, AlignSize: 1, Flags: RPCOut},
		{Name: "value", DataType: "INT", Size: 2, AlignSize: 2, Flags: RPCIn | RPCOut},
	}}
}
func rpcAttributeWire(attrs []Attribute) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, uint16(len(attrs)))
	for _, a := range attrs {
		b.WriteByte(byte(len(a.Name)))
		b.WriteByte(byte(len(a.Value)))
		b.WriteString(a.Name + "\x00" + a.Value + "\x00")
	}
	return b.Bytes()
}

// Synthetic protocol fixtures use explicit header offsets rather than
// serializing parser structs. Live captured fixtures remain a validation gate.
func rpcMethodWire(m RPCMethod) []byte {
	b := make([]byte, 56)
	for offset, value := range map[int]uint32{4: m.Version, 8: m.VTableIndex, 12: m.ReturnSize, 16: m.ReturnAlignSize, 40: m.ReturnADSDataType, 44: m.Flags} {
		binary.LittleEndian.PutUint32(b[offset:], value)
	}
	copy(b[24:40], m.ReturnTypeGUID[:])
	for offset, value := range map[int]uint16{48: uint16(len(m.Name)), 50: uint16(len(m.ReturnType)), 52: uint16(len(m.Comment)), 54: uint16(len(m.Parameters))} {
		binary.LittleEndian.PutUint16(b[offset:], value)
	}
	b = append(b, []byte(m.Name+"\x00"+m.ReturnType+"\x00"+m.Comment+"\x00")...)
	for _, p := range m.Parameters {
		data := make([]byte, 48)
		for offset, value := range map[int]uint32{4: p.Size, 8: p.AlignSize, 12: p.ADSDataType, 16: uint32(p.Flags)} {
			binary.LittleEndian.PutUint32(data[offset:], value)
		}
		copy(data[24:40], p.TypeGUID[:])
		for offset, value := range map[int]uint16{40: p.LengthIsParameterIndex, 42: uint16(len(p.Name)), 44: uint16(len(p.DataType)), 46: uint16(len(p.Comment))} {
			binary.LittleEndian.PutUint16(data[offset:], value)
		}
		data = append(data, []byte(p.Name+"\x00"+p.DataType+"\x00"+p.Comment+"\x00")...)
		if p.Flags&RPCParameterAttributes != 0 {
			data = append(data, rpcAttributeWire(p.Attributes)...)
		}
		binary.LittleEndian.PutUint32(data, uint32(len(data)))
		b = append(b, data...)
	}
	if m.Flags&8 != 0 {
		b = append(b, rpcAttributeWire(m.Attributes)...)
	}
	binary.LittleEndian.PutUint32(b, uint32(len(b)))
	return b
}
func rpcDatatypeWire(methods ...RPCMethod) []byte {
	b := datatypeWire("FB_RPC", "", 1, nil)
	binary.LittleEndian.PutUint32(b[28:], datatypeFlagMethodInfos)
	var count [2]byte
	binary.LittleEndian.PutUint16(count[:], uint16(len(methods)))
	b = append(b, count[:]...)
	for _, m := range methods {
		b = append(b, rpcMethodWire(m)...)
	}
	binary.LittleEndian.PutUint32(b, uint32(len(b)))
	return b
}
func TestRPCMetadataAndBounds(t *testing.T) {
	m := rpcTestMethod()
	m.Flags |= 8
	m.Comment = "mixed width method"
	m.Attributes = []Attribute{{"author", "test"}}
	m.ReturnTypeGUID[0] = 42
	m.Parameters[0].Flags |= RPCParameterAttributes
	m.Parameters[0].Attributes = []Attribute{{"unit", "count"}}
	m.Parameters[0].TypeGUID[15] = 99
	data := rpcDatatypeWire(m)
	types, err := parseUploadSymbolInfoDataTypes(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(types["FB_RPC"].Methods, []RPCMethod{m}) {
		t.Fatalf("lost metadata: %+v", types["FB_RPC"].Methods)
	}
	for end := 1; end < len(data); end++ {
		if _, err := parseUploadSymbolInfoDataTypes(data[:end]); err == nil {
			t.Fatalf("accepted truncated upload at %d", end)
		}
	}
	bad := append([]byte(nil), data...)
	binary.LittleEndian.PutUint32(bad[53:], 0)
	if _, err := parseUploadSymbolInfoDataTypes(bad); err == nil {
		t.Fatal("accepted zero-length method")
	}
	budget := 0
	if _, err := parseRPCMethods(bytes.NewBuffer(append([]byte{1, 0}, rpcMethodWire(m)...)), &budget); err == nil {
		t.Fatal("ignored node budget")
	}
}
func FuzzRPCMetadata(f *testing.F) {
	f.Add(rpcDatatypeWire(rpcTestMethod()))
	f.Add(rpcDatatypeWire(RPCMethod{Name: "Ping", Version: 1, Flags: 1}))
	f.Fuzz(func(t *testing.T, data []byte) { _, _ = parseUploadSymbolInfoDataTypes(data) })
}

type rpcInput struct {
	Small int8  `ads:"small"`
	Large int32 `ads:"large"`
	Value int16 `ads:"value"`
}
type rpcOutput struct {
	Return   int32 `ads:"$return"`
	Accepted bool  `ads:"accepted"`
	Value    int16 `ads:"value"`
}

func TestRPCWireLayout(t *testing.T) {
	in, out, _, err := rpcSchema(rpcTestMethod(), nil, defaultMaxFrameSize)
	if err != nil {
		t.Fatal(err)
	}
	if in.Length != 7 || out.Length != 7 {
		t.Fatalf("wrong wire sizes %d %d", in.Length, out.Length)
	}
	input, err := codecFor(reflect.TypeFor[rpcInput](), in, nil)
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 7)
	if err = input.encode(reflect.ValueOf(rpcInput{2, 0x11223344, 6}), b); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, []byte{2, 0x44, 0x33, 0x22, 0x11, 6, 0}) {
		t.Fatalf("Go alignment leaked into wire: %x", b)
	}
	output, err := codecFor(reflect.TypeFor[rpcOutput](), out, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got rpcOutput
	if err = output.decode(reflect.ValueOf(&got).Elem(), []byte{0x46, 0x33, 0x22, 0x11, 1, 7, 0}); err != nil {
		t.Fatal(err)
	}
	if got != (rpcOutput{0x11223346, true, 7}) {
		t.Fatal(got)
	}
}
func TestRPCRejectsUnsupportedSignatures(t *testing.T) {
	for _, change := range []func(*RPCMethod){
		func(m *RPCMethod) { m.Parameters[0].Flags |= 0x8 },
		func(m *RPCMethod) { m.Parameters[0].LengthIsParameterIndex = 1 },
		func(m *RPCMethod) { m.Parameters[0].DataType = "POINTER TO BYTE" },
		func(m *RPCMethod) { m.Parameters[0].AlignSize = 3 },
		func(m *RPCMethod) { m.Flags |= 4 },
		func(m *RPCMethod) { m.Parameters[0].Size = defaultMaxFrameSize },
	} {
		m := rpcTestMethod()
		change(&m)
		_, _, _, err := rpcSchema(m, nil, defaultMaxFrameSize)
		var unsupported *UnsupportedRPCError
		if !errors.As(err, &unsupported) {
			t.Fatalf("expected typed rejection, got %v", err)
		}
	}
}
func TestRPCRealTransportAndReconnect(t *testing.T) {
	router := newFakeRouter(t)
	router.rpc.Store(true)
	conn := router.connect(t, true)
	rpc, err := conn.BindRPC[rpcInput, rpcOutput]("MAIN.rpc", "Mixed")
	if err != nil {
		t.Fatal(err)
	}
	call := func() error {
		out, err := rpc.Call(t.Context(), rpcInput{2, 0x11223344, 6})
		if err != nil {
			return err
		}
		if out != (rpcOutput{0x11223346, true, 7}) {
			return fmt.Errorf("RPC result %+v", out)
		}
		return nil
	}
	var wg sync.WaitGroup
	failures := make(chan error, 16)
	for range 16 {
		wg.Go(func() {
			if err := call(); err != nil {
				failures <- err
			}
		})
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if router.handles.Load() != 1 {
		t.Fatalf("method handle not shared: %d", router.handles.Load())
	}
	old := conn.CurrentEpoch()
	router.generation.Add(1)
	router.disconnect()
	waitRPCEpoch(t, conn, old)
	if err := call(); err != nil {
		t.Fatal(err)
	}
	if router.handles.Load() != 2 {
		t.Fatalf("method handle not rebound: %d", router.handles.Load())
	}
	old = conn.CurrentEpoch()
	router.rpcChanged.Store(true)
	router.generation.Add(1)
	router.disconnect()
	waitRPCEpoch(t, conn, old)
	before := router.rpcCalls.Load()
	if _, err := rpc.Call(t.Context(), rpcInput{}); !errors.Is(err, ErrRPCSignatureChanged) {
		t.Fatalf("accepted changed signature: %v", err)
	}
	if router.rpcCalls.Load() != before {
		t.Fatal("sent incompatible method")
	}
	if _, err := conn.BindRPC[rpcInput, rpcOutput]("MAIN.rpc", "Mixed", RPCOptions{Signature: "stale"}); !errors.Is(err, ErrRPCSignatureChanged) {
		t.Fatal(err)
	}
}
func waitRPCEpoch(t *testing.T, conn *Connection, old uint64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for conn.CurrentEpoch() == old && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if conn.CurrentEpoch() == old {
		t.Fatal("no reconnect")
	}
}
func TestRPCCancellationDoesNotRetry(t *testing.T) {
	router := newFakeRouter(t)
	router.rpc.Store(true)
	conn := router.connect(t, false)
	rpc, err := conn.BindRPC[rpcInput, rpcOutput]("MAIN.rpc", "Mixed")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = rpc.Call(ctx, rpcInput{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if router.handles.Load() != 0 || router.rpcCalls.Load() != 0 {
		t.Fatal("canceled call sent data")
	}
	router.rpcNoReply.Store(true)
	ctx, cancel = context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err = rpc.Call(ctx, rpcInput{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	router.rpcNoReply.Store(false)
	if router.rpcCalls.Load() != 1 {
		t.Fatalf("call retried or not sent: %d", router.rpcCalls.Load())
	}
	if _, err = rpc.Call(t.Context(), rpcInput{}); err != nil {
		t.Fatal(err)
	}
	if router.rpcCalls.Load() != 2 {
		t.Fatal("unexpected replay")
	}
	conn.generationLock.Lock()
	ctx, cancel = context.WithTimeout(t.Context(), 10*time.Millisecond)
	_, err = rpc.Call(ctx, rpcInput{})
	cancel()
	conn.generationLock.Unlock()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
func TestRPCVoid(t *testing.T) {
	router := newFakeRouter(t)
	router.rpc.Store(true)
	conn := router.connect(t, false)
	rpc, err := conn.BindRPC[struct{}, struct{}]("MAIN.rpc", "Ping")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = rpc.Call(t.Context(), struct{}{}); err != nil {
		t.Fatal(err)
	}
}
func TestGeneratedRPCClientCompiles(t *testing.T) {
	router := newFakeRouter(t)
	router.rpc.Store(true)
	conn := router.connect(t, false)
	code, err := conn.GenerateRPCClient("MAIN.rpc", "TestClient")
	if err != nil {
		t.Fatal(err)
	}
	source, err := FormatGeneratedCode([]string{code}, "generated")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(source, "func (c *TestClient) Mixed(ctx context.Context, input TestClientMixedInput)") || !strings.Contains(source, "func (c *TestClient) Ping(ctx context.Context) error") {
		t.Fatal(source)
	}
	code2, err := conn.GenerateRPCClient("MAIN.rpc", "TestClient")
	if err != nil || code2 != code {
		t.Fatal("unstable RPC code generation")
	}
	_, port, _ := net.SplitHostPort(router.listener.Addr().String())
	integration := fmt.Sprintf(`package generated
import("testing"; ads "github.com/fluxin/go-native-ads")
func TestGeneratedCalls(t *testing.T){
 conn,err:=ads.NewConnection(t.Context(),ads.ConnectionOptions{IP:"127.0.0.1",Port:%s,AMSPort:851,Transport:ads.ConnectionTransportTCP});if err!=nil{t.Fatal(err)};defer conn.Close()
 if err=conn.Connect();err!=nil{t.Fatal(err)}
 client,err:=NewTestClient(conn,"MAIN.rpc");if err!=nil{t.Fatal(err)}
 result,err:=client.Mixed(t.Context(),TestClientMixedInput{Small:2,Large:0x11223344,Value:6})
 if err!=nil || result.ReturnValue!=0x11223346 || result.Value!=7 || !result.Accepted {t.Fatalf("result %%+v %%v",result,err)}
 if err=client.Ping(t.Context());err!=nil{t.Fatal(err)}
}
`, port)
	compileRPCSource(t, source, integration)
}
func compileRPCSource(t *testing.T, source string, tests ...string) {
	t.Helper()
	dir := t.TempDir()
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	module := fmt.Sprintf("module generated\n\ngo 1.27\nrequire github.com/fluxin/go-native-ads v0.1.0\nreplace github.com/fluxin/go-native-ads => %s\n", filepath.ToSlash(root))
	if err = os.WriteFile(filepath.Join(dir, "go.mod"), []byte(module), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "generated.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if len(tests) > 0 {
		if err = os.WriteFile(filepath.Join(dir, "generated_test.go"), []byte(tests[0]), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.CommandContext(t.Context(), "go", "test", "./...")
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated package: %v\n%s\n%s", err, output, source)
	}
}
func TestGeneratedRPCNameCollisions(t *testing.T) {
	cases := []string{
		"type X struct{}; func X() {}",
		"type X struct{}; func (*X) Call(){}; func (*X) Call(){}",
		"type X struct{ Call int }; func (*X) Call(){}",
	}
	for _, code := range cases {
		if _, err := FormatGeneratedCode([]string{code}, "test"); err == nil {
			t.Fatal("accepted collision", code)
		}
	}
}

func TestRPCCompoundTypesAndGeneratedDependencies(t *testing.T) {
	const arr = "ARRAY [-1..0] OF INT"
	types, err := parseUploadSymbolInfoDataTypes(datatypeWire(arr, "INT", 4, []datatypeArrayInfo{{LBound: -1, Elements: 2}}))
	if err != nil {
		t.Fatal(err)
	}
	types["E_Mode"] = SymbolUploadDataType{Name: "E_Mode", DataType: "INT", DatatypeEntry: datatypeEntry{Size: 2}, EnumMembers: []EnumMember{{Name: "Idle", Value: 0}, {Name: "Run", Value: 1}}}
	types["ST_Payload"] = SymbolUploadDataType{Name: "ST_Payload", DatatypeEntry: datatypeEntry{Size: 12}, Children: map[string]*SymbolUploadDataType{
		"mode":   {Name: "mode", DataType: "E_Mode", DatatypeEntry: datatypeEntry{Size: 2}},
		"values": {Name: "values", DataType: arr, DatatypeEntry: datatypeEntry{Size: 4, Offs: 2}},
		"delay":  {Name: "delay", DataType: "TIME", DatatypeEntry: datatypeEntry{Size: 4, Offs: 8}},
	}}
	types["AliasPayload"] = SymbolUploadDataType{Name: "AliasPayload", DataType: "ST_Payload", DatatypeEntry: datatypeEntry{Size: 12}}
	method := RPCMethod{Name: "Echo", Flags: 1, ReturnType: "AliasPayload", ReturnSize: 12, ReturnAlignSize: 4, Parameters: []RPCParameter{{Name: "payload", DataType: "AliasPayload", Size: 12, AlignSize: 4, Flags: RPCIn | RPCOut}}}
	types["FB_RPC"] = SymbolUploadDataType{Name: "FB_RPC", Methods: []RPCMethod{method}}
	in, out, signature, err := rpcSchema(method, types, defaultMaxFrameSize)
	if err != nil {
		t.Fatal(err)
	}
	type payload struct {
		Mode   int16         `ads:"mode"`
		Values [2]int16      `ads:"values"`
		Delay  time.Duration `ads:"delay"`
	}
	type input struct {
		Payload payload `ads:"payload"`
	}
	type output struct {
		Return  payload `ads:"$return"`
		Payload payload `ads:"payload"`
	}
	p := payload{1, [2]int16{-1, 2}, 1500 * time.Millisecond}
	enc, err := codecFor(reflect.TypeFor[input](), in, types)
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 12)
	if err = enc.encode(reflect.ValueOf(input{p}), data); err != nil {
		t.Fatal(err)
	}
	expected := []byte{1, 0, 255, 255, 2, 0, 0, 0, 0xdc, 5, 0, 0}
	if !bytes.Equal(data, expected) {
		t.Fatalf("wrong compound payload %x", data)
	}
	dec, err := codecFor(reflect.TypeFor[output](), out, types)
	if err != nil {
		t.Fatal(err)
	}
	var result output
	if err = dec.decode(reflect.ValueOf(&result).Elem(), append(data, data...)); err != nil || result != (output{p, p}) {
		t.Fatalf("compound roundtrip %+v %v", result, err)
	}
	conn := &Connection{datatypes: types, symbols: map[string]*Symbol{"MAIN.rpc": {DataType: "FB_RPC"}, "MAIN.rpc2": {DataType: "FB_RPC"}}}
	code, err := conn.GenerateRPCClients(map[string]string{"MAIN.rpc": "First", "MAIN.rpc2": "Second"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(code, "type AliasPayload struct") != 1 || strings.Count(code, "type E_Mode ") != 1 {
		t.Fatal(code)
	}
	source, err := FormatGeneratedCode([]string{code}, "generated")
	if err != nil {
		t.Fatal(err)
	}
	compileRPCSource(t, source)
	// Editing comments/table positions must not invalidate compatible clients.
	method.Comment = "new docs"
	method.VTableIndex = 17
	_, _, same, err := rpcSchema(method, types, defaultMaxFrameSize)
	if err != nil || same != signature {
		t.Fatalf("non-ABI change changed signature: %v", err)
	}
	dt := types["E_Mode"]
	dt.EnumMembers = append([]EnumMember(nil), dt.EnumMembers...)
	dt.EnumMembers[1].Value = 2
	types["E_Mode"] = dt
	_, _, changed, err := rpcSchema(method, types, defaultMaxFrameSize)
	if err != nil || changed == signature {
		t.Fatalf("enum semantic change missed: %v", err)
	}
}

func rpcSmokeMethods() []RPCMethod {
	return []RPCMethod{
		{Name: "Add", Version: 1, Flags: 1, ReturnType: "INT", ReturnSize: 2, ReturnAlignSize: 2, Parameters: []RPCParameter{{Name: "a", DataType: "INT", Size: 2, AlignSize: 2, Flags: RPCIn}, {Name: "b", DataType: "INT", Size: 2, AlignSize: 2, Flags: RPCIn}}},
		{Name: "Ping", Version: 1, Flags: 1},
	}
}
func TestRPCExampleClientCurrent(t *testing.T) {
	checkRPCExampleClient(t, rpcSmokeMethods(), "RPCClient", "examples/rpc/generated.go")
}
func TestRPCReferenceExampleClientCurrent(t *testing.T) {
	checkRPCExampleClient(t, rpcReferenceSmokeMethods(), "ReferenceClient", "examples/rpc/references_generated.go")
}
func checkRPCExampleClient(t *testing.T, methods []RPCMethod, client, path string) {
	t.Helper()
	data, err := parseUploadSymbolInfoDataTypes(rpcDatatypeWire(methods...))
	if err != nil {
		t.Fatal(err)
	}
	conn := &Connection{datatypes: data, symbols: map[string]*Symbol{"MAIN.rpc": {DataType: "FB_RPC"}}}
	code, err := conn.GenerateRPCClient("MAIN.rpc", client)
	if err != nil {
		t.Fatal(err)
	}
	source, err := FormatGeneratedCode([]string{code}, "main")
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("UPDATE_RPC_GOLDEN") == "1" {
		if err = os.WriteFile(path, []byte(source), 0644); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if source != string(expected) {
		t.Fatal("RPC example is stale; run UPDATE_RPC_GOLDEN=1 go test -run 'TestRPC.*ExampleClientCurrent'")
	}
}

func TestRPCRejectsGoTypeBeforeAcquisition(t *testing.T) {
	router := newFakeRouter(t)
	router.rpc.Store(true)
	conn := router.connect(t, false)
	type wrongInput struct {
		Small int32 `ads:"small"`
		Large int32 `ads:"large"`
		Value int16 `ads:"value"`
	}
	if _, err := conn.BindRPC[wrongInput, rpcOutput]("MAIN.rpc", "Mixed"); err == nil {
		t.Fatal("accepted wrong Go width")
	}
	if router.handles.Load() != 0 || router.rpcCalls.Load() != 0 {
		t.Fatal("type rejection sent PLC work")
	}
}
func TestRPCOutputErrorAndClose(t *testing.T) {
	router := newFakeRouter(t)
	router.rpc.Store(true)
	conn := router.connect(t, false)
	rpc, err := conn.BindRPC[rpcInput, rpcOutput]("MAIN.rpc", "Mixed")
	if err != nil {
		t.Fatal(err)
	}
	router.rpcShortReply.Store(true)
	got, err := rpc.Call(t.Context(), rpcInput{Value: 6})
	if err == nil || got != (rpcOutput{}) {
		t.Fatalf("partial output escaped: %+v %v", got, err)
	}
	router.rpcShortReply.Store(false)
	conn.Close()
	if _, err = rpc.Call(t.Context(), rpcInput{}); !errors.Is(err, net.ErrClosed) {
		t.Fatal(err)
	}
}
func TestRPCNestedLayoutChangesSignature(t *testing.T) {
	types := map[string]SymbolUploadDataType{"P": {Name: "P", DatatypeEntry: datatypeEntry{Size: 4}, Children: map[string]*SymbolUploadDataType{"x": {Name: "x", DataType: "INT", DatatypeEntry: datatypeEntry{Size: 2}}}}}
	m := RPCMethod{Name: "Read", Flags: 1, ReturnType: "P", ReturnSize: 4, ReturnAlignSize: 2}
	_, _, a, err := rpcSchema(m, types, defaultMaxFrameSize)
	if err != nil {
		t.Fatal(err)
	}
	types["P"].Children["x"].DatatypeEntry.Offs = 2
	_, _, b, err := rpcSchema(m, types, defaultMaxFrameSize)
	if err != nil || a == b {
		t.Fatalf("nested offset change missed: %v", err)
	}
}

func TestRPCOnlineChangeAndHandleCleanup(t *testing.T) {
	router := newFakeRouter(t)
	router.rpc.Store(true)
	conn := router.connect(t, false)
	rpc, err := conn.BindRPC[rpcInput, rpcOutput]("MAIN.rpc", "Mixed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = rpc.Call(t.Context(), rpcInput{}); err != nil {
		t.Fatal(err)
	}
	notifyChange := func() {
		t.Helper()
		old := conn.CurrentEpoch()
		conn.symbolLock.Lock()
		watcher := conn.symbolVersionWatchHandle
		conn.symbolLock.Unlock()
		generation := router.generation.Add(1)
		router.notify(t, watcher, []byte{byte(generation)})
		waitRPCEpoch(t, conn, old)
	}
	notifyChange()
	if _, err = rpc.Call(t.Context(), rpcInput{}); err != nil {
		t.Fatal(err)
	}
	if router.handles.Load() != 2 {
		t.Fatal("online change did not reacquire method handle")
	}
	router.rpcClosing.Store(true)
	conn.Close()
	deadline := time.Now().Add(time.Second)
	for router.rpcReleaseCount.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if router.rpcReleaseCount.Load() != 1 || router.rpcReleased.Load() != 201 {
		t.Fatalf("method release count=%d handle=%d", router.rpcReleaseCount.Load(), router.rpcReleased.Load())
	}
}
