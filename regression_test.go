package ads

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"os/exec"
	"reflect"
	"sync"
	"testing"
	"time"
)

func reviewConn(t *testing.T) *Connection {
	c, err := NewConnection(t.Context(), ConnectionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c.state = connectionStateConnected
	return c
}

func reviewFrame(command CommandID, id uint32, payload []byte) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, amsHeader{Command: command, InvokeID: id, Length: uint32(len(payload))})
	b.Write(payload)
	return b.Bytes()
}

func TestReviewStructTypeMismatch(t *testing.T) {
	sym := &Symbol{FullName: "MAIN.s", Length: 4, Children: map[string]*Symbol{"X": {Name: "X", DataType: "REAL", Length: 4}}}
	buf := make([]byte, 4)
	if err := encodeStructValue(reflect.ValueOf(struct{ X int32 }{42}), sym, buf, nil); err == nil {
		t.Fatalf("DINT Go field accepted for REAL PLC field, bytes=%x", buf)
	}
}

func TestReviewStructWrongWidth(t *testing.T) {
	defer func() {
		if p := recover(); p != nil {
			t.Errorf("wrong field width panics instead of returning error: %v", p)
		}
	}()
	sym := &Symbol{Length: 2, Children: map[string]*Symbol{"X": {Name: "X", DataType: "INT", Length: 2}}}
	if err := encodeStructValue(reflect.ValueOf(struct{ X int64 }{42}), sym, make([]byte, 2), nil); err == nil {
		t.Error("accepted incompatible width")
	}
}

func TestReviewBatchConnectionOwnership(t *testing.T) {
	c1, c2 := reviewConn(t), reviewConn(t)
	h := &Handle[int16]{symbolBinding: symbolBinding{conn: c2, handle: 99, length: 2, dataType: "INT", symbolName: "MAIN.x"}}
	if _, err := NewBatchWriter[struct{ X int16 }](c1, h); err == nil {
		t.Error("batch accepts handle belonging to another connection")
	}
}

func TestReviewBatchStaleEpoch(t *testing.T) {
	c := reviewConn(t)
	c.epoch.Store(2)
	c.symbols = map[string]*Symbol{"MAIN.x": {Handle: 22, Length: 2, DataType: "INT"}}
	h := &Handle[int16]{symbolBinding: symbolBinding{conn: c, handle: 11, length: 2, dataType: "INT", symbolName: "MAIN.x", bindEpoch: 1}}
	br, err := NewBatchReader[struct{ X int16 }](c, h)
	if err != nil {
		t.Fatal(err)
	}
	if err := br.handles[0].bind(); err != nil {
		t.Fatal(err)
	}
	if br.handles[0].handle != 22 {
		t.Errorf("new batch stamped old handle %d as epoch %d; current handle=22", br.handles[0].handle, br.handles[0].bindEpoch)
	}
}

func TestReviewRestoreDecoder(t *testing.T) {
	c := reviewConn(t)
	type value struct{ X int16 }
	old := &Symbol{FullName: "MAIN.s", Length: 4, Children: map[string]*Symbol{"X": {Name: "X", DataType: "INT", Length: 2, Offset: 0}}}
	fresh := &Symbol{FullName: "MAIN.s", Handle: 22, Length: 4, Children: map[string]*Symbol{"X": {Name: "X", DataType: "INT", Length: 2, Offset: 2}}}
	updates := make(chan Update[value], 1)
	spec := &subscriptionSpec{id: 1, symbolName: "MAIN.s", callback: createNotificationCallback(old, nil, updates), factory: func(s *Symbol, types map[string]SymbolUploadDataType) (NotificationCallback, error) {
		return createNotificationCallback(s, types, updates), nil
	}}
	c.subscriptions[1] = spec
	c.testRestoreLookupSymbolFn = func(string) (*Symbol, error) { return fresh, nil }
	c.testRestoreAddNotificationFn = func(uint32, uint32, uint32, TransMode, time.Duration, time.Duration) (uint32, error) { return 99, nil }
	c.restoreSubscriptions([]*subscriptionSpec{spec})
	if err := c.activeNotifications[99](t.Context(), 0, []byte{7, 0, 42, 0}); err != nil {
		t.Fatal(err)
	}
	if got := (<-updates).Value.X; got != 42 {
		t.Errorf("restored callback decoded %d from old offset, want 42 from new offset", got)
	}
}

func TestReviewRestoreAfterCancel(t *testing.T) {
	c := reviewConn(t)
	spec := &subscriptionSpec{id: 1, symbolName: "MAIN.x", callback: func(context.Context, uint64, []byte) error { return nil }}
	c.subscriptions[1] = spec
	c.testRestoreLookupSymbolFn = func(string) (*Symbol, error) { return &Symbol{Handle: 22, Length: 2}, nil }
	c.testRestoreAddNotificationFn = func(uint32, uint32, uint32, TransMode, time.Duration, time.Duration) (uint32, error) {
		if err := c.cancelSubscription(1); err != nil {
			t.Fatal(err)
		}
		return 99, nil
	}
	c.restoreSubscriptions([]*subscriptionSpec{spec})
	if _, active := c.activeNotifications[99]; active {
		t.Error("restore reactivated callback after Cancel removed subscription")
	}
}

func TestReviewNotificationTruncation(t *testing.T) {
	c := reviewConn(t)
	var got []byte
	c.activeNotifications[42] = func(_ context.Context, _ uint64, data []byte) error { got = append([]byte(nil), data...); return nil }
	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, notificationStream{Length: 29, Stamps: 1})
	_ = binary.Write(&b, binary.LittleEndian, stampHeader{Samples: 1})
	_ = binary.Write(&b, binary.LittleEndian, notificationSample{Handle: 42, Size: 4})
	b.WriteByte(7)
	err := c.deviceNotification(t.Context(), b.Bytes())
	if err == nil || got != nil {
		t.Errorf("truncated sample accepted: error=%v delivered=%x", err, got)
	}
}

func TestReviewTODRoundTrip(t *testing.T) {
	want := time.Date(2026, 9, 25, 12, 34, 56, 0, time.UTC)
	var got time.Time
	buf := make([]byte, 4)
	if err := encodeTimeValue(reflect.ValueOf(want), buf, "TOD"); err != nil {
		t.Fatal(err)
	}
	if err := decodeTimeValue(reflect.ValueOf(&got).Elem(), buf, "TOD"); err != nil {
		t.Fatal(err)
	}
	if got.UTC().Format("15:04:05") != want.Format("15:04:05") {
		t.Errorf("TOD roundtrip changed %s to %s", want.Format("15:04:05"), got.UTC().Format("15:04:05"))
	}
}

func TestReviewTimeNotification(t *testing.T) {
	updates := make(chan Update[time.Time], 1)
	sym := &Symbol{FullName: "MAIN.date", DataType: "DATE", Length: 4}
	cb := createNotificationCallback(sym, nil, updates)
	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, 1700000000)
	if err := cb(t.Context(), 0, buf); err != nil {
		t.Fatal(err)
	}
	if got := (<-updates).Value; got.IsZero() {
		t.Error("time.Time notification silently delivered zero value")
	}
}

func TestReviewDatatypeTruncation(t *testing.T) {
	if os.Getenv("ADS_REVIEW_PARSER_CHILD") == "1" {
		_, err := parseUploadSymbolInfoDataTypes([]byte{1})
		if err == nil {
			t.Fatal("missing parse error")
		}
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReviewDatatypeTruncation$")
	cmd.Env = append(os.Environ(), "ADS_REVIEW_PARSER_CHILD=1", "GORACE=atexit_sleep_ms=0")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatal("one-byte datatype upload never returns (parser makes no progress)")
	}
	if err != nil {
		t.Fatalf("child: %v %s", err, out)
	}
}

func TestReviewCodegenPrimitiveArray(t *testing.T) {
	c := reviewConn(t)
	c.datatypes = map[string]SymbolUploadDataType{"ARRAY [0..1] OF INT": {DataType: "INT"}}
	sym := &Symbol{FullName: "MAIN.a", DataType: "ARRAY [0..1] OF INT", Length: 4, Children: map[string]*Symbol{
		"[0]": {DataType: "INT", Length: 2}, "[1]": {DataType: "INT", Length: 2, Offset: 2},
	}}
	info, err := c.analyzeSymbol(sym)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsArray || info.GoType != "[2]ads.Int16" {
		t.Errorf("array generated as IsArray=%v GoType=%s", info.IsArray, info.GoType)
	}
}

func TestReviewResponseHoldsGlobalLock(t *testing.T) {
	c := reviewConn(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	// This is the state after a request selects timeout and before it removes its entry.
	c.activeRequests[1] = &pendingRequest{command: CommandIDRead, response: make(chan commandResponse, 1)}
	done := make(chan struct{})
	go func() { c.handleReceive(ctx, reviewFrame(CommandIDRead, 1, nil)); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("orphaned response blocked dispatcher")
	}
	if !c.activeRequestLock.TryLock() {
		t.Fatal("dispatcher retained request lock")
	}
	c.activeRequestLock.Unlock()

}

func TestReviewConcurrentRebind(t *testing.T) {
	c := reviewConn(t)
	c.epoch.Store(2)
	c.symbols = map[string]*Symbol{"MAIN.x": {Handle: 22, Length: 2, DataType: "INT"}}
	h := &Handle[int16]{symbolBinding: symbolBinding{conn: c, handle: 11, length: 2, dataType: "INT", symbolName: "MAIN.x", bindEpoch: 1}}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 32 {
		wg.Go(func() { <-start; _ = h.ensureBound() })
	}
	close(start)
	wg.Wait()
}

func TestReviewRouterRequestSurvivesTransportFailure(t *testing.T) {
	c := reviewConn(t)
	client, server := net.Pipe()
	transportCtx := c.activateTransport(client)
	c.startTransportWorkers(transportCtx, client)
	defer func() { c.shutdown(); c.stopTransport() }()
	done := make(chan error, 1)
	go func() { _, err := c.send(buildGetLocalNetIDPacket()); done <- err }()
	buf := make([]byte, 6)
	if _, err := server.Read(buf); err != nil {
		t.Fatal(err)
	}
	_ = server.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Error("missing transport error")
		}
	case <-time.After(30 * time.Millisecond):
		t.Error("router request still blocked after peer disconnect; only whole-connection cancellation releases it")
		c.shutdown()
		<-done
	}
}

func TestReviewAMSHeaderError(t *testing.T) {
	c := reviewConn(t)
	response := make(chan commandResponse, 1)
	c.activeRequests[1] = &pendingRequest{command: CommandIDWrite, response: response}
	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, amsHeader{Command: CommandIDWrite, InvokeID: 1, Length: 4, ErrorCode: 7})
	b.Write(make([]byte, 4))
	c.handleReceive(t.Context(), b.Bytes())
	if result := <-response; result.err == nil {
		t.Error("nonzero AMS header error discarded and write reported successful")
	}
}

func BenchmarkReviewArrayDecode(b *testing.B) {
	for _, n := range []int{16, 256, 4096} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			sym := &Symbol{Length: uint32(n * 2), Children: make(map[string]*Symbol, n)}
			for i := range n {
				name := fmt.Sprintf("[%d]", i)
				sym.Children[name] = &Symbol{Name: name, DataType: "INT", Length: 2, Offset: uint32(i * 2)}
			}
			v := reflect.New(reflect.ArrayOf(n, reflect.TypeFor[int16]())).Elem()
			buf := make([]byte, n*2)
			if err := decodeArrayField(v, sym, buf, nil); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if err := decodeArrayField(v, sym, buf, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkReviewSumReadParse(b *testing.B) {
	for _, n := range []int{1, 100, 500} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			cmds := make([]sumReadSubCommand, n)
			for i := range n {
				cmds[i].Length = 2
			}
			resp := make([]byte, 8+n*6)
			binary.LittleEndian.PutUint32(resp[4:], uint32(n*6))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := parseSumReadResponse(cmds, resp); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
