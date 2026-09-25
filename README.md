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
- Offline hardening tests cover real socket bootstrap/reconnect, codec validation, bounded notification delivery, malformed metadata, and generated-package compilation. The earlier live PLC smoke reported 12/12; the hardening changes still need a live PLC rerun.

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

## Lifecycle and delivery contracts

- Connect/reconnect/schema refresh publish a complete generation. Typed operations pin that generation; handles and batches rebind and revalidate after it changes. Application requests fail with `ErrNotConnected` while disconnected. In-flight writes are never automatically replayed.
- `RequestTimeout` defaults to four seconds; `MaxFrameSize` defaults to 16 MiB. Transport failure cancels outstanding requests promptly. `Close` rejects new work, cancels owned workers, and gives best-effort remote releases a shared 100 ms budget.
- A zero `ReconnectPolicy` uses `DefaultReconnectPolicy()`. For a nonzero policy, zero backoff durations get defaults; jitter and boolean fields are honored literally. To customize defaults, start with `DefaultReconnectPolicy()` and change its fields.
- Each subscription has one ordered worker with up to 64 queued samples and 1 MiB of queued payload. When full, new samples are dropped; `Subscription.Dropped()` reports the count. Keep the consumer channel open until `Cancel` returns. `Cancel` stops local delivery before deleting the remote registration.
- Restoration recompiles the decoder against current metadata. `Subscription.Err()` reports restoration/decoding failures; `Retry()` retries an inactive subscription. Cancellation takes precedence over an in-progress restoration.
- Batch operations use one sum request and can have partial PLC success. `BatchReader.Read` changes its Go destination only when every field succeeds; it does not guarantee a same-scan PLC snapshot. `BatchWriter.Write` cannot roll back successful subcommands.
- Metadata is treated as immutable. Do not modify returned Symbol trees or registry entries. Parsing is bounded to 64 nesting levels and 100,000 expanded metadata nodes. Unknown-notification buffering is bounded and expires after five seconds.
- Low-level sum calls accept exported `SumReadCommand` / `SumWriteCommand` values. `ProtocolError` preserves AMS/ADS error codes for `errors.As`.

## Code generation

The CLI emits formatted standalone Go files with required imports, inline nested struct definitions, multidimensional arrays, enums, and aliases for time types. Identifier collisions are rejected. Run it from its own module:

```bash
cd cmd/codegen
go run . -symbols=MAIN.counter,MAIN.values -o=generated_types.go -pkg=plc
```

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
- Handles and batch fields compile a shared validated codec at acquisition and after schema changes. Nested primitive types, widths, field accessibility, array shapes, and offsets are checked before writes.
- Array encoding/decoding caches ordered layouts and supports negative lower bounds, multidimensional arrays, structs, and nested arrays.
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
- `TIME` -> `time.Duration` (nonnegative, up to `math.MaxUint32` milliseconds)
- `TOD`/`DATE`/`DT` -> `time.Time` (`TOD` decodes on 1970-01-01 UTC without a timezone offset)

## Testing

```bash
go test -race ./...
go vet ./...
(cd cmd/codegen && go test ./... && go vet ./...)
(cd examples/simple && go test ./... && go vet ./...)
(cd examples/add_route && go test ./... && go vet ./...)
go test -run='^$' -bench=BenchmarkReview -benchmem
go test -run='^$' -fuzz=FuzzDatatypeUpload -fuzztime=10s
```

The root module does not include the nested CLI/example modules in its ./... traversal.

Live PLC smoke tests (these write PLC values):

```bash
(cd examples/simple && go run .)
(cd examples/simple && go run . -ip=<PLC_IP> -netid=<PLC_NETID>)
```

Regenerate enum wrappers used by the simple smoke test:

```bash
(cd examples/simple && go generate .)
```

Credentialed route-helper smoke test:

```bash
(cd examples/add_route && go run . -plc-ip=<PLC_IP> -sending-netid=<CLIENT_NETID> -adding-hostname=<CLIENT_NAME> -username=<PLC_USER> -password=<PLC_PASSWORD>)
```

## Known limitations

- No automated live PLC CI in this repo; offline tests use a local fake AMS router.
- Sum commands are capped at 500 subcommands and the configured frame size.
- Notification overflow drops new samples and must be monitored by applications requiring loss detection.
- RPC invoke support remains deferred.

## License

MIT
