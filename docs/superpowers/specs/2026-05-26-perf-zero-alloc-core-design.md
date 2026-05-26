# Performance Optimization Phase 2 — Zero-Alloc Core

**Date:** 2026-05-26
**Approach:** Nil-channel ChatStream + pooling + smart pre-allocation
**Branch:** `perf/benchmark-and-optimization`
**Predecessor:** `2026-05-26-perf-optimization-design.md` (Phase 1 — landed)

## Problem

Phase 1 reduced SingleTurn from 47,856 → 30,699 B/op (36%) and 4,500 → 3,370 ns/op (25%). The remaining overhead breaks down as:

| Source | Est. bytes | Est. allocs |
|--------|-----------|-------------|
| Goroutine stack in `core.Chat` drain goroutine | 2,000–4,000 | 1 |
| `make(chan StreamEvent, 1)` in `core.Chat` | ~360 | 1 |
| Message slice over-allocation (80 extra slots × ~128B) | ~10,000 | 0 (single alloc already counted) |
| `BuildMessages` chain construction + slice growth | 3,000–5,000 | 3–5 |
| `loopState` heap escape | ~300 | 1 |
| Small per-iteration allocs (`strconv.Itoa`, `toolNames`, `postProcessed`) | ~500 | 3–4 |
| Network `buildToolDefsLocked` fresh slice per Execute | 1,000–2,000 | 2–3 |

Target: **3x from pre-optimization baseline** → ≤15,952 B/op and ≤1,500 ns/op for SingleTurn.

## Goals

| Metric | Phase 1 result | Target | Reduction from baseline |
|--------|---------------|--------|------------------------|
| SingleTurn B/op | 30,699 | ≤14,000 | ≥71% |
| SingleTurn ns/op | 3,370 | ≤1,800 | ≥60% |
| SingleTurn allocs/op | 37 | ≤28 | ≥26% |
| DeepIteration/10 B/op | 42,249 | ≤30,000 | ≥88% (from 242K baseline) |
| Network/SingleAgent B/op | 37,439 | ≤26,000 | ≥64% (from 72K baseline) |
| Public API breaking changes | — | 0 | — |

## Non-Goals

- Changing `StreamEvent` to pointer-based channels (`chan *StreamEvent`). Deferred to a future phase.
- Changing `ChatMessage` value semantics or `ChatRequest` to pass-by-pointer.
- Adding a separate `Chatter` interface. Superseded by nil-channel approach.
- Pooling structs beyond `loopState` (diminishing returns vs complexity).

## Design

### 1. Nil channel in ChatStream

**Files:** `core/provider_helpers.go`, `core/types.go` (doc), `agent/iteration.go`, all provider implementations

**Contract change:** `Provider.ChatStream` accepts `nil` for `ch`. When `ch` is nil, the provider:
- MUST NOT send to `ch`
- MUST NOT close `ch`
- MUST still return `(ChatResponse, error)` normally

This is a semver-minor change (v0.x allows breaking changes in minor bumps).

**`core.Chat` after:**

```go
func Chat(ctx context.Context, p Provider, req ChatRequest) (ChatResponse, error) {
    return p.ChatStream(ctx, req, nil)
}
```

The `donePool` and drain goroutine are removed entirely.

**`callLLM` non-streaming branch after:**

```go
resp, err = provider.ChatStream(llmCtx, req, nil)
```

No `core.Chat` wrapper needed. The streaming branch is unchanged (passes a real channel).

**Provider migration pattern:**

```go
// Before:
func (p *myProvider) ChatStream(ctx context.Context, req ChatRequest, ch chan<- StreamEvent) (ChatResponse, error) {
    defer close(ch)
    // ... ch <- event ...
}

// After:
func (p *myProvider) ChatStream(ctx context.Context, req ChatRequest, ch chan<- StreamEvent) (ChatResponse, error) {
    if ch != nil {
        defer close(ch)
    }
    // ... if ch != nil { ch <- event } ...
}
```

**Saves:** ~2,000–4,000B + 2 allocs per non-streaming call (goroutine stack + channel allocation). Eliminates goroutine scheduling latency (~500–1,500ns).

### 2. Smart message pre-allocation

**File:** `agent/loop.go:65-77`

**Current:** Fixed formula `preAllocPer=8`, always `MaxIter × 8` extra slots.

**After:** Scale based on whether tools are registered:

```go
var preAllocCap int
if len(cfg.Tools) == 0 {
    preAllocCap = 2 // no tools: one assistant reply + margin
} else {
    preAllocCap = min(cfg.MaxIter*4, 200) // each tool iter: ~2-3 messages
}
```

**Saves:** ~6,000–8,000B for tool-less paths (SingleTurn, LargePrompt, LargeInput). ~2,000–4,000B for tool paths.

### 3. loopState pooling

**Files:** `agent/iteration.go` (loopState definition), `agent/loop.go` (acquire/release)

Package-level pool:

```go
var loopStatePool = sync.Pool{
    New: func() any { return new(loopState) },
}
```

Acquire at `runLoop` entry, release after building `AgentResult`:

```go
state := loopStatePool.Get().(*loopState)
state.reset(messages, messageRuneCount, attachByteBudget, hasAgentTools, cfg.CompressThreshold, safeCloseCh)
defer func() {
    state.clear() // nil out references, prevent retention
    loopStatePool.Put(state)
}()
```

**Lifetime safety:** `patchTerminal` copies slice data into `AgentResult` by slice assignment (sharing backing array). Before returning `loopState` to pool, `clear()` nils all slice fields. The `AgentResult` retains the backing arrays; the pool just recycles the struct shell. On next use, `reset()` assigns fresh nil slices — the old backing arrays are only GC'd when the caller drops `AgentResult`.

This is safe because:
- `loopState.steps` is assigned to `AgentResult.Steps` — caller owns the backing array
- `loopState.clear()` sets `steps = nil` — pool entry no longer references the array
- Next `reset()` starts with nil slices that grow independently

**Saves:** ~300B + 1 alloc per call. Reuse of struct shell amortizes across calls under load.

### 4. Cached tool defs in network Execute

**File:** `network/network.go`

**Current:** `buildLoopConfig` passes `buildToolDefs` as a prebuild callback, which calls `buildToolDefsLocked` on every Execute — allocating a fresh `[]ToolDefinition` even when network membership is stable. A cached path (`rebuildCachedToolDefsLocked`) exists but is only called on `AddAgent`/`RemoveAgent`.

**Fix:** Add a dirty bit (`toolDefsDirty bool`) guarded by the existing `mu`:

- `AddAgent`/`RemoveAgent` set `toolDefsDirty = true` (already rebuild here)
- `buildLoopConfig` checks: if `!toolDefsDirty`, return cached slice directly. If dirty, rebuild, cache, clear bit.
- The prebuild callback (`buildToolDefs`) becomes a no-op when the cache is valid.

**Saves:** ~1,000–2,000B + 2–3 allocs per network Execute when membership is stable.

### 5. Memory package chain caching

**Files:** `memory/retrieve.go`, `memory/memory.go`

**Current:** `defaultRetrieveChain()` and `defaultIngestChain()` allocate new processor slices on every `BuildMessages` / `PersistTurn` call. The chain composition depends only on config fields set at `Init` time.

**Fix:** Compute both chains once in `Init()` and store as fields:

```go
type AgentMemory struct {
    // ...
    retrieveChain []RetrieveProcessor
    ingestChain   []IngestProcessor
}

func (m *AgentMemory) Init(ctx context.Context) error {
    // ...
    m.retrieveChain = m.buildRetrieveChain()
    m.ingestChain = m.buildIngestChain()
}
```

**Also:** Pre-size `BuildMessages` output slice:

```go
out := make([]core.ChatMessage, 0, len(in.History)+3)
```

**Saves:** ~500B + 3–5 allocs per BuildMessages/PersistTurn call.

### 6. Small independent wins

| Fix | File | Change | Saves |
|-----|------|--------|-------|
| Eliminate `toolNames` slice | `agent/iteration.go:377` | Log tool names from `resp.ToolCalls` directly using a lazy `slog.LogValuer` or inline loop | 1 alloc per tool iteration |
| Guard `postProcessed` | `agent/iteration.go:395` | Wrap in `if cfg.OnIterationComplete != nil` | 1 alloc when hook absent |
| Pre-size `AllDefinitions()` | `core/types.go:153` | `make([]ToolDefinition, 0, len(r.tools))` | 1–2 allocs per LLM call |
| Intern iteration index strings | `agent/iteration.go` | Lookup table for `strconv.Itoa(i)` where i < 32 | 1 small alloc per iteration |

## Ordering

Changes are independent. Recommended order for review clarity and maximum early signal:

1. **Nil channel in ChatStream** (biggest single win, touches most files)
2. **Smart message pre-allocation** (one-file change, easy to verify)
3. **loopState pooling** (medium complexity, needs careful lifetime test)
4. **Network cached tool defs** (contained in one file)
5. **Memory chain caching** (contained in memory package)
6. **Small wins** (all independent, can land in any order)

## Verification

1. Capture baseline: `go test -bench=. -benchmem -count=5 ./agent/ ./network/ ./memory/ > before.txt`
2. Apply changes, run full test suite: `go test ./...`
3. Capture after: same bench command → `after.txt`
4. Compare: `benchstat before.txt after.txt`
5. All existing tests must pass. No benchmark may regress beyond noise margin.
6. CPU profile (`-cpuprofile`) before and after to validate speed claims, especially goroutine scheduling reduction.

## Risk

| Risk | Mitigation |
|------|-----------|
| Provider implementations panic on nil ch | Migration is one-line nil guard; all in-tree providers updated in the same PR |
| loopState pool returns corrupt state | `clear()` nils all references; `reset()` validates zero state. Unit test: run 1000 sequential Executes, verify no cross-contamination |
| Cached tool defs serve stale data after mutation | Dirty bit is set under the same lock as AddAgent/RemoveAgent. Existing tests cover dynamic membership |
| Speed target missed (goroutine scheduling < expected) | CPU profile before committing to target. Stretch goal, not a gate. Memory target is the hard requirement |
