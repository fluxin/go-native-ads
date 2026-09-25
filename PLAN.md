# go-native-ads: TODO Plan

Only open work items are listed here.

## Immediate TODOs

0. Live validation of hardening
   - Rerun all PLC smoke checks after transport/codec/codegen changes.
   - Exercise reconnect, online layout changes, notification overflow/cancellation, and server-side cleanup on real TwinCAT.

1. Route helper expansion
   - Add optional local route helper wrappers (add/delete local route) where protocol support is stable.
   - Add route listing helpers where router APIs are stable.

2. Live smoke expansion
   - Add an explicit opt-in reconnect smoke path to `examples/simple`.
   - Keep credentialed route smoke in `examples/add_route` and optionally add a scripted response checker.

## Deferred extras (feature parity; intentionally not short-term)

1. RPC method invocation support.
2. Additional administrative/system commands.
   - Remote process helpers (`StartProcess`-style command wrappers where supported).
   - File service helpers (read/write/delete/find via ADS file services).
   - Registry service helpers (import/export/read operations where supported by target).
   - License/platform/system info helpers (online license info, platform ID, system ID, volume/license metadata).
   - Runtime diagnostics helpers (distributed clock diagnostics activate/deactivate/clear/print).
   - EtherCAT master discovery helpers (list masters / basic topology info).
   - Route status/list APIs (list known routes with per-route status where available).
