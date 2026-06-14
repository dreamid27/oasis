# Oasis Framework Benchmarks

Benchmarks measure **framework overhead only** — LLM providers are mocked with instant responses (zero latency). Every nanosecond and byte reported is the framework's tax, not LLM time.

**Environment:** Go 1.26.3, Linux, AMD Ryzen 7 9700X (8 cores / 16 threads). Each number is the **median over 6 runs**. Baseline measured 2026-06-14 at the v1.0.0 tag.

## How to Run

```bash
# All benchmarks (median over 6 runs)
go test -run='^$' -bench='.' -benchmem -count=6 ./agent/ ./network/ ./memory/ ./a2a/ ./core/

# Specific package
go test -run='^$' -bench='BenchmarkAgentExecute' -benchmem ./agent/

# Compare against this baseline (install benchstat: go install golang.org/x/perf/cmd/benchstat@latest)
go test -run='^$' -bench='.' -benchmem -count=6 ./agent/ > new.txt
benchstat baseline.txt new.txt
```

## Agent

The core agent loop: message building, tool dispatch, iteration control, streaming.

| Benchmark | ns/op | B/op | allocs/op | What it measures |
|-----------|------:|-----:|----------:|------------------|
| SingleTurn | 580 | 1,169 | 9 | Bare agent, no tools, one LLM call. **Baseline framework tax.** |
| WithTools/1 | 644 | 2,067 | 9 | Tools registered but not called. Definition-building overhead. |
| WithTools/5 | 669 | 2,067 | 9 | |
| WithTools/10 | 706 | 2,067 | 9 | |
| ToolLoop/calls=1 | 2,276 | 7,340 | 48 | Provider returns tool calls, then text. Full iteration loop. |
| ToolLoop/calls=3 | 6,560 | 10,335 | 73 | |
| ToolLoop/calls=5 | 8,371 | 13,237 | 88 | |
| DeepIteration/iters=1 | 2,388 | 7,342 | 48 | Multiple iterations before final text. Tests iteration scaling. |
| DeepIteration/iters=3 | 4,470 | 10,131 | 67 | |
| DeepIteration/iters=5 | 6,441 | 16,704 | 85 | |
| DeepIteration/iters=10 | 10,780 | 31,219 | 127 | |
| ParallelDispatch/1 | 2,396 | 7,342 | 48 | Parallel tool calls in a single iteration. Goroutine overhead. |
| ParallelDispatch/5 | 8,384 | 13,238 | 88 | |
| ParallelDispatch/10 | 13,634 | 23,713 | 128 | |
| ParallelDispatch/20 | 23,860 | 44,523 | 196 | |
| Stream | 2,532 | 2,382 | 25 | Single turn with streaming channel. |
| StreamWithToolCalls | 10,816 | 12,421 | 96 | Streaming + 3 tool calls. **Real-world hot path.** |
| Processors/1 | 617 | 1,170 | 9 | Pre + post processor chains. |
| Processors/3 | 598 | 1,170 | 9 | |
| Processors/5 | 620 | 1,170 | 9 | |
| LargePrompt/10KB | 637 | 1,554 | 10 | System prompt size scaling. |
| LargePrompt/50KB | 630 | 1,554 | 10 | |
| LargePrompt/100KB | 665 | 1,554 | 10 | |
| LargeInput/10KB | 586 | 1,170 | 9 | User input size scaling. |
| LargeInput/50KB | 584 | 1,170 | 9 | |
| LargeInput/100KB | 587 | 1,170 | 9 | |
| LargeToolResult/10KB | 2,481 | 7,343 | 48 | Tool result payload size scaling. **O(1) in payload.** |
| LargeToolResult/100KB | 2,583 | 7,375 | 49 | |
| LargeToolResult/1MB | 3,214 | 10,224 | 50 | |

## Network (Multi-Agent)

Router-based orchestration: tool-definition building, agent delegation, result forwarding.

| Benchmark | ns/op | B/op | allocs/op | What it measures |
|-----------|------:|-----:|----------:|------------------|
| SingleAgent | 3,328 | 8,710 | 73 | One child, one delegation. **Baseline network overhead.** |
| AgentScaling/1 | 3,233 | 8,725 | 74 | Varying child count, router picks one. |
| AgentScaling/3 | 3,597 | 9,206 | 83 | |
| AgentScaling/5 | 3,748 | 9,704 | 90 | |
| AgentScaling/10 | 4,956 | 11,517 | 109 | |
| AgentScaling/20 | 6,358 | 15,159 | 142 | |
| MultiDelegation/1 | 3,358 | 8,741 | 75 | Router delegates to N agents sequentially. |
| MultiDelegation/2 | 5,335 | 11,476 | 111 | |
| MultiDelegation/3 | 7,086 | 13,938 | 147 | |
| MultiDelegation/5 | 11,107 | 22,904 | 216 | |
| Stream | 12,621 | 46,748 | 93 | Single delegation with streaming. |
| LargeAgentOutput/10KB | 3,618 | 8,736 | 74 | Child returns large payload. **O(1)** (delegation rides the same loop). |
| LargeAgentOutput/100KB | 3,874 | 8,769 | 75 | |
| BuildToolDefs/1 | 8 | 0 | 0 | Cached tool-def lookup. **Zero-alloc when membership stable.** |
| BuildToolDefs/5 | 8 | 0 | 0 | |
| BuildToolDefs/20 | 8 | 0 | 0 | |

## A2A (Agent-to-Agent Protocol)

Protocol layer overhead: JSON-RPC encoding, task lifecycle management, and binary artifact transport. Agents are mocked with instant responses, servers use the bounded in-memory task store, and round-trip measurements use real TCP sockets via httptest loopback — no simulated network latency.

A JSON+base64 loopback round trip necessarily materializes at least three payload-sized buffers (server encode ~1.33x for base64, client body+decode ~1.33x, decoded bytes 1x), so B/op for LargeArtifact is expected at ~4–6x payload size, not 1x.

| Benchmark | ns/op | B/op | allocs/op | What it measures |
|-----------|------:|-----:|----------:|------------------|
| Server_MessageSend | 2,149 | 2,016 | 34 | Handler path: decode → execute → store → encode. **Server baseline tax.** |
| RoundTrip | 33,079 | 17,572 | 198 | Full client→server loopback. Wire cost above the agent execute baseline (~580 ns). |
| RoundTrip_Stream | 100,974 | 125,915 | 338 | Streaming loopback: SSE event translation both directions. |
| RoundTrip_LargeArtifact/10KB | 156,940 | 98,466 | 214 | Binary attachment, 10 KB payload. Base64 wire encoding + decode. |
| RoundTrip_LargeArtifact/100KB | 1,140,184 | 1,193,554 | 243 | |
| RoundTrip_LargeArtifact/1024KB | 10,436,853 | 10,739,133 | 261 | |
| TaskStore | 42 | 0 | 0 | In-memory store under parallel poll. **Zero-alloc read path.** |

## Memory

Message assembly, fact storage, and recall.

| Benchmark | ns/op | B/op | allocs/op | What it measures |
|-----------|------:|-----:|----------:|------------------|
| BuildMessages/20 | 189 | 1,017 | 6 | Message assembly with conversation history. |
| BuildMessages/100 | 220 | 1,017 | 6 | |
| BuildMessages/200 | 254 | 1,017 | 6 | |
| BuildMessages/1000 | 179 | 1,017 | 6 | |
| Remember/facts=1 | 397 | 482 | 4 | Storing memory items. |
| Remember/facts=10 | 2,578 | 3,309 | 34 | |
| Remember/facts=50 | 11,880 | 16,631 | 158 | |
| Recall/items=10 | 53 | 96 | 4 | Retrieving facts (in-memory store). |
| Recall/items=100 | 52 | 96 | 4 | |
| Recall/items=500 | 53 | 96 | 4 | |

## Tool Result Store

The in-memory `ToolResultStore` on the tool-dispatch path, pre-filled with
10,000 unexpired entries (the default cap) — the worst case for the TTL sweep.
A `nextExpiry` watermark skips the O(N) sweep entirely when nothing can have
expired, so `Put`/`Get` stay sub-microsecond even at the cap.

| Benchmark | ns/op | B/op | allocs/op | What it measures |
|-----------|------:|-----:|----------:|------------------|
| InMemoryStorePut | 383 | 261 | 2 | Store a tool result with 10,000 entries resident. |
| InMemoryStoreGet | 87 | 0 | 0 | Fetch with 10,000 entries resident. **Zero-alloc read path.** |

## Key Takeaways

**Baseline overhead is negligible.** A single agent turn costs ~580ns and ~1.2KB with 9 allocs — five orders of magnitude below any real LLM API call (100ms+). The framework is not your bottleneck. Every returned `AgentResult` owns its trace memory outright (no pooled aliasing), so results are safe to hold indefinitely.

**Everything scales linearly.** No hidden quadratic behavior in iterations, tool dispatch, agent scaling, or delegation chains:
- Each additional tool call: ~1.5us + ~1.5KB
- Each additional iteration: ~0.9us + ~2.7KB
- Each additional parallel dispatch: ~1.1us
- Each additional network delegation: ~1.9us
- Each additional child agent: ~160ns

**Registering tools is nearly free.** An agent with tools that makes no calls pays ~650ns and ~2.1KB. Memory follows actual tool usage, not configured limits.

**Processors add zero measurable overhead.** 1 vs 5 no-op processors land within noise (~600ns, identical 1,170 B / 9 allocs) — the chain dispatch is essentially free.

**Prompt/input size is O(1).** With compression disabled (the default), large system prompts and user inputs add zero overhead — 100KB costs the same ~590ns as 0KB. The O(n) RuneCount walk is skipped entirely.

**Tool-result storage is O(1).** The in-memory store's `nextExpiry` watermark skips the TTL sweep unless something can actually have expired: with 10,000 entries resident, `Put` costs ~383ns and `Get` ~87ns (zero-alloc).

**Non-streaming path is zero-overhead.** With nil-channel ChatStream, cached DispatchFunc, and pooled LoopConfig, a non-streaming Execute allocates no channels, spawns no goroutines, and reuses dispatch closures.

**Streaming overhead is minimal.** A streaming Execute adds ~2.0us and ~1.2KB over the non-streaming baseline. Buffer-1 forwarder channels and pooled close guards eliminate the bulk of the streaming tax.

**Large payloads are O(1).** A 1MB tool result costs ~3.2µs and ~10.2KB — the same order as a 10KB one, and as the bare tool-loop. The payload is never scanned (chunk decisions use byte length, chunk cuts are byte offsets backed off to rune starts) and never copied (`StepTrace.RawOutput` is a string sharing the tool result's backing memory).

**Network tool defs are zero-alloc.** Cached dirty-bit pattern means stable networks (no membership changes between calls) pay zero for tool definition building.

**Memory assembly is essentially free.** BuildMessages stays flat from 20 to 1000 messages (~180–250ns, 6 allocs). The cost lives in the store backend, not the framework.
