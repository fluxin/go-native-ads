package ads

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func datatypeWire(name, base string, size uint32, levels []datatypeArrayInfo) []byte {
	entry := datatypeEntry{NameLength: uint16(len(name)), TypeLength: uint16(len(base)), Size: size, ArrayDim: uint16(len(levels))}
	entry.EntryLength = uint32(42 + len(name) + len(base) + 3 + 8*len(levels))
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, entry)
	buf.WriteString(name + "\x00" + base + "\x00\x00")
	_ = binary.Write(&buf, binary.LittleEndian, levels)
	return buf.Bytes()
}
func symbolWire(name, dt string, size uint32) []byte {
	entry := symbolEntry{NameLength: uint16(len(name)), TypeLength: uint16(len(dt)), Size: size, EntryLength: uint32(30 + len(name) + len(dt) + 3)}
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, entry)
	buf.WriteString(name + "\x00" + dt + "\x00\x00")
	return buf.Bytes()
}
func TestParsedNegativeMultidimensionalArrayRoundTrip(t *testing.T) {
	const dt = "ARRAY [-1..0, 3..5] OF INT"
	types, err := parseUploadSymbolInfoDataTypes(datatypeWire(dt, "INT", 12, []datatypeArrayInfo{{LBound: -1, Elements: 2}, {LBound: 3, Elements: 3}}))
	if err != nil {
		t.Fatal(err)
	}
	symbols, err := parseUploadSymbolInfoSymbols(symbolWire("MAIN.matrix", dt, 12), types)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := symbols["MAIN.matrix[-1,3]"]; !ok {
		t.Fatalf("missing multidimensional leaf: %v", symbols)
	}
	sym := symbols["MAIN.matrix"]
	input := [2][3]int16{{1, 2, 3}, {4, 5, 6}}
	var output [2][3]int16
	data := make([]byte, 12)
	if err := encodeValue(reflect.ValueOf(input), sym, data, types); err != nil {
		t.Fatal(err)
	}
	if err := decodeValue(reflect.ValueOf(&output).Elem(), sym, data, types); err != nil {
		t.Fatal(err)
	}
	if input != output {
		t.Fatalf("array mismatch: %v", output)
	}
	conn := &Connection{symbols: symbols, datatypes: types}
	source, err := conn.GenerateType("MAIN.matrix")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(source, "[2][3]ads.Int16") {
		t.Fatal(source)
	}
}
func TestCodecRejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		value  any
		symbol *Symbol
	}{
		{struct{ x int16 }{}, &Symbol{Length: 2, Children: map[string]*Symbol{"x": {Name: "x", DataType: "INT", Length: 2}}}},
		{[2]int32{}, &Symbol{Length: 4, Children: map[string]*Symbol{"[0]": {Name: "[0]", DataType: "INT", Length: 2}, "[1]": {Name: "[1]", DataType: "INT", Length: 2, Offset: 2}}}},
		{int16(1), &Symbol{DataType: "INT", Length: 1}},
		{time.Duration(-1), &Symbol{DataType: "TIME", Length: 4}},
		{time.Duration(math.MaxUint32+1) * time.Millisecond, &Symbol{DataType: "TIME", Length: 4}},
		{"abcd", &Symbol{DataType: "STRING", Length: 4}},
	}
	for _, test := range tests {
		if err := encodeValue(reflect.ValueOf(test.value), test.symbol, make([]byte, test.symbol.Length), nil); err == nil {
			t.Errorf("accepted invalid %T", test.value)
		}
	}
	if _, err := resolveDataType("A", map[string]SymbolUploadDataType{"A": {DataType: "B"}, "B": {DataType: "A"}}); err == nil {
		t.Fatal("accepted alias cycle")
	}
}
func TestBoundedOrderedDeliveryAndCancel(t *testing.T) {
	conn := reviewConn(t)
	defer conn.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	values := make(chan byte, notificationQueueSize+1)
	spec := &subscriptionSpec{ctx: ctx, cancel: cancel}
	delivery := conn.newDelivery(spec, func(ctx context.Context, _ uint64, data []byte) error {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		values <- data[0]
		return nil
	})
	callback := delivery.callback(spec)
	_ = callback(ctx, 0, []byte{0})
	<-entered
	for i := 1; i <= notificationQueueSize+20; i++ {
		_ = callback(ctx, uint64(i), []byte{byte(i)})
	}
	if got := spec.dropped.Load(); got != 20 {
		t.Fatalf("dropped %d want 20", got)
	}
	close(release)
	for i := 0; i <= notificationQueueSize; i++ {
		select {
		case value := <-values:
			if value != byte(i) {
				t.Fatalf("out-of-order: got %d want %d", value, i)
			}
		case <-time.After(time.Second):
			t.Fatal("delivery stalled")
		}
	}
	delivery.cancel()
	<-delivery.done
	if err := callback(ctx, 0, []byte{1}); err == nil {
		t.Fatal("delivery accepted after cancellation")
	}
}
func TestSubscriptionCancelReleasesBlockedConsumer(t *testing.T) {
	conn := reviewConn(t)
	defer conn.Close()
	ctx, cancel := context.WithCancel(t.Context())
	spec := &subscriptionSpec{id: 1, ctx: ctx, cancel: cancel}
	entered := make(chan struct{})
	delivery := conn.newDelivery(spec, func(ctx context.Context, _ uint64, _ []byte) error { close(entered); <-ctx.Done(); return ctx.Err() })
	spec.delivery = delivery
	conn.subscriptions[1] = spec
	_ = delivery.callback(spec)(ctx, 0, []byte{1})
	<-entered
	done := make(chan struct{})
	go func() { _ = conn.cancelSubscription(1); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Cancel blocked on consumer")
	}
}
func FuzzDatatypeUpload(f *testing.F) {
	f.Add([]byte{1})
	f.Add(datatypeWire("INT", "INT", 2, nil))
	f.Add(datatypeWire("ARRAY [0..1] OF INT", "INT", 4, []datatypeArrayInfo{{Elements: 2}}))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		_, _ = parseUploadSymbolInfoDataTypes(data)
	})
}
func FuzzSymbolUpload(f *testing.F) {
	f.Add([]byte{1})
	f.Add(symbolWire("MAIN.x", "INT", 2))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		_, _ = parseUploadSymbolInfoSymbols(data, nil)
	})
}
func FuzzNotificationFrame(f *testing.F) {
	f.Add([]byte{0})
	f.Add(make([]byte, 8))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		conn, _ := NewConnection(t.Context(), ConnectionOptions{})
		// Suppress asynchronous unknown-handle work while fuzzing the parser itself.
		conn.state = connectionStateClosed
		_ = conn.deviceNotification(t.Context(), data)
	})
}
