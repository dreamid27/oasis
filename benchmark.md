# Oasis Framework Benchmarks

Benchmarks measure **framework overhead only** — LLM providers are mocked with instant responses (zero latency). Every nanosecond and byte reported is the framework's tax, not LLM time.

**Environment:** Go 1.24, Linux, AMD Ryzen 7 9700X (16 threads). Results averaged over 3 runs with `-benchtime=500ms`.

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
| SingleTurn | 536 | 961 | 8 | Bare agent, no tools, one LLM call. **Baseline framework tax.** |
| WithTools/1 | 1,067 | 14,166 | 8 | Tools registered but not called. Definition-building overhead. |
| WithTools/5 | 1,080 | 14,166 | 8 | |
| WithTools/10 | 1,082 | 14,166 | 8 | |
| ToolLoop/calls=1 | 2,731 | 18,897 | 46 | Provider returns tool calls, then text. Full iteration loop. |
| ToolLoop/calls=3 | 6,688 | 20,968 | 71 | |
| ToolLoop/calls=5 | 8,374 | 22,526 | 87 | |
| DeepIteration/iters=1 | 2,229 | 6,590 | 46 | Multiple iterations before final text. Tests iteration scaling. |
| DeepIteration/iters=3 | 3,810 | 8,739 | 64 | |
| DeepIteration/iters=5 | 5,427 | 11,052 | 81 | |
| DeepIteration/iters=10 | 9,776 | 17,031 | 125 | |
| ParallelDispatch/1 | 3,037 | 18,904 | 46 | Parallel tool calls in a single iteration. Goroutine overhead. |
| ParallelDispatch/5 | 8,457 | 22,528 | 87 | |
| ParallelDispatch/10 | 13,347 | 27,572 | 130 | |
| ParallelDispatch/20 | 23,325 | 37,520 | 206 | |
| Stream | 2,298 | 2,173 | 24 | Single turn with streaming channel. |
| StreamWithToolCalls | 11,212 | 23,060 | 94 | Streaming + 3 tool calls. **Real-world hot path.** |
| Processors/1 | 549 | 961 | 8 | Pre + post processor chains. |
| Processors/3 | 559 | 961 | 8 | |
| Processors/5 | 560 | 961 | 8 | |
| LargePrompt/10KB | 594 | 1,346 | 9 | System prompt size scaling. |
| LargePrompt/50KB | 593 | 1,346 | 9 | |
| LargePrompt/100KB | 596 | 1,346 | 9 | |
| LargeInput/10KB | 544 | 961 | 8 | User input size scaling. |
| LargeInput/50KB | 542 | 961 | 8 | |
| LargeInput/100KB | 543 | 961 | 8 | |
| LargeToolResult/10KB | 5,779 | 29,165 | 46 | Tool result payload size scaling. |
| LargeToolResult/100KB | 118,095 | 125,620 | 47 | |
| LargeToolResult/1MB | 1,218,362 | 1,068,790 | 48 | |

## Network (Multi-Agent)

Router-based orchestration: tool-definition building, agent delegation, result forwarding.

| Benchmark | ns/op | B/op | allocs/op | What it measures |
|-----------|------:|-----:|----------:|------------------|
| SingleAgent | 4,179 | 22,323 | 96 | One child, one delegation. **Baseline network overhead.** |
| AgentScaling/1 | 4,237 | 22,358 | 97 | Varying child count, router picks one. |
| AgentScaling/3 | 4,518 | 22,839 | 106 | |
| AgentScaling/5 | 4,746 | 23,336 | 113 | |
| AgentScaling/10 | 5,480 | 25,140 | 132 | |
| AgentScaling/20 | 7,141 | 28,779 | 165 | |
| MultiDelegation/1 | 4,293 | 22,375 | 98 | Router delegates to N agents sequentially. |
| MultiDelegation/2 | 6,306 | 24,245 | 145 | |
| MultiDelegation/3 | 8,225 | 26,211 | 193 | |
| MultiDelegation/5 | 12,153 | 30,066 | 285 | |
| Stream | 15,687 | 78,579 | 118 | Single delegation with streaming. |
| LargeAgentOutput/10KB | 10,658 | 53,102 | 99 | Child returns large payload. |
| LargeAgentOutput/100KB | 351,893 | 861,427 | 102 | |
| BuildToolDefs/1 | 8 | 0 | 0 | Cached tool-def lookup. **Zero-alloc when membership stable.** |
| BuildToolDefs/5 | 8 | 0 | 0 | |
| BuildToolDefs/20 | 8 | 0 | 0 | |

## Memory

Message assembly, fact storage, and recall.

| Benchmark | ns/op | B/op | allocs/op | What it measures |
|-----------|------:|-----:|----------:|------------------|
| BuildMessages/20 | 165 | 1,017 | 6 | Message assembly with conversation history. |
| BuildMessages/100 | 165 | 1,017 | 6 | |
| BuildMessages/200 | 166 | 1,017 | 6 | |
| BuildMessages/1000 | 170 | 1,017 | 6 | |
| Remember/facts=1 | 410 | 481 | 4 | Storing memory items. |
| Remember/facts=10 | 2,615 | 3,308 | 34 | |
| Remember/facts=50 | 11,905 | 16,630 | 158 | |
| Recall/items=10 | 51 | 96 | 4 | Retrieving facts (in-memory store). |
| Recall/items=100 | 51 | 96 | 4 | |
| Recall/items=500 | 51 | 96 | 4 | |

## Optimization History

### Phase 4 (current) vs Phase 3 vs Phase 2 vs Phase 1 vs pre-optimization baseline

**Phase 1** (v0.18.0): channel buffer reduction (64 → 1) + sync.Pool for signaling channel, LoopConfig pass-by-pointer, tool result copy chain reduction, TruncateStr ASCII fast path, RetrieveContext lazy map init, endIter closure inlining.

**Phase 2**: nil-channel ChatStream (zero-alloc non-streaming path), smart message pre-allocation, loopState sync.Pool, cached network tool defs, memory chain caching, AllDefinitions pre-sizing, toolNames elimination, postProcessed lazy alloc, interned iteration strings.

**Phase 3**: slog Enabled() guards, RuneCount skip when compression disabled, LoopConfig sync.Pool, BuildMessages fast path for no-memory agents, cached DispatchFunc at construction, cached method values at Init, retryProvider nil-channel fast path.

**Phase 4** (current): ToolResult.Content `json.RawMessage` → `string` (eliminates 4 type round-trips on tool results), `splitContentRunes` rewritten with byte-scanning (eliminates `[]rune` explosion), `rawMessageToString`/`toolContentToString` eliminated, streaming forwarder buffer 64→1 (saves 16KB per forwarder), `onceClose` moved to pooled loopState.

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

| Benchmark | Baseline B/op | Phase 1 B/op | Phase 2 B/op | Total change |
|-----------|------------|-----------|-----------|-------------|
| SingleAgent | 72,888 | 37,439 | 22,323 | **-69% (3.3x)** |
| MultiDelegation/5 | 160,203 | 51,503 | 30,066 | **-81% (5.3x)** |
| LargeAgentOutput/100KB | 1,427,880 | 876,584 | 861,427 | **-40%** |
| BuildToolDefs (any count) | ~2,000 | ~2,000 | 0 | **zero-alloc** |

**Memory highlights:**

| Benchmark | Phase 1 | Phase 2 | Change |
|-----------|---------|---------|--------|
| BuildMessages/20 | 207ns / 1,113B / 9 allocs | 165ns / 1,017B / 6 allocs | **-20% speed, -9% mem, -33% allocs** |

## Key Takeaways

**Baseline overhead is negligible.** A single agent turn costs ~536ns and 961B with 8 allocs — five orders of magnitude below any real LLM API call (100ms+). The framework is not your bottleneck.

**Everything scales linearly.** No hidden quadratic behavior in iterations, tool dispatch, agent scaling, or delegation chains:
- Each additional tool call: ~1.4us + ~740B
- Each additional iteration: ~0.9us + ~1.2KB
- Each additional parallel dispatch: ~1.1us
- Each additional network delegation: ~2us
- Each additional child agent (definitions only): ~60ns

**Processors add zero measurable overhead.** 1 vs 5 no-op processors show identical numbers — the chain dispatch is essentially free.

**Prompt/input size is now O(1).** With compression disabled (the default), large system prompts and user inputs add zero overhead — 100KB costs the same ~540ns as 0KB. The O(n) RuneCount walk is skipped entirely.

**Non-streaming path is zero-overhead.** With nil-channel ChatStream, cached DispatchFunc, and pooled LoopConfig, a non-streaming Execute allocates no channels, spawns no goroutines, and reuses dispatch closures.

**Streaming overhead is minimal.** A streaming Execute adds ~1.8us and 1.2KB over the non-streaming baseline — down from 38KB in Phase 2. Buffer-1 forwarder channels and pooled close guards eliminated the bulk of the streaming tax.

**Large payloads scale at ~1x.** A 1MB tool result costs 1.2ms and 1.04MB (~1x the payload size). Phase 4's `ToolResult.Content` string type and byte-scanning `splitContentRunes` eliminated the 4× `[]rune` explosion and 4 type round-trips that previously inflated 1MB to 9.5MB.

**Network tool defs are zero-alloc.** Cached dirty-bit pattern means stable networks (no membership changes between calls) pay zero for tool definition building.

**Memory assembly is essentially free.** BuildMessages stays flat from 20 to 1000 messages (~167ns, 6 allocs). The cost lives in the store backend, not the framework.
