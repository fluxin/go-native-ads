# Changelog

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
