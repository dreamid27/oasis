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
| SingleTurn | 1,044 | 3,122 | 30 | Bare agent, no tools, one LLM call. **Baseline framework tax.** |
| WithTools/1 | 1,880 | 16,321 | 30 | Tools registered but not called. Definition-building overhead. |
| WithTools/5 | 1,906 | 16,321 | 30 | |
| WithTools/10 | 1,897 | 16,321 | 30 | |
| ToolLoop/calls=1 | 3,618 | 20,688 | 71 | Provider returns tool calls, then text. Full iteration loop. |
| ToolLoop/calls=3 | 7,868 | 22,866 | 110 | |
| ToolLoop/calls=5 | 9,639 | 24,521 | 138 | |
| DeepIteration/iters=1 | 2,861 | 8,386 | 71 | Multiple iterations before final text. Tests iteration scaling. |
| DeepIteration/iters=3 | 4,861 | 10,841 | 113 | |
| DeepIteration/iters=5 | 6,857 | 13,458 | 154 | |
| DeepIteration/iters=10 | 12,352 | 20,202 | 258 | |
| ParallelDispatch/1 | 3,701 | 20,690 | 71 | Parallel tool calls in a single iteration. Goroutine overhead. |
| ParallelDispatch/5 | 9,733 | 24,522 | 138 | |
| ParallelDispatch/10 | 14,921 | 29,812 | 211 | |
| ParallelDispatch/20 | 26,125 | 40,261 | 349 | |
| Stream | 7,582 | 40,591 | 44 | Single turn with streaming channel. |
| StreamWithToolCalls | 22,782 | 97,854 | 137 | Streaming + 3 tool calls. **Real-world hot path.** |
| Processors/1 | 1,057 | 3,123 | 30 | Pre + post processor chains. |
| Processors/3 | 1,060 | 3,123 | 30 | |
| Processors/5 | 1,073 | 3,123 | 30 | |
| LargePrompt/10KB | 5,145 | 3,515 | 32 | System prompt size scaling. |
| LargePrompt/50KB | 20,536 | 3,515 | 32 | |
| LargePrompt/100KB | 39,511 | 3,515 | 32 | |
| LargeInput/10KB | 4,985 | 3,131 | 31 | User input size scaling. |
| LargeInput/50KB | 20,137 | 3,131 | 31 | |
| LargeInput/100KB | 39,147 | 3,131 | 31 | |
| LargeToolResult/10KB | 11,141 | 62,332 | 74 | Tool result payload size scaling. |
| LargeToolResult/100KB | 371,950 | 966,458 | 77 | |
| LargeToolResult/1MB | 3,734,259 | 9,533,579 | 88 | |

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

### Phase 2 (current) vs Phase 1 vs pre-optimization baseline

**Phase 1** (v0.18.0): channel buffer reduction (64 → 1) + sync.Pool for signaling channel, LoopConfig pass-by-pointer, tool result copy chain reduction, TruncateStr ASCII fast path, RetrieveContext lazy map init, endIter closure inlining.

**Phase 2** (current): nil-channel ChatStream (zero-alloc non-streaming path), smart message pre-allocation, loopState sync.Pool, cached network tool defs, memory chain caching, AllDefinitions pre-sizing, toolNames elimination, postProcessed lazy alloc, interned iteration strings.

**Agent highlights (memory):**

| Benchmark | Baseline B/op | Phase 1 B/op | Phase 2 B/op | Total change |
|-----------|------------|-----------|-----------|-------------|
| SingleTurn | 47,856 | 30,699 | 3,122 | **-93% (15.3x)** |
| DeepIteration/iters=10 | 242,278 | 42,249 | 20,202 | **-92% (12x)** |
| ToolLoop/calls=5 | 78,476 | 43,057 | 24,521 | **-69% (3.2x)** |
| LargeToolResult/1MB | 13,777,170 | 9,549,248 | 9,533,579 | **-31%** |
| LargePrompt/100KB | 48,376 | 31,221 | 3,515 | **-93% (13.8x)** |

**Agent highlights (speed):**

| Benchmark | Baseline ns/op | Phase 1 ns/op | Phase 2 ns/op | Total change |
|-----------|-------------|------------|------------|-------------|
| SingleTurn | 4,500 | 3,370 | 1,044 | **-77% (4.3x)** |
| DeepIteration/iters=10 | 39,100 | 22,060 | 12,352 | **-68% (3.2x)** |
| LargeToolResult/1MB | 4,900,000 | 3,820,000 | 3,734,259 | **-24%** |

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

**Baseline overhead is negligible.** A single agent turn costs ~1us and 3KB — four orders of magnitude below any real LLM API call (100ms+). The framework is not your bottleneck.

**Everything scales linearly.** No hidden quadratic behavior in iterations, tool dispatch, agent scaling, or delegation chains:
- Each additional tool call: ~1.5us + ~820B
- Each additional iteration: ~1.3us + ~1.3KB
- Each additional parallel dispatch: ~1.2us
- Each additional network delegation: ~2us
- Each additional child agent (definitions only): ~60ns

**Processors add zero measurable overhead.** 1 vs 5 no-op processors show identical numbers — the chain dispatch is essentially free.

**Non-streaming path is zero-overhead.** With nil-channel ChatStream, a non-streaming Execute allocates no channels and spawns no goroutines. The streaming path still adds ~6.5us and 37KB for channel setup and goroutine forwarding.

**Large payloads are the remaining cost center.** A 1MB tool result costs 3.7ms and 9.5MB (~9.5x the payload size). Further reduction requires changing internal types to avoid string/[]byte round-trips.

**Network tool defs are zero-alloc.** Cached dirty-bit pattern means stable networks (no membership changes between calls) pay zero for tool definition building.

**Memory assembly is essentially free.** BuildMessages stays flat from 20 to 1000 messages (~165ns, 6 allocs). The cost lives in the store backend, not the framework.
