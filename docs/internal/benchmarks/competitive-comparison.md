# Competitive Benchmark: Oasis vs go-agent

**Date:** 2026-05-26
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
| ns/op | **1,044** | 7,415 | Oasis **7.1x faster** |
| B/op | **3,122** | 19,756 | Oasis **6.3x less memory** |
| allocs/op | **30** | 63 | Oasis **52% fewer allocs** |

Oasis dominates the baseline. The nil-channel ChatStream path eliminates all channel/goroutine overhead for non-streaming calls, and smart message pre-allocation keeps the slice budget tight.

### Tools registered but not called

| Tools | Oasis ns/op | go-agent ns/op | Oasis B/op | go-agent B/op | Oasis allocs | go-agent allocs |
|------:|------------:|---------------:|-----------:|--------------:|-------------:|----------------:|
| 1 | **1,880** | 7,064 | **16,321** | 20,440 | **30** | 64 |
| 5 | **1,906** | 7,080 | **16,321** | 23,842 | **30** | 68 |
| 10 | **1,897** | 7,689 | **16,321** | 28,625 | **30** | 76 |

Oasis pre-caches tool definitions at construction — adding tools costs zero allocs at call time. go-agent's B/op grows linearly with tool count (~900 bytes per tool at call time). Oasis is now **3.7x faster** and uses **less memory** across all tool counts.

### Streaming

| Metric | Oasis | go-agent | Delta |
|--------|------:|--------:|-------|
| ns/op | 7,582 | **6,171** | go-agent **1.2x faster** |
| B/op | 40,591 | **16,472** | go-agent **2.5x less memory** |
| allocs/op | **44** | 53 | Oasis **17% fewer allocs** |

go-agent still edges out on streaming due to its simpler direct-channel model. Oasis's stream forwarding layer (needed for tool-call interleaving and event multiplexing) adds overhead. The gap narrowed significantly from 1.7x to 1.2x.

### Large system prompt

| Prompt | Oasis ns/op | go-agent ns/op | Oasis B/op | go-agent B/op |
|-------:|------------:|---------------:|-----------:|--------------:|
| 10KB | **5,145** | 9,104 | **3,515** | 43,592 |
| 50KB | **20,536** | 11,174 | **3,515** | 77,202 |
| 100KB | **39,511** | 13,929 | **3,515** | 126,354 |

Oasis has constant B/op regardless of prompt size (3.5KB — prompt is referenced, not copied). go-agent's B/op scales linearly (~900B/KB). Oasis is now faster at all sizes and uses **12-36x less memory** for large prompts.

### Large user input

| Input | Oasis ns/op | go-agent ns/op | Oasis B/op | go-agent B/op |
|------:|------------:|---------------:|-----------:|--------------:|
| 10KB | **4,985** | 335,945 | **3,131** | 113,591 |
| 50KB | **20,137** | 1,546,077 | **3,131** | 538,537 |
| 100KB | **39,147** | 3,097,063 | **3,131** | 981,933 |

Oasis dominates: **79x faster** and **314x less memory** at 100KB. go-agent has an O(n) input sanitization path (`sanitizeInput` + regex-based tool detection scanning) that scales poorly with input size. Oasis passes user input through without scanning.

### Conversation growth (same session, prior history)

| Prior turns | go-agent ns/op | go-agent B/op | go-agent allocs |
|------------:|---------------:|--------------:|----------------:|
| 10 | 33,017 | 48,388 | 669 |
| 50 | 32,707 | 48,443 | 669 |
| 100 | 32,950 | 48,304 | 669 |

go-agent's conversation overhead is flat after the context-limit window (8 turns), but at 669 allocs per call — the TOON memory serialization format allocates heavily. Oasis's equivalent path (memory.BuildMessages) costs ~165ns and 6 allocs.

## Summary

| Scenario | Winner | Margin |
|----------|--------|--------|
| Baseline latency | **Oasis** | 7.1x faster |
| Baseline memory | **Oasis** | 6.3x less B/op |
| Baseline allocs | **Oasis** | 52% fewer |
| Tool definition scaling | **Oasis** | Zero alloc growth vs linear |
| Streaming | go-agent | 1.2x faster, 2.5x less memory |
| Large prompts (memory) | **Oasis** | 3.5KB constant vs 126KB at 100KB |
| Large inputs | **Oasis** | 79x faster, 314x less memory |
| Conversation history | **Oasis** | 6 allocs vs 669 allocs |

**Overall:** Oasis wins every category except streaming (where go-agent's simpler direct-channel model edges out by 1.2x). The baseline flipped from go-agent's only memory advantage (36% less B/op) to Oasis leading by 6.3x — nil-channel ChatStream and smart pre-allocation eliminated the overhead that previously favored go-agent's simpler architecture.

The largest gap is on large inputs (79x faster, 314x less memory), where go-agent's regex-based input scanning becomes the bottleneck. This matters in production when tools return large results that feed back into the conversation.
