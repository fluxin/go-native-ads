# go-native-ads

Pure-Go TwinCAT ADS client ([github.com/fluxin/go-native-ads](https://github.com/fluxin/go-native-ads)). The development branch requires Go 1.27 or newer.
based on my original implementation and cleaned up for modern golang. Generics and handle IO added by myself, codegen, docs, tests, and additional features thankfully supported by AI.

## Current status

Current published release: **v0.1.0** (Go 1.26). The working tree targets **v0.2.0** (Go 1.27) with typed RPC and generated clients. See [CHANGELOG.md](CHANGELOG.md) for migration and compatibility notes, [PERFORMANCE.md](PERFORMANCE.md) for measured CPU improvements, and [PLAN.md](PLAN.md) for remaining work.

- Core behavior is implemented: connect, typed handle read/write, batch read/write (sum commands), notifications, symbol/type upload, typed RPC invocation, and code generation.
- Recent protocol/correctness fixes are in place for notification timing, sum parsing, command response validation, and array metadata handling.
- Reconnect and subscription restoration foundations are implemented: policy-driven lifecycle hooks, reconnect loop, epoch-based handle rebinding, and subscription replay.
- Symbol-version change detection and metadata/subscription refresh are implemented via `GroupSymbolVersion` monitoring.
- Route helpers are implemented for UDP-based NetID discovery and credentialed PLC route creation.
- `examples/simple/main.go` is now a comprehensive live PLC smoke test that exercises primitives, time values, structs, arrays (including whole-array-of-structs read/write), batch operations, notifications, and type-safety failures.
- `examples/add_route/main.go` provides a focused route helper smoke program.
- Offline hardening tests cover real socket bootstrap/reconnect, codec validation, bounded notification delivery, malformed metadata, and generated-package compilation. The earlier live PLC smoke reported 12/12; the hardening changes still need a live PLC rerun.

## Install

```bash
go get github.com/fluxin/go-native-ads@v0.1.0
```

Import the package as `ads`:

```go
import ads "github.com/fluxin/go-native-ads"
```

## Migrating from Codeberg

Replace `codeberg.org/fluxin/go-native-ads` imports with `github.com/fluxin/go-native-ads`, run the install command above, then run `go mod tidy` in each consuming module. Update any `replace` directives and regenerate generated files or update their ADS import path. Avoid mixing the two module paths: Go treats them as different packages.

The GitHub history includes the original `v0.0.1`–`v0.0.6` tags unchanged; those versions still declare the Codeberg module path. **v0.1.0 is the first release using the GitHub module path.**

The install command selects the published v0.1.0 release. RPC and the connection-level generic methods below are v0.2.0 development APIs; use this checkout and the examples' local replacements until that release is published.

## API snapshot

```go
policy := ads.DefaultReconnectPolicy()
policy.Enabled = true
conn, err := ads.NewConnection(ctx, ads.ConnectionOptions{
    IP:              "192.168.1.100",       // AMS router address (default: "127.0.0.1")
    NetID:           "192.168.1.100.1.1",   // target AMS Net ID (use "localhost" or "" for local)
    AMSPort:         851,                   // target AMS port (851 = TC3 PLC Runtime 1)
    ReconnectPolicy: policy,
    Transport:       ads.ConnectionTransportAuto,
})
err = conn.Connect()
defer conn.Close()

h, err := conn.GetHandle[int16]("MAIN.counter")
v, err := h.Read()
err = h.Write(42)

reader, err := conn.NewBatchReader[MyStruct](h1, h2, h3)
err = reader.Read(&out)

writer, err := conn.NewBatchWriter[MyStruct](h1, h2, h3)
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
routeInfo, err := ads.DiscoverNetIDInfo(ctx, "192.168.0.10")

// Router diagnostics/introspection helpers
router := conn.Router()
diag := router.Diagnostics()
localNetID, err := router.LocalNetID()
stateValue, stateKnown, stateUpdated := router.StateSnapshot()

// Explicit router port register/unregister diagnostics
assigned, err := router.RegisterPort(0)
err = router.UnregisterPort(assigned.Port)
```

These are API fragments; handle each error before using its result. The original package-level `GetHandle`, `NewBatchReader`, and `NewBatchWriter` functions remain supported.

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

The CLI emits formatted standalone Go files with required imports, inline nested struct definitions, multidimensional arrays, enums, and aliases for time types. Identifier collisions are rejected. Run the CLI from its own module in this checkout against a reachable PLC. Replace the output path with a file inside your application:

```bash
cd cmd/codegen
go run . -ip=192.168.1.100 -netid=192.168.1.100.1.1 \
  -symbols=MAIN.counter,MAIN.values -o=/path/to/your/app/generated_types.go -pkg=plc
```

The CLI targets PLC runtime port 851. Its `-port` flag changes the AMS router TCP port (default 48898). Generated ADS imports use the GitHub module path. The CLI and examples have local `replace` directives so checkout builds use the matching root source.

## RPC and generated clients

RPC methods must have the PLC attribute `{attribute 'TcRpcEnable'}`. Generate typed clients from function-block instances:

```bash
(cd cmd/codegen && go run . -ip=<PLC_IP> -netid=<PLC_NETID> \
  -rpc=MAIN.rpc=RPCClient -pkg=main -o=/path/to/your/app/generated.go)
```

The generated constructor validates method signatures. Calls use ordinary Go methods:

```go
client, err := NewRPCClient(conn, "MAIN.rpc")
if err != nil { return err }
result, err := client.Add(ctx, RPCClientAddInput{A: 7, B: 5})
if err != nil { return err }
fmt.Println(result.ReturnValue)
```

For handwritten typed bindings, use `conn.BindRPC[Input, Result](instance, method)` and `binding.Call(ctx, input)`. Input/output structs use `ads` tags matching PLC parameter names; the return value uses `ads:"$return"`. Empty records use `struct{}`. Generated void methods return only `error`, and methods without inputs omit the input argument.

- Supported fixed values use the shared codec: primitives, STRING, time values, enums, nested structures and fixed arrays where metadata describes their complete layout. Fixed IN|OUT parameters appear in both records.
- Fixed references and INOUT values use ordinary typed fields. `POINTER TO T` / `REFERENCE TO T` with `TcRpcLengthIs` use `[]T`; `PVOID` with a count uses `[]byte`. Counts are element counts, indexed one-based in the complete PLC parameter list. Input slices must match the explicit count exactly; output slices are allocated to that count. Calls do not mutate input slices.
- Length links must point to input-only integer parameters. Output-only or mutable length parameters, unbounded pointers, nested pointers/references, RPC array-dimension flags, custom packing and unknown signatures return `UnsupportedRPCError`. Go memory addresses are never sent to the PLC.
- Parameter values are concatenated in declaration order; the response begins with the return value, followed by outputs. Internal struct offsets come from PLC metadata, never Go memory alignment.
- The connection owns and deduplicates method handles. Reconnects and symbol-version changes invalidate bindings; generated signatures include nested layouts and enum values. Incompatible changes return `ErrRPCSignatureChanged` before invocation. Comments and method-table placement do not affect signatures.
- `Call` observes its context while waiting for a connection generation, handle acquisition and response. `RequestTimeout` also bounds the call. Cancellation cannot undo PLC execution; calls are never automatically retried and a failed decode returns no partial result.
- `RPCMethods` exposes immutable discovery metadata. `GenerateRPCClient` / `GenerateRPCClients` emit client declarations, and `FormatGeneratedCode` produces a standalone source file. Multiple clients in one generation share dependent type declarations.

See [examples/rpc](examples/rpc/README.md) for PLC fixture declarations and the generated smoke harness. Offline socket/codegen tests pass; live TwinCAT ABI, reconnect and online-change validation remains open.

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
(cd examples/rpc && go test ./... && go vet ./...)
go test -run='^$' -bench=BenchmarkReview -benchmem
go test -run='^$' -fuzz=FuzzDatatypeUpload -fuzztime=10s
go test -run='^$' -fuzz=FuzzRPCMetadata -fuzztime=10s
go test -run='^$' -fuzz=FuzzRPCBufferCodec -fuzztime=10s
```

Run these commands from the repository root. The root module does not include the nested CLI/example modules in its `./...` traversal. Root tests use local TCP/Unix sockets; they do not contact a PLC. See [PERFORMANCE.md](PERFORMANCE.md) for benchmark scope and recorded results.

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
- RPC, references and pointer buffers are validated offline with synthetic metadata. Live captured fixtures and real TwinCAT validation remain open. [RPC_CHECKPOINTS.md](RPC_CHECKPOINTS.md) records the two checkpoints and their hardware validation gates.

## License

MIT
