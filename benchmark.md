# Oasis Framework Benchmarks

Benchmarks measure **framework overhead only** — LLM providers are mocked with instant responses (zero latency). Every nanosecond and byte reported is the framework's tax, not LLM time.

**Environment:** Go 1.26, Linux, AMD Ryzen 7 9700X (16 threads). Results averaged over 3 runs.

## How to Run

```bash
# All benchmarks
go test -run='^$' -bench='.' -benchmem -count=3 ./agent/ ./network/ ./memory/

# Specific package
go test -run='^$' -bench='BenchmarkAgentExecute' -benchmem ./agent/

# Compare against a baseline (install benchstat: go install golang.org/x/perf/cmd/benchstat@latest)
go test -run='^$' -bench='.' -benchmem -count=5 ./agent/ > new.txt
benchstat old.txt new.txt
```

## Agent

The core agent loop: message building, tool dispatch, iteration control, streaming.

| Benchmark | ns/op | B/op | allocs/op | What it measures |
|-----------|------:|-----:|----------:|------------------|
| SingleTurn | 559 | 961 | 8 | Bare agent, no tools, one LLM call. **Baseline framework tax.** |
| WithTools/1 | 1,121 | 14,167 | 8 | Tools registered but not called. Definition-building overhead. |
| WithTools/5 | 1,175 | 14,168 | 8 | |
| WithTools/10 | 1,179 | 14,168 | 8 | |
| ToolLoop/calls=1 | 3,029 | 18,903 | 46 | Provider returns tool calls, then text. Full iteration loop. |
| ToolLoop/calls=3 | 6,878 | 20,971 | 71 | |
| ToolLoop/calls=5 | 8,588 | 22,529 | 87 | |
| DeepIteration/iters=1 | 2,266 | 6,590 | 46 | Multiple iterations before final text. Tests iteration scaling. |
| DeepIteration/iters=3 | 3,878 | 8,741 | 64 | |
| DeepIteration/iters=5 | 5,591 | 11,053 | 81 | |
| DeepIteration/iters=10 | 10,286 | 17,034 | 125 | |
| ParallelDispatch/1 | 3,137 | 18,907 | 46 | Parallel tool calls in a single iteration. Goroutine overhead. |
| ParallelDispatch/5 | 8,617 | 22,530 | 87 | |
| ParallelDispatch/10 | 13,990 | 27,579 | 130 | |
| ParallelDispatch/20 | 23,943 | 37,524 | 206 | |
| Stream | 2,312 | 2,173 | 24 | Single turn with streaming channel. |
| StreamWithToolCalls | 12,023 | 23,061 | 94 | Streaming + 3 tool calls. **Real-world hot path.** |
| Processors/1 | 578 | 961 | 8 | Pre + post processor chains. |
| Processors/3 | 579 | 961 | 8 | |
| Processors/5 | 589 | 961 | 8 | |
| LargePrompt/10KB | 622 | 1,346 | 9 | System prompt size scaling. |
| LargePrompt/50KB | 625 | 1,346 | 9 | |
| LargePrompt/100KB | 626 | 1,346 | 9 | |
| LargeInput/10KB | 567 | 961 | 8 | User input size scaling. |
| LargeInput/50KB | 573 | 961 | 8 | |
| LargeInput/100KB | 573 | 961 | 8 | |
| LargeToolResult/10KB | 6,393 | 29,164 | 46 | Tool result payload size scaling. |
| LargeToolResult/100KB | 120,167 | 125,618 | 47 | |
| LargeToolResult/1MB | 1,202,638 | 1,068,779 | 48 | |

## Network (Multi-Agent)

Router-based orchestration: tool-definition building, agent delegation, result forwarding.

| Benchmark | ns/op | B/op | allocs/op | What it measures |
|-----------|------:|-----:|----------:|------------------|
| SingleAgent | 3,723 | 20,274 | 71 | One child, one delegation. **Baseline network overhead.** |
| AgentScaling/1 | 3,911 | 20,300 | 72 | Varying child count, router picks one. |
| AgentScaling/3 | 4,227 | 20,782 | 81 | |
| AgentScaling/5 | 4,434 | 21,279 | 88 | |
| AgentScaling/10 | 5,254 | 23,094 | 107 | |
| AgentScaling/20 | 6,928 | 26,737 | 140 | |
| MultiDelegation/1 | 3,979 | 20,316 | 73 | Router delegates to N agents sequentially. |
| MultiDelegation/2 | 5,787 | 22,035 | 108 | |
| MultiDelegation/3 | 7,662 | 23,833 | 144 | |
| MultiDelegation/5 | 11,388 | 27,372 | 212 | |
| Stream | 13,792 | 58,317 | 91 | Single delegation with streaming. |
| LargeAgentOutput/10KB | 7,018 | 30,556 | 72 | Child returns large payload. |
| LargeAgentOutput/100KB | 123,043 | 127,016 | 73 | |
| BuildToolDefs/1 | 8 | 0 | 0 | Cached tool-def lookup. **Zero-alloc when membership stable.** |
| BuildToolDefs/5 | 8 | 0 | 0 | |
| BuildToolDefs/20 | 8 | 0 | 0 | |

## A2A (Agent-to-Agent Protocol)

Protocol layer overhead: JSON-RPC encoding, task lifecycle management, and binary artifact transport. Agents are mocked with instant responses, servers use the bounded in-memory task store, and round-trip measurements use real TCP sockets via httptest loopback — no simulated network latency.

A JSON+base64 loopback round trip necessarily materializes at least three payload-sized buffers (server encode ~1.33x for base64, client body+decode ~1.33x, decoded bytes 1x), so B/op for LargeArtifact is expected at ~4–6x payload size, not 1x.

| Benchmark | ns/op | B/op | allocs/op | What it measures |
|-----------|------:|-----:|----------:|------------------|
| Server_MessageSend | 2,230 | 2,016 | 34 | Handler path: decode → execute → store → encode. **Server baseline tax.** |
| RoundTrip | 35,031 | 17,621 | 196 | Full client→server loopback. Wire cost above the agent execute baseline (~559 ns). |
| RoundTrip_Stream | 139,962 | 126,563 | 339 | Streaming loopback: SSE event translation both directions. |
| RoundTrip_LargeArtifact/10KB | 169,000 | 99,307 | 215 | Binary attachment, 10 KB payload. Base64 wire encoding + decode. |
| RoundTrip_LargeArtifact/100KB | 2,021,000 | 1,198,978 | 243 | |
| RoundTrip_LargeArtifact/1024KB | 10,272,000 | 10,823,000 | 261 | |
| TaskStore | 31 | 0 | 0 | In-memory store under parallel poll. **Zero-alloc read path.** |

## Memory

Message assembly, fact storage, and recall.

| Benchmark | ns/op | B/op | allocs/op | What it measures |
|-----------|------:|-----:|----------:|------------------|
| BuildMessages/20 | 169 | 1,017 | 6 | Message assembly with conversation history. |
| BuildMessages/100 | 169 | 1,017 | 6 | |
| BuildMessages/200 | 171 | 1,017 | 6 | |
| BuildMessages/1000 | 178 | 1,017 | 6 | |
| Remember/facts=1 | 433 | 482 | 4 | Storing memory items. |
| Remember/facts=10 | 2,679 | 3,309 | 34 | |
| Remember/facts=50 | 12,065 | 16,631 | 158 | |
| Recall/items=10 | 52 | 96 | 4 | Retrieving facts (in-memory store). |
| Recall/items=100 | 52 | 96 | 4 | |
| Recall/items=500 | 52 | 96 | 4 | |

## Optimization History

### Phase 4 (current) vs Phase 3 vs Phase 2 vs Phase 1 vs pre-optimization baseline

**Phase 1** (v0.18.0): channel buffer reduction (64 → 1) + sync.Pool for signaling channel, LoopConfig pass-by-pointer, tool result copy chain reduction, TruncateStr ASCII fast path, RetrieveContext lazy map init, endIter closure inlining.

**Phase 2**: nil-channel ChatStream (zero-alloc non-streaming path), smart message pre-allocation, loopState sync.Pool, cached network tool defs, memory chain caching, AllDefinitions pre-sizing, toolNames elimination, postProcessed lazy alloc, interned iteration strings.

**Phase 3**: slog Enabled() guards, RuneCount skip when compression disabled, LoopConfig sync.Pool, BuildMessages fast path for no-memory agents, cached DispatchFunc at construction, cached method values at Init, retryProvider nil-channel fast path.

**Phase 4**: ToolResult.Content `json.RawMessage` → `string` (eliminates 4 type round-trips on tool results), `splitContentRunes` rewritten with byte-scanning (eliminates `[]rune` explosion), `rawMessageToString`/`toolContentToString` eliminated, streaming forwarder buffer 64→1 (saves 16KB per forwarder), `onceClose` moved to pooled loopState.

**Phase 5** (current): DX audit — Store interface 25→17 methods (ScheduledActionStore extraction removes 8 methods + 32 mock stubs), iteration.go `iterEndParams` copy elimination (-30 lines), LLM call 3-way branch collapsed, `TextContent` identity function removed, `JSONResult` generic, `WithSemanticTrimming` wired to actual implementation, `WithDecayInterval` stub removed, `RestartOnFail` gains backoff delay, `classifyAgent` correctly returns `KindUnknown` for custom agents, `agentTool.ExecuteRaw` error protocol fixed. Zero agent/memory allocation regression; network allocs -26% from Store interface shrink.

**Agent highlights (memory):**

| Benchmark | Baseline B/op | Phase 1 B/op | Phase 2 B/op | Phase 4 B/op | Total change |
|-----------|------------|-----------|-----------|-----------|-------------|
| SingleTurn | 47,856 | 30,699 | 3,122 | 961 | **-98% (49.8x)** |
| DeepIteration/iters=10 | 242,278 | 42,249 | 20,202 | 17,031 | **-93% (14.2x)** |
| ToolLoop/calls=5 | 78,476 | 43,057 | 24,521 | 22,526 | **-71% (3.5x)** |
| LargeToolResult/1MB | 13,777,170 | 9,549,248 | 9,533,579 | 1,068,790 | **-92% (12.9x)** |
| LargePrompt/100KB | 48,376 | 31,221 | 3,515 | 1,346 | **-97% (35.9x)** |
| LargeInput/100KB | 48,376 | — | 3,131 | 961 | **-98% (50.3x)** |
| Stream | — | — | 40,591 | 2,173 | **-95% (18.7x)** |

**Agent highlights (speed):**

| Benchmark | Baseline ns/op | Phase 1 ns/op | Phase 2 ns/op | Phase 4 ns/op | Total change |
|-----------|-------------|------------|------------|------------|-------------|
| SingleTurn | 4,500 | 3,370 | 1,044 | 536 | **-88% (8.4x)** |
| DeepIteration/iters=10 | 39,100 | 22,060 | 12,352 | 9,776 | **-75% (4.0x)** |
| LargePrompt/100KB | — | — | 39,511 | 596 | **-98% (66.3x)** |
| LargeInput/100KB | — | — | 39,147 | 543 | **-99% (72.1x)** |
| LargeToolResult/1MB | 4,900,000 | 3,820,000 | 3,734,259 | 1,218,362 | **-75% (4.0x)** |
| Stream | — | — | 7,582 | 2,298 | **-70% (3.3x)** |
| StreamWithToolCalls | — | — | 22,782 | 11,212 | **-51% (2.0x)** |

**Agent highlights (allocs):**

| Benchmark | Phase 2 allocs | Phase 4 allocs | Change |
|-----------|---------------|---------------|--------|
| SingleTurn | 30 | 8 | **-73%** |
| ToolLoop/calls=5 | 138 | 87 | **-37%** |
| DeepIteration/iters=10 | 258 | 125 | **-52%** |
| LargePrompt/100KB | 32 | 9 | **-72%** |
| Stream | 44 | 24 | **-45%** |
| LargeToolResult/1MB | 88 | 48 | **-45%** |

**Network highlights:**

| Benchmark | Baseline B/op | Phase 1 B/op | Phase 2 B/op | Phase 5 B/op | Total change |
|-----------|------------|-----------|-----------|-----------|-------------|
| SingleAgent | 72,888 | 37,439 | 22,323 | 20,274 | **-72% (3.6x)** |
| MultiDelegation/5 | 160,203 | 51,503 | 30,066 | 27,372 | **-83% (5.9x)** |
| LargeAgentOutput/100KB | 1,427,880 | 876,584 | 861,427 | 127,016 | **-91% (11.2x)** |
| BuildToolDefs (any count) | ~2,000 | ~2,000 | 0 | 0 | **zero-alloc** |

**Memory highlights:**

| Benchmark | Phase 1 | Phase 2 | Change |
|-----------|---------|---------|--------|
| BuildMessages/20 | 207ns / 1,113B / 9 allocs | 165ns / 1,017B / 6 allocs | **-20% speed, -9% mem, -33% allocs** |

## Key Takeaways

**Baseline overhead is negligible.** A single agent turn costs ~559ns and 961B with 8 allocs — five orders of magnitude below any real LLM API call (100ms+). The framework is not your bottleneck.

**Everything scales linearly.** No hidden quadratic behavior in iterations, tool dispatch, agent scaling, or delegation chains:
- Each additional tool call: ~1.4us + ~900B
- Each additional iteration: ~1.0us + ~1.3KB
- Each additional parallel dispatch: ~1.1us
- Each additional network delegation: ~1.9us
- Each additional child agent (definitions only): ~60ns

**Processors add zero measurable overhead.** 1 vs 5 no-op processors show identical numbers — the chain dispatch is essentially free.

**Prompt/input size is now O(1).** With compression disabled (the default), large system prompts and user inputs add zero overhead — 100KB costs the same ~570ns as 0KB. The O(n) RuneCount walk is skipped entirely.

**Non-streaming path is zero-overhead.** With nil-channel ChatStream, cached DispatchFunc, and pooled LoopConfig, a non-streaming Execute allocates no channels, spawns no goroutines, and reuses dispatch closures.

**Streaming overhead is minimal.** A streaming Execute adds ~1.75us and 1.2KB over the non-streaming baseline — down from 38KB in Phase 2. Buffer-1 forwarder channels and pooled close guards eliminated the bulk of the streaming tax.

**Large payloads scale at ~1x.** A 1MB tool result costs 1.2ms and 1.04MB (~1x the payload size). The `ToolResult.Content` string type and byte-scanning `splitContentRunes` eliminated the 4× `[]rune` explosion and 4 type round-trips that previously inflated 1MB to 9.5MB.

**Network tool defs are zero-alloc.** Cached dirty-bit pattern means stable networks (no membership changes between calls) pay zero for tool definition building.

**Memory assembly is essentially free.** BuildMessages stays flat from 20 to 1000 messages (~170ns, 6 allocs). The cost lives in the store backend, not the framework.
