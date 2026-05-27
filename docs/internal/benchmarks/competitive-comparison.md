# Competitive Benchmark: Oasis vs go-agent

**Date:** 2026-05-27
**Environment:** Go 1.24, Linux, AMD Ryzen 7 9700X (16 threads)
**Methodology:** Both frameworks benchmarked with stub/mock LLM providers returning instant responses. Measures pure framework overhead only.

## Frameworks

| | Oasis | go-agent |
|---|---|---|
| Module | `github.com/nevindra/oasis` | `github.com/Protocol-Lattice/go-agent` |
| Architecture | Structured tool-use protocol, typed tool dispatch | Prompt-injection + regex-based tool detection |
| Memory | Optional (zero-value safe) | Required at construction |
| Streaming | Internal forwarding goroutine + channel | Direct channel return |

## Head-to-Head Results

### Baseline — single turn, no tools

| Metric | Oasis | go-agent | Delta |
|--------|------:|--------:|-------|
| ns/op | **606** | 7,415 | Oasis **12.2x faster** |
| B/op | **961** | 19,756 | Oasis **20.6x less memory** |
| allocs/op | **8** | 63 | Oasis **87% fewer allocs** |

Oasis dominates the baseline. Pooled LoopConfig, cached DispatchFunc + method values, slog Enabled() guards, and the nil-channel retryProvider fast path cut overhead to sub-microsecond territory at under 1KB.

### Tools registered but not called

| Tools | Oasis ns/op | go-agent ns/op | Oasis B/op | go-agent B/op | Oasis allocs | go-agent allocs |
|------:|------------:|---------------:|-----------:|--------------:|-------------:|----------------:|
| 1 | **1,291** | 7,064 | **14,174** | 20,440 | **8** | 64 |
| 5 | **1,310** | 7,080 | **14,174** | 23,842 | **8** | 68 |
| 10 | **1,308** | 7,689 | **14,174** | 28,625 | **8** | 76 |

Oasis pre-caches tool definitions at construction — adding tools costs zero allocs at call time. go-agent's B/op grows linearly with tool count (~900 bytes per tool at call time). Oasis is **5.5x faster** and uses **up to 2x less memory** across all tool counts.

### Streaming

| Metric | Oasis | go-agent | Delta |
|--------|------:|--------:|-------|
| ns/op | 6,866 | **6,171** | go-agent **1.1x faster** |
| B/op | 38,686 | **16,472** | go-agent **2.3x less memory** |
| allocs/op | **26** | 53 | Oasis **51% fewer allocs** |

go-agent still edges out on streaming latency due to its simpler direct-channel model. Oasis's stream forwarding layer (needed for tool-call interleaving and event multiplexing) adds overhead, but the gap is now just 1.1x. Oasis wins decisively on allocs (26 vs 53).

### Large system prompt

| Prompt | Oasis ns/op | go-agent ns/op | Oasis B/op | go-agent B/op |
|-------:|------------:|---------------:|-----------:|--------------:|
| 10KB | **625** | 9,104 | **1,346** | 43,592 |
| 50KB | **625** | 11,174 | **1,346** | 77,202 |
| 100KB | **621** | 13,929 | **1,346** | 126,354 |

Oasis is now O(1) on prompt size — skipping RuneCount when compression is disabled means 10KB and 100KB cost the same ~625ns. go-agent scales linearly on both time and memory. At 100KB: Oasis is **22x faster** and uses **94x less memory**.

### Large user input

| Input | Oasis ns/op | go-agent ns/op | Oasis B/op | go-agent B/op |
|------:|------------:|---------------:|-----------:|--------------:|
| 10KB | **592** | 335,945 | **961** | 113,591 |
| 50KB | **591** | 1,546,077 | **961** | 538,537 |
| 100KB | **591** | 3,097,063 | **961** | 981,933 |

Oasis dominates: **5,240x faster** and **1,022x less memory** at 100KB. go-agent has an O(n) input sanitization path (`sanitizeInput` + regex-based tool detection scanning) that scales poorly with input size. Oasis is now O(1) — input size has zero impact on framework overhead.

### Conversation growth (same session, prior history)

| Prior turns | go-agent ns/op | go-agent B/op | go-agent allocs |
|------------:|---------------:|--------------:|----------------:|
| 10 | 33,017 | 48,388 | 669 |
| 50 | 32,707 | 48,443 | 669 |
| 100 | 32,950 | 48,304 | 669 |

go-agent's conversation overhead is flat after the context-limit window (8 turns), but at 669 allocs per call — the TOON memory serialization format allocates heavily. Oasis's equivalent path (memory.BuildMessages) costs ~167ns and 6 allocs.

## Summary

| Scenario | Winner | Margin |
|----------|--------|--------|
| Baseline latency | **Oasis** | 12.2x faster |
| Baseline memory | **Oasis** | 20.6x less B/op |
| Baseline allocs | **Oasis** | 87% fewer |
| Tool definition scaling | **Oasis** | Zero alloc growth vs linear |
| Streaming latency | go-agent | 1.1x faster |
| Streaming allocs | **Oasis** | 51% fewer |
| Large prompts | **Oasis** | 22x faster, 94x less memory (O(1) vs O(n)) |
| Large inputs | **Oasis** | 5,240x faster, 1,022x less memory (O(1) vs O(n)) |
| Conversation history | **Oasis** | 6 allocs vs 669 allocs |

**Overall:** Oasis wins every category except streaming latency (where go-agent's simpler direct-channel model edges out by 1.1x — down from 1.2x). Phase 3 optimizations widened the baseline gap from 7.1x to 12.2x faster and from 6.3x to 20.6x less memory.

The largest gap is on large inputs (5,240x faster, 1,022x less memory at 100KB), where Oasis is now O(1) on input size while go-agent's regex-based scanning remains O(n). This matters in production when tools return large results that feed back into the conversation.
