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
| SingleTurn | 3,370 | 30,699 | 37 | Bare agent, no tools, one LLM call. **Baseline framework tax.** |
| WithTools/1 | 3,490 | 30,700 | 37 | Tools registered but not called. Definition-building overhead. |
| WithTools/5 | 3,590 | 30,700 | 37 | |
| WithTools/10 | 3,690 | 30,700 | 37 | |
| ToolLoop/calls=1 | 6,120 | 35,803 | 82 | Provider returns tool calls, then text. Full iteration loop. |
| ToolLoop/calls=3 | 10,760 | 39,431 | 125 | |
| ToolLoop/calls=5 | 13,320 | 43,057 | 155 | |
| DeepIteration/iters=1 | 4,660 | 10,819 | 82 | Multiple iterations before final text. Tests iteration scaling. |
| DeepIteration/iters=3 | 8,470 | 17,450 | 139 | |
| DeepIteration/iters=5 | 12,170 | 24,240 | 194 | |
| DeepIteration/iters=10 | 22,060 | 42,249 | 331 | |
| ParallelDispatch/1 | 6,440 | 35,805 | 82 | Parallel tool calls in a single iteration. Goroutine overhead. |
| ParallelDispatch/5 | 13,240 | 43,057 | 155 | |
| ParallelDispatch/10 | 19,540 | 52,426 | 230 | |
| ParallelDispatch/20 | 34,140 | 71,283 | 369 | |
| Stream | 10,330 | 67,690 | 47 | Single turn with streaming channel. |
| StreamWithToolCalls | 26,360 | 113,430 | 144 | Streaming + 3 tool calls. **Real-world hot path.** |
| Processors/1 | 4,420 | 30,703 | 37 | Pre + post processor chains. |
| Processors/3 | 4,430 | 30,703 | 37 | |
| Processors/5 | 4,470 | 30,704 | 37 | |
| LargePrompt/10KB | 7,640 | 31,221 | 40 | System prompt size scaling. |
| LargePrompt/50KB | 22,950 | 31,221 | 40 | |
| LargePrompt/100KB | 42,330 | 31,221 | 40 | |
| LargeInput/10KB | 7,600 | 30,708 | 38 | User input size scaling. |
| LargeInput/50KB | 22,900 | 30,707 | 38 | |
| LargeInput/100KB | 42,170 | 30,708 | 38 | |
| LargeToolResult/10KB | 14,800 | 77,452 | 85 | Tool result payload size scaling. |
| LargeToolResult/100KB | 380,600 | 981,629 | 89 | |
| LargeToolResult/1MB | 3,820,000 | 9,549,248 | 101 | |

## Network (Multi-Agent)

Router-based orchestration: tool-definition building, agent delegation, result forwarding.

| Benchmark | ns/op | B/op | allocs/op | What it measures |
|-----------|------:|-----:|----------:|------------------|
| SingleAgent | 6,750 | 37,439 | 107 | One child, one delegation. **Baseline network overhead.** |
| AgentScaling/1 | 6,950 | 37,491 | 108 | Varying child count, router picks one. |
| AgentScaling/3 | 7,250 | 37,971 | 117 | |
| AgentScaling/5 | 7,580 | 38,468 | 124 | |
| AgentScaling/10 | 8,350 | 40,272 | 143 | |
| AgentScaling/20 | 10,160 | 43,910 | 176 | |
| MultiDelegation/1 | 7,260 | 37,510 | 109 | Router delegates to N agents sequentially. |
| MultiDelegation/2 | 10,150 | 40,892 | 164 | |
| MultiDelegation/3 | 13,020 | 43,984 | 219 | |
| MultiDelegation/5 | 19,030 | 51,503 | 325 | |
| Stream | 17,960 | 93,694 | 129 | Single delegation with streaming. |
| LargeAgentOutput/10KB | 13,890 | 68,219 | 110 | Child returns large payload. |
| LargeAgentOutput/100KB | 353,870 | 876,584 | 114 | |

## Memory

Message assembly, fact storage, and recall.

| Benchmark | ns/op | B/op | allocs/op | What it measures |
|-----------|------:|-----:|----------:|------------------|
| BuildMessages/20 | 207 | 1,113 | 9 | Message assembly with conversation history. |
| BuildMessages/100 | 209 | 1,113 | 9 | |
| BuildMessages/200 | 208 | 1,113 | 9 | |
| BuildMessages/1000 | 219 | 1,128 | 10 | |
| Remember/facts=1 | 397 | 481 | 4 | Storing memory items. |
| Remember/facts=10 | 2,580 | 3,308 | 34 | |
| Remember/facts=50 | 11,920 | 16,630 | 158 | |
| Recall/items=10 | 51 | 96 | 4 | Retrieving facts (in-memory store). |
| Recall/items=100 | 50 | 96 | 4 | |
| Recall/items=500 | 51 | 96 | 4 | |

## Optimization History

### v0.18.0 (current) vs pre-optimization baseline

Optimizations applied: channel buffer reduction (64 → 1) + sync.Pool for signaling channel, LoopConfig pass-by-pointer, tool result copy chain reduction, TruncateStr ASCII fast path, RetrieveContext lazy map init, endIter closure inlining.

**Agent highlights:**

| Benchmark | Before B/op | After B/op | Change |
|-----------|------------|-----------|--------|
| SingleTurn | 47,856 | 30,699 | **-36%** |
| DeepIteration/iters=10 | 242,278 | 42,249 | **-83%** |
| ToolLoop/calls=5 | 78,476 | 43,057 | **-45%** |
| LargeToolResult/1MB | 13,777,170 | 9,549,248 | **-31%** |
| LargePrompt/100KB | 48,376 | 31,221 | **-35%** |

| Benchmark | Before ns/op | After ns/op | Change |
|-----------|-------------|------------|--------|
| SingleTurn | 4,500 | 3,370 | **-25%** |
| DeepIteration/iters=10 | 39,100 | 22,060 | **-44%** |
| LargeToolResult/1MB | 4,900,000 | 3,820,000 | **-22%** |

**Network highlights:**

| Benchmark | Before B/op | After B/op | Change |
|-----------|------------|-----------|--------|
| SingleAgent | 72,888 | 37,439 | **-49%** |
| MultiDelegation/5 | 160,203 | 51,503 | **-68%** |
| LargeAgentOutput/100KB | 1,427,880 | 876,584 | **-39%** |

## Key Takeaways

**Baseline overhead is negligible.** A single agent turn costs ~3.4us and 30KB — three orders of magnitude below any real LLM API call (100ms+). The framework is not your bottleneck.

**Everything scales linearly.** No hidden quadratic behavior in iterations, tool dispatch, agent scaling, or delegation chains:
- Each additional tool call: ~1.6us
- Each additional iteration: ~2.1us + ~3.5KB
- Each additional parallel dispatch: ~1.5us
- Each additional network delegation: ~3us
- Each additional child agent (definitions only): ~160ns

**Processors add zero measurable overhead.** 1 vs 5 no-op processors show identical numbers — the chain dispatch is essentially free.

**Large payloads are the remaining cost center.** A 1MB tool result costs 3.8ms and 9.5MB (~9.5x the payload size, down from 13.7x). Further reduction requires changing internal types to avoid string/[]byte round-trips.

**Streaming adds modest overhead.** ~7us and 37KB over non-streaming for the channel setup and goroutine forwarding. Worth the UX tradeoff.

**Memory assembly is essentially free.** BuildMessages stays flat from 20 to 1000 messages (~210ns). The cost lives in the store backend, not the framework.
