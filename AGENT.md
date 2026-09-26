# AGENT.md - Verified project context

## Purpose

`go-native-ads` is a pure-Go TwinCAT ADS client (`github.com/fluxin/go-native-ads`) with no C dependencies.

## Repository and release

- Canonical repository/module: `github.com/fluxin/go-native-ads`.
- Current release: `v0.2.0` (Go 1.27); `v0.1.0` remains available for Go 1.26. See `CHANGELOG.md` for compatibility changes.
- Historical `v0.0.1`–`v0.0.6` tags retain the Codeberg module path; do not retag or rewrite them.
- The CLI and examples are separate modules with local replacements. Test/vet each module as shown in `README.md`; root `./...` does not cover them.

## Public API snapshot

```go
ads.NewConnection(ctx, ConnectionOptions) (*Connection, error)
(*Connection).Connect() error
(*Connection).Close()

(*Connection).SetAdsState(state AdsState) error

(*Connection).Router() RouterClient

ads.AddRouteToPLC(ctx, req AddRouteToPLCRequest) (bool, error)
ads.DiscoverNetID(ctx, ip) (string, error)
ads.DiscoverNetIDInfo(ctx, ip) (NetIDRouteInfo, error)
ads.ParseNetID(value) ([6]byte, error)
ads.FormatNetID(value [6]byte) string

ads.ConnectionTransportAuto
ads.ConnectionTransportTCP
ads.ConnectionTransportUnix

(*Connection).GetEnum(typeName string) (EnumInfo, error)

(RouterClient).Diagnostics() RouteDiagnostics
(RouterClient).StateSnapshot() (RouterState, bool, time.Time)
(RouterClient).LocalNetID() (string, error)
(RouterClient).RegisterPort(requestedPort uint16) (AmsAddress, error)
(RouterClient).UnregisterPort(port uint16) error

(*Connection).GetHandle[T](symbolName) (*Handle[T], error) // package function retained
(*Handle[T]).Read() (T, error)
(*Handle[T]).Write(value T) error

ads.NewBatchReader[S](conn, handles...) (*BatchReader[S], error)
(*BatchReader[S]).Read(*S) error
ads.NewBatchWriter[S](conn, handles...) (*BatchWriter[S], error)
(*BatchWriter[S]).Write(S) error

(*Handle[T]).Subscribe(chan<- Update[T], *SubscribeOptions) (*Subscription, error)
(*Subscription).Cancel() error
(*Subscription).Err() error
(*Subscription).Dropped() uint64
(*Subscription).Retry() error

(*Connection).GenerateType(symbolName string) (string, error)
(*Connection).GenerateStructBody(symbolName string) (string, error)
(*Connection).AnalyzeType(symbolName string) (*GeneratedTypeInfo, error)
```

## RPC implementation

- `metadata_rpc.go` parses RPC methods, ordered parameters, attributes and type GUIDs through the shared datatype-tail parser.
- `binding.go` owns typed symbol/batch rebinding; `ads.go` owns deduplicated named handles for symbols and methods. Lifecycle reset invalidates that table; Close releases its handles.
- `rpc.go`: `Connection.BindRPC[I,O]`, `RPC.Call(ctx,input)`, `RPCMethods`, `RPCOptions.Signature`, `ErrRPCSignatureChanged`, `UnsupportedRPCError`.
- `codegen_rpc.go` emits named clients, records and shared type dependencies. File formatting/collision checks and enums live in the library, used by the CLI's `-rpc=instance=Client` option.
- RPC records reuse existing value codecs via `rpc_values.go`. Fixed references are values; length-linked buffers are slices. Counts must be input-only integers, indexed one-based in the complete method parameter list. Pointer widths never determine pointee widths. Calls are never retried.
- Caller cancellation reaches generation admission, shared handle acquisition and requests. Do not add goroutines solely to wait on locks.
- `examples/rpc` contains generated Go and PLC sources. The golden metadata is synthetic; it does not establish real PLC ABI validation.

## Verified behavior

- `Connect()` uploads/caches symbol and datatype metadata.
- Symbol handles are resolved lazily via handle-by-name request path.
- `Handle.Read/Write` use `GroupSymbolValueByHandle (0xF005)`.
- `WriteControl` helpers are available for ADS/device state transitions.
- `GroupSymbolVersion` is monitored; version changes trigger metadata reload, handle invalidation, and subscription restoration.
- AMS/TCP router note frames are parsed and exposed through route diagnostics helpers.
- `SumRead`/`SumWrite` are sent through `ReadWrite` with `GroupSumupRead (0xF080)` and `GroupSumupWrite (0xF081)`.
- A shared cached codec validates primitive/time types, struct fields, array shapes, offsets and byte widths at acquisition and rebinding.
- Array encode/decode uses compiled ordered layouts, including negative lower bounds and multidimensional arrays.
- Type mismatch errors in encode/decode include symbol name, Go type, and ADS type.
- Notifications encode `CycleTime` and `MaxDelay` as 100ns ADS ticks.
- Max sum command count is fixed at 500.
- Route helper support includes UDP-based NetID discovery and credentialed PLC route creation.
- Local Linux AMS transport is configurable: auto mode uses `/run/ams/tcsyssrv.ams.sock` when present and falls back to TCP; callers can force TCP or Unix socket transport through `ConnectionOptions`.

## Current validation assets

- Focused root tests cover reconnect retry/recovery, restore replay flow, unknown-notification cleanup policy behavior, notification timing, sum parser behavior, shared command response parsing, array lower-bound handling, nested array field offsets, and time-handle type validation.
- `examples/simple/main.go` is the live PLC smoke harness (12 tests) and is intended for integration verification only. Includes whole-array-of-structs read/write coverage.
- `examples/add_route/main.go` is the route-helper smoke harness for NetID discovery and credentialed PLC route creation.

## Open work

- Immediate focus: run the RPC fixture and rerun live PLC smoke and exercise reconnect/online schema changes on a real target; offline socket and race regression gates cover the hardened implementation.
- Deferred extras: mutable/output-only length links, unbounded/nested pointers, direct RPC array-dimension flags and custom packing. See `RPC_CHECKPOINTS.md` for the baseline and extended hardware gates.

## Core style and implementation patterns

- Locality of Behavior: keep lifecycle/reconnect behavior in lifecycle-specific code; avoid spreading reconnect conditionals across command files.
- Data-driven design: represent replayable runtime state as explicit specs (e.g., subscription specs) and reconstruct behavior from data after reconnect.
- Semantic compression: consolidate protocol framing/parsing into shared helpers and typed packet structs; avoid duplicated byte-layout logic.
- Options-based API: `NewConnection` uses a `ConnectionOptions` struct; `local` is inferred from the target NetID, source address defaults to router-assigned, and transport can be auto/TCP/Unix socket. Prefer options structs for new APIs with multiple parameters.

## Reference projects

- `ads-client`: TypeScript client with reconnect/subscription restore behavior and unknown-notification cleanup policy.
- `pyads`: Python helpers for route creation and NetID discovery over UDP system service.
- `ADS`: Beckhoff AdsLib reference for route/admin command semantics and protocol framing patterns.

Use these projects for protocol guidance and behavior comparisons, while keeping this repository pure-Go and aligned to local design patterns above.

## Editing guidance

- Keep type mapping in `types.go`.
- Keep symbol topology/parsing in `symbols.go`.
- Keep command wire details in `command*.go`; use shared parsing/validation helpers where protocol framing overlaps.
- Preserve exported API shape unless intentionally making a breaking change.
- Human requests can be directionally useful but not always technically correct; validate assumptions, think through implications, and suggest better alternatives before implementing.

## Decision protocol

- Validate: confirm request assumptions against code, protocol behavior, and existing style constraints.
- Propose: if a better option exists, present a concise recommendation and tradeoff before coding.
- Implement: once direction is clear, implement the smallest coherent change that preserves locality and semantic compression.
