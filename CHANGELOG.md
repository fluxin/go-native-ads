# Changelog

## v0.2.0 — 2026-09-25

- Raise the minimum Go version to 1.27 and add generic `Connection.GetHandle`, `NewBatchReader`, `NewBatchWriter`, and `BindRPC` methods. Existing symbol/batch package functions remain compatible.
- Add bounded RPC metadata parsing, typed fixed-layout calls with per-call context, shared named-handle ownership and reconnect/schema signature validation. Calls are never automatically retried.
- Generate typed RPC clients, input/result records, dependent structs/enums, and void methods via `-rpc=instance=Client`. Share formatting, enum emission and collision checks between library and CLI.
- Consolidate symbol/batch binding logic and batch layout state. Reuse the codec for RPC values and empty records.
- Add exact-wire, malformed-metadata, concurrent-call, cancellation, reconnect, schema-change, and generated-client execution tests plus a PLC smoke fixture.
- Offline validation: root/CLI race tests, vet across all five modules, generated-client execution against the fake router, and 452,920 RPC fuzz cases pass.
- Add fixed reference/INOUT values and typed `TcRpcLengthIs` slices, with input-only element counts, bounded allocation, exact input lengths, and generated buffer fields. Include compound pointee codecs, independent fake-router wire checks, concurrent/reconnect tests, generated-client execution and 122,445 successful buffer fuzz cases.
- Preserve the fixed-layout implementation as `rpc-fixed-layout-checkpoint` (`2a67b775`) and the extension as `rpc-reference-checkpoint`. Live captured RPC fixtures and real TwinCAT validation remain open for both; see `RPC_CHECKPOINTS.md`.

## v0.1.0 — 2026-09-25

First release at [github.com/fluxin/go-native-ads](https://github.com/fluxin/go-native-ads). This minor release includes the module-path migration and stricter runtime validation. Requires Go 1.26 or newer.

### Migration and compatibility

- Change imports and dependencies from `codeberg.org/fluxin/go-native-ads` to `github.com/fluxin/go-native-ads`; install with `go get github.com/fluxin/go-native-ads@v0.1.0`, then run `go mod tidy`.
- Update generated ADS imports or regenerate them with the current CLI. Historical `v0.0.1`–`v0.0.6` tags retain their original Codeberg module paths and contents.
- Incompatible Go types, field widths, array shapes, out-of-range TIME values, and oversized STRING values now fail validation instead of being silently accepted.
- TOD decoding no longer applies a fixed timezone offset; values use 1970-01-01 UTC. TIME uses nonnegative `time.Duration` values through `math.MaxUint32` milliseconds.
- Notifications have bounded queues and drop new samples on overflow. Monitor `Subscription.Dropped()` and keep the consumer channel open until `Cancel()` returns.
- A zero `ReconnectPolicy` uses defaults. Nonzero policies preserve explicit boolean/jitter values; start from `DefaultReconnectPolicy()` when customizing defaults.
- Low-level sum-read field data shares the response allocation. Clone individual `Data` slices if retaining the entire response is undesirable.

### Fixes and additions

- Fixed request timeout deadlocks, transport shutdown races, router deadlines, and one-way port unregister handling.
- Serialized connection generations and schema refresh; handles and batches rebind against current metadata. Concurrent symbol acquisition is deduplicated.
- Fixed subscription restoration after schema changes and cancellation during replay. Added restoration errors/retry and ordered, bounded delivery.
- Added configurable request timeouts and frame limits, bounded metadata/notification parsing, and structured protocol errors.
- Added shared cached codec layouts, strict type validation, signed array bounds, multidimensional arrays, and consistent time notification decoding.
- Fixed codegen imports, nested structs/arrays, enums, time aliases, deterministic identifiers, and declaration collision handling. Generated packages are compiled in tests.
- Exported sum-command input/result types. Batch reads update their Go destination only after all fields succeed; PLC operations can still partially succeed.

### Validation and performance

- Offline race tests, vet, local fake-router reconnect/schema-change tests, generated-package compilation, and example builds pass.
- Parser fuzzing completed over 1.1 million cases without a failure during the hardening review.
- Matched warm CPU benchmarks measured approximately 14.4× faster 4,096-element INT decoding with zero allocations; 500-field sum parsing dropped from 503 allocations to one. See [PERFORMANCE.md](PERFORMANCE.md) for methodology and limits.
- Real PLC smoke/reconnect/online-change validation remains open; see [PLAN.md](PLAN.md). No end-to-end PLC throughput improvement is claimed.

## Historical releases

The original `v0.0.1`–`v0.0.6` tags are preserved in Git history. `v0.0.6` is the last release using the Codeberg module path.
