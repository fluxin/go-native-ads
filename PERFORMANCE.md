Performance validation, 2026-09-25

Matched CPU-only benchmarks compare the pre-hardening source at `dca2259b` with the hardened codec and response parser. Both runs used the same fixtures and Go 1.27.1 on Linux/amd64, AMD Ryzen 9 9900X3D, default logging, `-benchtime=500ms -count=3`. Runs were sequential. The table gives medians; allocation counts were identical across repetitions.

| Operation | Before | After | Before allocations | After allocations |
| --- | ---: | ---: | ---: | ---: |
| Decode 16 INT array elements | 404.8 ns | 106.1 ns | 184 B / 3 | 0 B / 0 |
| Decode 256 INT array elements | 13.196 µs | 1.368 µs | 2,360 B / 3 | 0 B / 0 |
| Decode 4,096 INT array elements | 311.114 µs | 21.622 µs | 32,824 B / 3 | 0 B / 0 |
| Parse 1-field sum read | 70.33 ns | 11.32 ns | 96 B / 4 | 32 B / 1 |
| Parse 100-field sum read | 1.241 µs | 0.3267 µs | 4,312 B / 103 | 3,456 B / 1 |
| Parse 500-field sum read | 5.867 µs | 1.631 µs | 20,440 B / 503 | 16,384 B / 1 |

Array measurements are warm: codec construction is performed once before timing. Cold binding intentionally performs recursive validation and allocates a reusable plan. The old CPU profile attributed 84.6% cumulative sampled CPU time to rebuilding/sorting child layouts. The new path retains ordered layouts and field indexes in the schema's codec cache. The 4,096-element warm decode is about 14.4 times faster in this fixture.

Sum reads retain capacity-limited slices of the owned response rather than allocating/copying each field. Retaining a single result can retain the complete response; callers needing only one long-lived field can clone its Data slice. Batch writes reuse one contiguous payload allocation after the first call.

These results exclude PLC/network latency, metadata upload, handle acquisition, cold codec construction, and end-to-end batch encoding. They do not claim a PLC throughput improvement. Correctness gates cover byte/value round trips, negative/multidimensional array metadata, type/width rejection, online schema replacement, stale batch binding, and generated-package compilation.

Run current benchmarks from the root:

```bash
go test -run '^$' -bench '^BenchmarkReview' -benchmem -benchtime=500ms -count=3
```

Offline validation includes root race tests, `go vet`, a local fake AMS router, the codegen module's standalone-package compilation test, and both example modules. Initial five-second fuzz runs with two workers completed 388,777 datatype cases, 318,795 symbol cases, and 427,146 notification cases without a failure. Real TwinCAT smoke/reconnect/online-change verification remains an explicit live gate in PLAN.md.
