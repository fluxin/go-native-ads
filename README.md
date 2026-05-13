# go-native-ads

Pure-Go TwinCAT ADS client (`codeberg.org/fluxin/go-native-ads`).
based on my original implementation and cleaned up for modern golang. Generics and handle IO added by myself, codegen, docs, tests, and additional features thankfully supported by AI.

## Current status

- Core behavior is implemented: connect, typed handle read/write, batch read/write (sum commands), notifications, symbol/type upload, and code generation.
- Recent protocol/correctness fixes are in place for notification timing, sum parsing, command response validation, and array metadata handling.
- Reconnect and subscription restoration foundations are implemented: policy-driven lifecycle hooks, reconnect loop, epoch-based handle rebinding, and subscription replay.
- Symbol-version change detection and metadata/subscription refresh are implemented via `GroupSymbolVersion` monitoring.
- Route helpers are implemented for UDP-based NetID discovery and credentialed PLC route creation.
- `examples/simple/main.go` is now a comprehensive live PLC smoke test that exercises primitives, time values, structs, arrays (including whole-array-of-structs read/write), batch operations, notifications, and type-safety failures.
- `examples/add_route/main.go` provides a focused route helper smoke program.
- Latest reported live PLC smoke execution passed all checks (12/12).

## Install

```bash
go get codeberg.org/fluxin/go-native-ads
```

## API snapshot

```go
conn, err := ads.NewConnection(ctx, ads.ConnectionOptions{
    IP:              "192.168.1.100",       // AMS router address (default: "127.0.0.1")
    NetID:           "192.168.1.100.1.1",   // target AMS Net ID (use "localhost" or "" for local)
    AMSPort:         851,                   // target AMS port (851 = TC3 PLC Runtime 1)
    ReconnectPolicy: ads.ReconnectPolicy{Enabled: true},
    Transport:       ads.ConnectionTransportAuto,
})
err = conn.Connect()
defer conn.Close()

h, err := ads.GetHandle[ads.Int16](conn, "MAIN.counter")
v, err := h.Read()
err = h.Write(42)

reader, err := ads.NewBatchReader[MyStruct](conn, h1, h2, h3)
err = reader.Read(&out)

writer, err := ads.NewBatchWriter[MyStruct](conn, h1, h2, h3)
err = writer.Write(in)

updates := make(chan ads.Update[ads.Int16], 10)
sub, err := h.Subscribe(updates, nil)
defer sub.Cancel()

code, err := conn.GenerateType("MAIN.symbol")
body, err := conn.GenerateStructBody("MAIN.symbol")
info, err := conn.AnalyzeType("MAIN.symbol")
enum, err := conn.GetEnum("TESTE")

// Administrative/system state helpers
err = conn.SetAdsState(ads.AdsStateRun)
err = conn.SetAdsState(ads.AdsStateConfig)
state, err := conn.ReadState()

// Route helpers (UDP system service)
ok, err := ads.AddRouteToPLC(ctx, ads.AddRouteToPLCRequest{
    SendingNetID:   "1.2.3.4.1.1",
    AddingHostName: "my-host",
    PLCIP:          "192.168.0.10",
    Username:       "Administrator",
    Password:       "secret",
})
netID, err := ads.DiscoverNetID(ctx, "192.168.0.10")
info, err := ads.DiscoverNetIDInfo(ctx, "192.168.0.10")

// Router diagnostics/introspection helpers
router := conn.Router()
diag := router.Diagnostics()
localNetID, err := router.LocalNetID()
stateValue, stateKnown, stateUpdated := router.StateSnapshot()

// Explicit router port register/unregister diagnostics
assigned, err := router.RegisterPort(0)
err = router.UnregisterPort(assigned.Port)
```

## Connection transport

`ConnectionOptions.Transport` controls how the AMS router connection is opened:

- `ads.ConnectionTransportAuto` (default) uses a local Unix socket on Linux when `NetID` is local and the socket exists, otherwise TCP.
- `ads.ConnectionTransportTCP` always uses `IP:Port`.
- `ads.ConnectionTransportUnix` always uses `UnixSocketPath`.

`UnixSocketPath` defaults to `/run/ams/tcsyssrv.ams.sock`. Set it when using a nonstandard TwinCAT/AMS router socket path.

## Verified behavior

- `Connect()` uploads/caches symbol and datatype metadata.
- `Handle.Read/Write` use `GroupSymbolValueByHandle (0xF005)`.
- `WriteControl` helpers support ADS/device state transitions.
- Symbol version changes trigger metadata reload and subscription restoration.
- Router note frames (`0x1001`) are parsed into router state diagnostics.
- NetID introspection helpers expose string/byte conversions and remote/local NetID queries.
- Enum metadata can be queried at runtime with `GetEnum`, and codegen emits enum type/const definitions.
- `SumRead`/`SumWrite` use `ReadWrite` with groups `0xF080` / `0xF081`.
- Notification `CycleTime` and `MaxDelay` are encoded as ADS ticks (100ns).
- Primitive and `time.Time` handles are validated at acquisition; struct/array compatibility is validated during encode/decode.
- Array encoding/decoding uses symbol child ordering, supports non-zero lower bounds, and handles all element types (primitives, structs, nested arrays).
- Type mismatch errors include the symbol name, Go type, and ADS type for quick diagnosis.

## Supported mapping

- `BOOL` -> `bool`
- `SINT` -> `int8`
- `INT` -> `int16`
- `DINT` -> `int32`
- `LINT` -> `int64`
- `BYTE`/`USINT` -> `uint8`
- `WORD`/`UINT` -> `uint16`
- `DWORD`/`UDINT` -> `uint32`
- `LWORD`/`ULINT` -> `uint64`
- `REAL` -> `float32`
- `LREAL` -> `float64`
- `STRING` -> `string`
- `TIME`/`TOD`/`DATE`/`DT` -> `time.Time`

## Testing

```bash
go test ./...
go vet ./...
```

Live PLC smoke test (local):

```bash
go run examples/simple/main.go
```

Live PLC smoke test (remote):

```bash
go run examples/simple/main.go -ip=<PLC_IP> -netid=<PLC_NETID>
```

Regenerate enum wrappers used by the simple smoke test:

```bash
go generate ./examples/simple
```

Route helper smoke test (credentialed PLC route creation):

```bash
go run examples/add_route/main.go \
  -plc-ip=<PLC_IP> \
  -sending-netid=<CLIENT_NETID> \
  -adding-hostname=<CLIENT_NAME> \
  -username=<PLC_USER> \
  -password=<PLC_PASSWORD>
```

## Known limitations

- No automated live PLC CI in this repo.
- Sum commands are capped at 500 sub-commands.
- RPC invoke support remains deferred.

## License

MIT
