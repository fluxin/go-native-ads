# AGENT.md - Verified project context

## Purpose

`go-native-ads` is a pure-Go TwinCAT ADS client (`codeberg.org/fluxin/go-native-ads`) with no C dependencies.

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

(*Connection).GetEnum(typeName string) (EnumInfo, error)

(RouterClient).Diagnostics() RouteDiagnostics
(RouterClient).StateSnapshot() (RouterState, bool, time.Time)
(RouterClient).LocalNetID() (string, error)
(RouterClient).RegisterPort(requestedPort uint16) (AmsAddress, error)
(RouterClient).UnregisterPort(port uint16) error

ads.GetHandle[T](conn, symbolName) (*Handle[T], error)
(*Handle[T]).Read() (T, error)
(*Handle[T]).Write(value T) error

ads.NewBatchReader[S](conn, handles...) (*BatchReader[S], error)
(*BatchReader[S]).Read(*S) error
ads.NewBatchWriter[S](conn, handles...) (*BatchWriter[S], error)
(*BatchWriter[S]).Write(S) error

(*Handle[T]).Subscribe(chan<- Update[T], *SubscribeOptions) (*Subscription, error)
(*Subscription).Cancel() error

(*Connection).GenerateType(symbolName string) (string, error)
(*Connection).GenerateStructBody(symbolName string) (string, error)
(*Connection).AnalyzeType(symbolName string) (*GeneratedTypeInfo, error)
```

## Verified behavior

- `Connect()` uploads/caches symbol and datatype metadata.
- Symbol handles are resolved lazily via handle-by-name request path.
- `Handle.Read/Write` use `GroupSymbolValueByHandle (0xF005)`.
- `WriteControl` helpers are available for ADS/device state transitions.
- `GroupSymbolVersion` is monitored; version changes trigger metadata reload, handle invalidation, and subscription restoration.
- AMS/TCP router note frames are parsed and exposed through route diagnostics helpers.
- `SumRead`/`SumWrite` are sent through `ReadWrite` with `GroupSumupRead (0xF080)` and `GroupSumupWrite (0xF081)`.
- Primitive and `time.Time` handles are validated at acquisition; struct/array shape validation occurs during encode/decode.
- Array encode/decode delegates per-element dispatch through `encodeField`/`decodeField`, supporting primitives, structs, and nested arrays as element types.
- Type mismatch errors in encode/decode include symbol name, Go type, and ADS type.
- Notifications encode `CycleTime` and `MaxDelay` as 100ns ADS ticks.
- Max sum command count is fixed at 500.
- Route helper support includes UDP-based NetID discovery and credentialed PLC route creation.

## Current validation assets

- Focused root tests cover reconnect retry/recovery, restore replay flow, unknown-notification cleanup policy behavior, notification timing, sum parser behavior, shared command response parsing, array lower-bound handling, nested array field offsets, and time-handle type validation.
- `examples/simple/main.go` is the live PLC smoke harness (12 tests) and is intended for integration verification only. Includes whole-array-of-structs read/write coverage.
- `examples/add_route/main.go` is the route-helper smoke harness for NetID discovery and credentialed PLC route creation.

## Open work

- Immediate focus: hardening reconnect/restore behavior with deterministic transport-failure tests.
- Deferred extras: RPC invocation support.

## Core style and implementation patterns

- Locality of Behavior: keep lifecycle/reconnect behavior in lifecycle-specific code; avoid spreading reconnect conditionals across command files.
- Data-driven design: represent replayable runtime state as explicit specs (e.g., subscription specs) and reconstruct behavior from data after reconnect.
- Semantic compression: consolidate protocol framing/parsing into shared helpers and typed packet structs; avoid duplicated byte-layout logic.
- Options-based API: `NewConnection` uses a `ConnectionOptions` struct; `local` is inferred from the target NetID, source address defaults to router-assigned. Prefer options structs for new APIs with multiple parameters.

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
