package ads

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeRouter struct {
	rpcClosing                  atomic.Bool
	rpcReleased                 atomic.Uint32
	rpcReleaseCount             atomic.Int32
	rpcShortReply               atomic.Bool
	rpc, rpcChanged, rpcNoReply atomic.Bool
	rpcCalls                    atomic.Int32
	listener                    net.Listener
	generation                  atomic.Uint32
	wide                        atomic.Bool
	mu                          sync.Mutex
	sockets                     []net.Conn
	wg                          sync.WaitGroup
	handles                     atomic.Int32
	notification                atomic.Uint32
}

func newFakeRouter(t *testing.T) *fakeRouter {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	router := &fakeRouter{listener: listener}
	router.generation.Store(1)
	router.wg.Go(func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			router.mu.Lock()
			router.sockets = append(router.sockets, conn)
			router.mu.Unlock()
			router.wg.Go(func() { defer conn.Close(); router.serve(conn) })
		}
	})
	t.Cleanup(func() { listener.Close(); router.disconnect(); router.wg.Wait() })
	return router
}
func (router *fakeRouter) disconnect() {
	router.mu.Lock()
	defer router.mu.Unlock()
	for _, conn := range router.sockets {
		conn.Close()
	}
	router.sockets = nil
}
func (router *fakeRouter) connect(t *testing.T, reconnect bool) *Connection {
	_, port, _ := net.SplitHostPort(router.listener.Addr().String())
	p, _ := strconv.Atoi(port)
	conn, err := NewConnection(t.Context(), ConnectionOptions{IP: "127.0.0.1", Port: p, AMSPort: 851, RequestTimeout: 200 * time.Millisecond, Transport: ConnectionTransportTCP, ReconnectPolicy: ReconnectPolicy{Enabled: reconnect, InitialBackoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Close)
	if err := conn.Connect(); err != nil {
		t.Fatal(err)
	}
	return conn
}
func fakeSymbolUpload() []byte {
	const name = "MAIN.x"
	const dt = "INT"
	entry := symbolEntry{EntryLength: uint32(30 + len(name) + len(dt) + 3), Size: 2, NameLength: uint16(len(name)), TypeLength: uint16(len(dt))}
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, entry)
	buf.WriteString(name + "\x00" + dt + "\x00\x00")
	return buf.Bytes()
}
func fakePayload(data []byte) []byte {
	out := make([]byte, 8+len(data))
	binary.LittleEndian.PutUint32(out[4:], uint32(len(data)))
	copy(out[8:], data)
	return out
}
func (router *fakeRouter) serve(conn net.Conn) {
	for {
		var prefix [6]byte
		if _, err := io.ReadFull(conn, prefix[:]); err != nil {
			return
		}
		length := binary.LittleEndian.Uint32(prefix[2:])
		if length > 1<<20 {
			return
		}
		packet := make([]byte, length)
		if _, err := io.ReadFull(conn, packet); err != nil {
			return
		}
		system := binary.LittleEndian.Uint16(prefix[:2])
		var response []byte
		if system != 0 {
			switch system {
			case amsTCPPortConnect:
				response = []byte{1, 2, 3, 4, 5, 6, 0x34, 0x12}
			case amsTCPGetLocalNetID:
				response = []byte{1, 2, 3, 4, 5, 6}
			case amsTCPPortClose:
				return
			default:
				return
			}
		} else {
			if len(packet) < 32 {
				return
			}
			command := CommandID(binary.LittleEndian.Uint16(packet[16:]))
			id := binary.LittleEndian.Uint32(packet[28:])
			data := packet[32:]
			generation := router.generation.Load()
			var payload []byte
			switch command {
			case CommandIDRead:
				group := binary.LittleEndian.Uint32(data)
				switch group {
				case uint32(GroupSymbolUploadInfo2):
					var info bytes.Buffer
					_ = binary.Write(&info, binary.LittleEndian, SymbolUploadInfo{SymbolCount: 1, SymbolLength: uint32(len(router.symbolUpload())), DataTypeLength: uint32(len(router.datatypeUpload()))})
					payload = fakePayload(info.Bytes())
				case uint32(GroupSymbolDataTypeUpload):
					payload = fakePayload(router.datatypeUpload())
				case uint32(GroupSymbolUpload):
					payload = fakePayload(router.symbolUpload())
				case uint32(GroupSymbolVersion):
					payload = fakePayload([]byte{byte(generation)})
				case uint32(GroupSymbolValueByHandle):
					if binary.LittleEndian.Uint32(data[4:]) != generation*100 {
						payload = []byte{3, 7, 0, 0, 0, 0, 0, 0}
					} else {
						payload = fakePayload([]byte{42, 0})
					}
				default:
					return
				}
			case CommandIDReadWrite:
				group := binary.LittleEndian.Uint32(data)
				if group == uint32(GroupSymbolHandleByName) {
					router.handles.Add(1)
					value := make([]byte, 4)
					handle := generation * 100
					if router.rpc.Load() {
						handle++
						if string(data[16:]) == "MAIN.rpc#Ping\x00" {
							handle++
						}
					}
					binary.LittleEndian.PutUint32(value, handle)
					payload = fakePayload(value)
				} else if group == uint32(GroupSymbolValueByHandle) && router.rpc.Load() {
					router.rpcCalls.Add(1)
					if router.rpcNoReply.Load() {
						continue
					}
					handle := binary.LittleEndian.Uint32(data[4:])
					if handle == generation*100+2 {
						payload = fakePayload(nil)
					} else if handle != generation*100+1 || len(data) != 23 || binary.LittleEndian.Uint32(data[8:]) != 7 || binary.LittleEndian.Uint32(data[12:]) != 7 {
						payload = []byte{5, 7, 0, 0}
					} else {
						result := make([]byte, 7)
						binary.LittleEndian.PutUint32(result, uint32(int32(int8(data[16]))+int32(binary.LittleEndian.Uint32(data[17:]))))
						result[4] = 1
						binary.LittleEndian.PutUint16(result[5:], binary.LittleEndian.Uint16(data[21:])+1)
						if router.rpcShortReply.Load() {
							result = result[:len(result)-1]
						}
						payload = fakePayload(result)
					}
				} else if group == uint32(GroupSumupRead) {
					count := int(binary.LittleEndian.Uint32(data[4:]))
					result := make([]byte, count*6)
					for i := 0; i < count; i++ {
						offset := binary.LittleEndian.Uint32(data[16+i*12+4:])
						if offset != generation*100 {
							binary.LittleEndian.PutUint32(result[i*4:], 1795)
						}
						binary.LittleEndian.PutUint16(result[count*4+i*2:], 42)
					}
					payload = fakePayload(result)
				} else if group == uint32(GroupSumupWrite) {
					count := binary.LittleEndian.Uint32(data[4:])
					payload = fakePayload(make([]byte, count*4))
				} else {
					return
				}
			case CommandIDAddDeviceNotification:
				payload = make([]byte, 8)
				binary.LittleEndian.PutUint32(payload[4:], router.notification.Add(1))
			case CommandIDDeleteDeviceNotification, CommandIDWrite:
				if command == CommandIDWrite && len(data) >= 16 && binary.LittleEndian.Uint32(data) == uint32(GroupSymbolReleaseHandle) {
					router.rpcReleased.Store(binary.LittleEndian.Uint32(data[12:]))
					router.rpcReleaseCount.Add(1)
				}
				if router.rpcClosing.Load() {
					continue
				}
				payload = make([]byte, 4)
			default:
				return
			}
			response = reviewFrame(command, id, payload)
			binary.LittleEndian.PutUint16(response[18:], 5)
		}
		out := make([]byte, 6+len(response))
		binary.LittleEndian.PutUint16(out, system)
		binary.LittleEndian.PutUint32(out[2:], uint32(len(response)))
		copy(out[6:], response)
		if _, err := conn.Write(out); err != nil {
			return
		}
	}
}
func TestRealTransportBootstrapReconnectAndBatch(t *testing.T) {
	router := newFakeRouter(t)
	conn := router.connect(t, true)
	h, err := conn.GetHandle[int16]("MAIN.x")
	if err != nil {
		t.Fatal(err)
	}
	if v, err := h.Read(); err != nil || v != 42 {
		t.Fatalf("initial read %d %v", v, err)
	}
	reader, err := conn.NewBatchReader[struct{ X int16 }](h)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := conn.NewBatchWriter[struct{ X int16 }](h)
	if err != nil {
		t.Fatal(err)
	}
	var value struct{ X int16 }
	if err := reader.Read(&value); err != nil || value.X != 42 {
		t.Fatalf("initial batch %+v %v", value, err)
	}
	oldEpoch := conn.CurrentEpoch()
	router.generation.Store(2)
	router.disconnect()
	deadline := time.Now().Add(2 * time.Second)
	for conn.CurrentEpoch() == oldEpoch && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if conn.CurrentEpoch() == oldEpoch {
		t.Fatal("no reconnect")
	}
	// Construct a fresh batch using the untouched pre-reconnect typed handle.
	fresh, err := NewBatchReader[struct{ X int16 }](conn, h)
	if err != nil {
		t.Fatal(err)
	}
	for _, br := range []*BatchReader[struct{ X int16 }]{reader, fresh} {
		if err := br.Read(&value); err != nil || value.X != 42 {
			t.Fatalf("rebound batch %+v %v", value, err)
		}
	}
	if err := writer.Write(struct{ X int16 }{41}); err != nil {
		t.Fatal(err)
	}
	if v, err := h.Read(); err != nil || v != 42 {
		t.Fatalf("rebound read %d %v", v, err)
	}
	conn.Close()
	if _, err := h.Read(); err == nil {
		t.Fatal("read succeeded after Close")
	}
}
func TestConcurrentColdAcquisitionAndRouterCalls(t *testing.T) {
	router := newFakeRouter(t)
	conn := router.connect(t, false)
	var wg sync.WaitGroup
	failures := make(chan error, 32)
	for range 16 {
		wg.Go(func() {
			_, err := GetHandle[int16](conn, "MAIN.x")
			if err != nil {
				failures <- err
			}
		})
		wg.Go(func() {
			id, err := conn.Router().LocalNetID()
			if err != nil {
				failures <- err
			} else if id != "1.2.3.4.5.6" {
				failures <- fmt.Errorf("wrong router reply %s", id)
			}
		})
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if router.handles.Load() != 1 {
		t.Fatalf("duplicate handle acquisition: %d", router.handles.Load())
	}
}

func (router *fakeRouter) symbolUpload() []byte {
	if router.rpc.Load() {
		return symbolWire("MAIN.rpc", "FB_RPC", 1)
	}
	if router.wide.Load() {
		return symbolWire("MAIN.x", "DINT", 4)
	}
	return fakeSymbolUpload()
}
func (router *fakeRouter) notify(t *testing.T, handle uint32, data []byte) {
	var payload bytes.Buffer
	_ = binary.Write(&payload, binary.LittleEndian, notificationStream{Length: uint32(28 + len(data)), Stamps: 1})
	_ = binary.Write(&payload, binary.LittleEndian, stampHeader{Samples: 1})
	_ = binary.Write(&payload, binary.LittleEndian, notificationSample{Handle: handle, Size: uint32(len(data))})
	payload.Write(data)
	frame := reviewFrame(CommandIDDeviceNotification, 0, payload.Bytes())
	packet := make([]byte, 6+len(frame))
	binary.LittleEndian.PutUint32(packet[2:], uint32(len(frame)))
	copy(packet[6:], frame)
	router.mu.Lock()
	defer router.mu.Unlock()
	if len(router.sockets) == 0 {
		t.Fatal("no socket")
	}
	if _, err := router.sockets[len(router.sockets)-1].Write(packet); err != nil {
		t.Fatal(err)
	}
}
func TestOnlineSchemaChangeRejectsOldTypeAndRestoresSubscription(t *testing.T) {
	router := newFakeRouter(t)
	conn := router.connect(t, false)
	handle, err := GetHandle[int16](conn, "MAIN.x")
	if err != nil {
		t.Fatal(err)
	}
	updates := make(chan Update[int16], 8)
	subscription, err := handle.Subscribe(updates, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Cancel()
	change := func(generation uint32, wide bool) {
		old := conn.CurrentEpoch()
		router.wide.Store(wide)
		router.generation.Store(generation)
		conn.symbolLock.Lock()
		watcher := conn.symbolVersionWatchHandle
		conn.symbolLock.Unlock()
		router.notify(t, watcher, []byte{byte(generation)})
		deadline := time.Now().Add(time.Second)
		for conn.CurrentEpoch() == old && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if conn.CurrentEpoch() == old {
			t.Fatal("schema notification did not refresh")
		}
	}
	change(2, true)
	if err := handle.Write(42); err == nil {
		t.Fatal("old INT handle accepted after DINT schema change")
	}
	if subscription.Err() == nil {
		t.Fatal("incompatible subscription restore was not reported")
	}
	change(3, false)
	if subscription.Err() != nil {
		t.Fatalf("subscription did not recover: %v", subscription.Err())
	}
	conn.symbolLock.Lock()
	notification := subscription.spec.adsHandle
	conn.symbolLock.Unlock()
	router.notify(t, notification, []byte{42, 0})
	select {
	case update := <-updates:
		if update.Value != 42 {
			t.Fatalf("restored value %d", update.Value)
		}
	case <-time.After(time.Second):
		t.Fatal("no restored update")
	}
}
func TestRouterUnregisterIsOneWay(t *testing.T) {
	router := newFakeRouter(t)
	conn := router.connect(t, false)
	if err := conn.Router().UnregisterPort(0x1234); err != nil {
		t.Fatalf("one-way unregister: %v", err)
	}
}

func (router *fakeRouter) datatypeUpload() []byte {
	if !router.rpc.Load() {
		return nil
	}
	method := rpcTestMethod()
	if router.rpcChanged.Load() {
		method.Parameters[0].Name = "renamed"
	}
	return rpcDatatypeWire(method, RPCMethod{Name: "Ping", Version: 1, Flags: 1})
}
