# go-native-ads: TODO Plan

Only open work items are listed here. The v0.1.0 GitHub migration, offline hardening, and codegen fixes are recorded in [CHANGELOG.md](CHANGELOG.md).

## Immediate TODOs

0. Live validation of hardening and RPC
   - Rerun all PLC smoke checks after transport/codec/codegen changes.
   - Test both named checkpoints using `RPC_CHECKPOINTS.md`. Import the baseline and reference PLC fixtures, regenerate each client from a real target, and run the smoke harness.
   - Capture actual RPC metadata/frames; verify mixed widths, fixed IN/OUT/INOUT representations, compound values, reconnect, signature changes, and handle cleanup.
   - Offline RPC fixtures are synthetic. Do not call the live ABI gate complete based on those tests.
   - Exercise reconnect, online layout changes, notification overflow/cancellation, and server-side cleanup on real TwinCAT.

1. Route helper expansion
   - Add optional local route helper wrappers (add/delete local route) where protocol support is stable.
   - Add route listing helpers where router APIs are stable.

2. Live smoke expansion
   - Add an explicit opt-in reconnect smoke path to `examples/simple`.
   - Keep credentialed route smoke in `examples/add_route` and optionally add a scripted response checker.

## Deferred extras (feature parity; intentionally not short-term)

1. Extended RPC signatures: mutable/output-only length links, unbounded/nested pointers, direct RPC array-dimension flags and custom packing. Fixed references and input-counted buffers are implemented offline; hardware validation remains open.
2. Additional administrative/system commands.
   - Remote process helpers (`StartProcess`-style command wrappers where supported).
   - File service helpers (read/write/delete/find via ADS file services).
   - Registry service helpers (import/export/read operations where supported by target).
   - License/platform/system info helpers (online license info, platform ID, system ID, volume/license metadata).
   - Runtime diagnostics helpers (distributed clock diagnostics activate/deactivate/clear/print).
   - EtherCAT master discovery helpers (list masters / basic topology info).
   - Route status/list APIs (list known routes with per-route status where available).
