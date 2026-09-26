# RPC smoke fixture

Requires Go 1.27 and a TwinCAT test PLC. This harness executes PLC methods and increments a counter. It is not run by `go test`.

1. Create a PLC function block named `FB_RPC` using the declaration in `plc/FB_RPC.st`.
2. Add public methods `Add` and `Ping` under that block. Copy each file's declaration into the method declaration pane and its executable statements into the implementation pane. Keep the `TcRpcEnable` attributes.
3. Declare and call the instance shown in `plc/MAIN.st`. Build/download the PLC and start its runtime on ADS port 851. Configure the client route as usual.
4. Regenerate the client against the actual target from the repository root:

   ```bash
   (cd cmd/codegen && go run . -ip=<PLC_IP> -netid=<PLC_NETID> \
     -rpc=MAIN.rpc=RPCClient -pkg=main -o=../../examples/rpc/generated.go)
   ```

5. Build and execute the harness:

   ```bash
   (cd examples/rpc && go test ./... && go vet ./...)
   (cd examples/rpc && go run . -ip=<PLC_IP> -netid=<PLC_NETID>)
   ```

The checked-in client is generated from synthetic metadata for these declarations and is covered by a golden test. Regenerate it before the live run: TwinCAT's uploaded type names and alignment are authoritative. The harness prints method metadata and checks `Add(7,5)=12` plus a void `Ping` call. For a local router, `go generate .` regenerates both the baseline and reference clients using default instance names; both PLC fixtures must be installed.

Additional live validation before declaring RPC production-validated:

- Compare captured ADS method/parameter metadata and mixed-width request/response bytes with the offline fixtures.
- Exercise fixed IN, OUT and INOUT values, strings, enums, nested structs and arrays within structs. The reference checkpoint also supports fixed references and input-counted pointer buffers. Mutable/output-only lengths, unbounded/nested pointers and custom packing remain explicit refusals.
- Disconnect/reconnect the PLC while retaining a generated client. Compatible calls should rebind; a changed method/type layout must produce `ErrRPCSignatureChanged` without invoking it.
- Verify server handle cleanup on connection close. A timed-out method can already have executed; do not retry it automatically.

Generated Go is compiled offline against synthetic metadata. The PLC declarations are prepared for later TwinCAT compilation; they have not been compiled or executed on TwinCAT. No live execution is claimed by these tests.

Protocol references: [Beckhoff PLC method call](https://infosys.beckhoff.com/content/1033/tc3_adssamples_net/1046349195.html), [RPC parameter metadata](https://infosys.beckhoff.com/content/1033/tc3_ads.net/9409302283.html). The offline framing/layout fixtures were cross-checked with the [ads-client implementation](https://github.com/jisotalo/ads-client/blob/master/src/ads-client.ts); they are not captured PLC traffic.

## Reference and buffer fixture

Keep `FB_RPC` unchanged for baseline comparisons. Create a second block, `FB_RPCReferences`, from `plc/references/FB_RPCReferences.st`, then add `Increment`, `SumBuffer`, and `FillBuffer` using the corresponding declarations/implementations. Add `rpcRefs : FB_RPCReferences;` to MAIN's VAR section and `rpcRefs();` to its body.

Regenerate and run the extended smoke from this checkpoint:

```bash
(cd cmd/codegen && go run . -ip=<PLC_IP> -netid=<PLC_NETID> \
  -rpc=MAIN.rpcRefs=ReferenceClient -pkg=main -o=../../examples/rpc/references_generated.go)
(cd examples/rpc && go run . -ip=<PLC_IP> -netid=<PLC_NETID> -references)
```

This checks `Increment(41)=42`, input pointer sums and output pointer contents at lengths 0, 1 and 3. Counts are in INT elements, so length 3 transfers 6 buffer bytes on either a 32-bit or 64-bit PLC. The output-only pointer is expected to receive its invocation buffer from the RPC runtime; verify that behavior on the target, together with uploaded flags and lengths. Keep the count input-only. Do not replace an output pointer with `VAR_IN_OUT POINTER TO ...` without examining the resulting extra indirection.

See [RPC_CHECKPOINTS.md](../../RPC_CHECKPOINTS.md) for testing both revisions, evidence to capture, and the remaining ABI gates.
