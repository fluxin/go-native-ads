# RPC checkpoints and hardware validation

Both checkpoints are local JJ bookmarks and committed revisions. They are development checkpoints, not published releases. Go 1.27 is required. Neither has been validated on a real TwinCAT target.

| Checkpoint | Scope | Offline evidence | Live status |
| --- | --- | --- | --- |
| `rpc-fixed-layout-checkpoint` (`2a67b775`) | Typed fixed-layout RPC, generated clients, shared handles, reconnect/signature checks | Race tests, fake-router and generated-client calls, metadata fuzzing | Open |
| `rpc-reference-checkpoint` | Baseline plus fixed references and input-counted pointer/reference buffers | Exact bytes for 32/64-bit pointer metadata, compound pointees, bounds/length refusals, concurrent calls, reconnect, generated clients, buffer fuzzing | Open |

The extension is a descendant of the baseline. The original `examples/rpc/plc/FB_RPC` Add/Ping fixture is unchanged. Extra methods live in a separate `FB_RPCReferences` / `MAIN.rpcRefs` instance so the baseline can still generate its client on the same PLC.

## Prepare separate workspaces

From this repository, create two clean workspaces so generated metadata can be compared without overwriting either checkpoint:

```bash
jj workspace add --revision rpc-fixed-layout-checkpoint ../go-native-ads-rpc-baseline
jj workspace add --revision rpc-reference-checkpoint ../go-native-ads-rpc-references
```

In TwinCAT, import the block/method declarations following `examples/rpc/README.md` in the reference workspace. Compile them in TwinCAT before running; the PLC sources are prepared fixtures, not a precompiled TwinCAT project. Build/download and configure the ADS route. Record TwinCAT build, PLC architecture, runtime ADS port and project revision.

## Baseline gate

From the baseline workspace:

```bash
(cd cmd/codegen && go run . -ip=<PLC_IP> -netid=<PLC_NETID> \
  -rpc=MAIN.rpc=RPCClient -pkg=main -o=../../examples/rpc/generated.go)
(cd examples/rpc && go run . -ip=<PLC_IP> -netid=<PLC_NETID>)
```

Capture method metadata printed by the harness and the regenerated Go diff. Require `Add(7,5)=12` and successful void Ping. Then validate mixed-width fixed IN/OUT/INOUT declarations, strings, enums, nested structs and arrays against captured request/response bytes. The baseline should explicitly refuse by-reference/length-linked signatures.

## Reference gate

From the reference workspace, regenerate both clients:

```bash
(cd cmd/codegen && go run . -ip=<PLC_IP> -netid=<PLC_NETID> \
  -rpc=MAIN.rpc=RPCClient -pkg=main -o=../../examples/rpc/generated.go)
(cd cmd/codegen && go run . -ip=<PLC_IP> -netid=<PLC_NETID> \
  -rpc=MAIN.rpcRefs=ReferenceClient -pkg=main -o=../../examples/rpc/references_generated.go)
(cd examples/rpc && go run . -ip=<PLC_IP> -netid=<PLC_NETID> -references)
```

Require baseline checks plus Increment, SumBuffer and FillBuffer at lengths 0, 1 and 3. Capture raw method/datatype uploads and ADS request/response bytes for the following additional cases; add them as regression fixtures after validation:

- `VAR_IN_OUT` / `REFERENCE TO` fixed scalars, enums, strings, arrays and padded structs; confirm whether the upload gives a dereferenced type or an explicit reference type.
- Input, output and INOUT buffers; input count preceding and following buffer declarations; a count whose parameter-list index differs from its input-only index; multiple buffers sharing a count.
- INT and struct buffers with element counts greater than one; distinguish element count from byte count and pointer storage width from pointee size.
- Zero-length buffers and maximum intended operational counts; a length mismatch must fail locally before acquiring/invoking the method handle.
- 32-bit and 64-bit PLCs when available. Offline fixtures cover both metadata widths but are not a substitute for these captures.

For both checkpoints, retain a client across a disconnect/reconnect and an online change. Compatible metadata must rebind; a changed method or nested layout must fail before invocation. Observe server handle cleanup on Close and test timeout/cancellation without retrying the PLC operation. A timed-out call may have executed.

## Implemented buffer contract and remaining restrictions

`TcRpcLengthIs` links use one-based indexes in the complete parameter list, with zero meaning no link. Attribute names are resolved when an index is absent; conflicting name/index metadata is rejected. Counts are elements of the resolved pointee type (`PVOID` uses bytes). Count fields remain explicit in generated inputs. Input slice length must match exactly; output slices are freshly allocated to the requested count. INOUT values are returned, not written into caller inputs.

Only input-only integer counts are accepted. Mutable or output-only counts, pointer returns, nested/unbounded pointers, direct RPC array-dimension flags and custom packing remain unsupported. No raw process addresses are exposed as a substitute for missing value metadata. An ordinary `POINTER TO T` without a count is still refused; a fixed reference is represented as a value.

The size/index rules were cross-checked against Beckhoff's published [ADS .NET package 6.2.521](https://www.nuget.org/packages/Beckhoff.TwinCAT.Ads/6.2.521), specifically RPC marshalling and reference-size handling. Public protocol context: [PLC method calls and TcRpcLengthIs](https://infosys.beckhoff.com/content/1033/tc3_adssamples_net/1046349195.html), [parameter length indexes](https://infosys.beckhoff.com/content/1033/tc3_ads.net/9409307403.html). These establish implementation guidance; captured PLC traffic and successful TwinCAT execution remain the acceptance gate.
