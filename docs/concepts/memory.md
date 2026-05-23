# Memory

Oasis agents are stateless by default. The memory system is opt-in — enable it to give agents structured long-term memory for facts, notes, events, playbooks, reflections, and summaries. Conversation history, cross-thread recall, semantic trimming, compaction, and per-turn compression are all configured through the same `WithMemory` entry point (see [below](#conversation-history-and-withmemory)).

## What the Redesign Solves

The old `WithUserMemory` / `MemoryStore` / `core.Fact` API was narrowly scoped to user facts with a fixed confidence model. The redesign replaces it with a single `MemoryItem` type discriminated by `Kind` — one store, one pipeline, one retrieval path — covering every memory category an agent might need. The ingest and retrieve pipelines are composable: you slot in processors (extraction, deduplication, embedder, upserter, decay) rather than inheriting a fixed behaviour. Agent-callable memory tools (`memory.remember`, `memory.recall`, `memory.forget`, `memory.pin`) are opt-in, so agents only get memory access when you explicitly wire it.

## The `MemoryItem` Shape

```go
type MemoryItem struct {
    ID        string
    Kind      Kind      // discriminator (see table below)
    Content   string    // canonical text — used for embedding, display, LLM consumption
    Scope     Scope     // visibility partition (see Scope section)
    Source    Source    // provenance — where this item came from
    Pinned    bool      // pinned items are always loaded into context
    Tags      []string  // arbitrary labels for filtering
    Embedding []float32 // backfilled by Embedder ingest processor when absent
    CreatedAt int64     // unix seconds
    UpdatedAt int64
    ExpiresAt int64     // 0 = never
}
```

### Kind

`Kind` is an open string type — the framework defines six canonical values, but you can define your own (e.g. `"decision"`, `"hypothesis"`, `"todo-event"`):

| Kind | Constant | Purpose |
|------|----------|---------|
| `fact` | `memory.KindFact` | Semantic fact about the user or world |
| `note` | `memory.KindNote` | Working memory scratchpad |
| `event` | `memory.KindEvent` | Episodic event (happened at a specific time) |
| `playbook` | `memory.KindPlaybook` | Procedural memory ("when X, do Y") |
| `reflection` | `memory.KindReflection` | Agent self-critique |
| `summary` | `memory.KindSummary` | Hierarchical compaction summary |

## Scope

`Scope` anchors a `MemoryItem` to a visibility partition:

```go
type Scope struct {
    Kind ScopeKind
    Ref  string   // the specific instance (user ID, thread ID, agent name, etc.)
}

// Shorthand constructor:
memory.Scoped(memory.ScopeResource, "user_123")
```

| `ScopeKind` | Constant | When to use |
|-------------|----------|-------------|
| `thread` | `memory.ScopeThread` | Visible only within one conversation thread |
| `resource` | `memory.ScopeResource` | Visible across all threads of one user or chat |
| `agent` | `memory.ScopeAgent` | Visible across all users for this agent |
| `global` | `memory.ScopeGlobal` | Visible to every agent |

The default scope when not specified is `ScopeResource` with an empty `Ref` (the framework fills the Ref from the task's user/chat context when available).

## Provenance via Source

`Source` records where a `MemoryItem` came from. This powers "where did I learn this?" queries and bulk deletion by source:

```go
type Source struct {
    Kind    string // "message" | "tool" | "user" | "agent" | "extraction"
    Ref     string // foreign key (message ID, tool call ID, etc.) — may be empty
    AgentID string // which agent created or extracted this
}
```

## The Two Pipelines

Memory flows through two composable pipelines: **ingest** (write path) and **retrieve** (read path). Each pipeline is a slice of processors that run in order.

### Ingest Pipeline

Runs in the background after each agent turn. Default chain (in order):

1. `EnsureThread` — creates the thread row if it doesn't exist
2. `PersistMessages` — writes user + assistant messages to the conversation store
3. `FactExtractor` — uses the agent's LLM to extract `KindFact` items from the conversation turn (skipped when no LLM provider is configured)
4. `Deduper` — finds semantically similar existing facts and merges them (cosine > 0.85 = reinforce, 0.80 = supersedes)
5. `Embedder` — backfills missing embeddings (skipped when no embedding provider)
6. `Upserter` — writes items to the `ItemStore`
7. `TitleGenerator` — generates a thread title on the first turn (when `WithAutoTitle()` is set and an LLM provider is configured)
8. `DecayProbabilistic` — runs decay (~5% probability per turn)
9. User-appended processors (from `memory.WithIngestProcessors(...)`)

### Retrieve Pipeline

Runs synchronously before each LLM call. Default chain:

1. Fetch pinned items (always included)
2. Semantic search for items matching the current query (`BatchedRecall`)
3. Fetch the working-memory slot (when `WithWorkingMemory()` is enabled)
4. User-appended processors (from `memory.WithRetrieveProcessors(...)`)

## Default Behavior (Passive Auto-Extract)

By default, `WithMemory` auto-extracts `KindFact` items from each conversation turn — no extra configuration needed. The agent's own LLM identifies durable facts and persists them to the `ItemStore`. Retrieval is also automatic: before each LLM call, the most semantically relevant items are injected into the system prompt.

```go
import (
    oasis "github.com/nevindra/oasis"
    "github.com/nevindra/oasis/memory"
    "github.com/nevindra/oasis/store/sqlite"
    "github.com/nevindra/oasis/provider/gemini"
)

store := sqlite.New("oasis.db")
store.Init(ctx)

embedding := gemini.NewEmbedding("key", "gemini-embedding-001", 1536)

agent := oasis.NewLLMAgent("assistant", "Helpful assistant", llm,
    oasis.WithMemory(
        memory.WithStore(store),
        memory.WithEmbedding(embedding),
    ),
)
```

The agent now automatically extracts facts from conversations and recalls relevant ones before each response. No other configuration is required for basic use.

## Unified memory configuration

`WithMemory` is the single entry point for every memory feature. Conversation history, cross-thread recall, compaction, semantic trimming, auto-titling, and structured `MemoryItem` storage all share one option list:

```go
agent := oasis.NewLLMAgent("assistant", "Helpful assistant", llm,
    oasis.WithMemory(
        memory.WithStore(store),
        memory.WithEmbedding(embedding),
        memory.WithMaxHistory(30),
        memory.WithSemanticRecall(),
        memory.WithCompaction(compactor, 0.80),
        memory.WithRecallKinds(memory.KindFact, memory.KindPlaybook),
    ),
)
```

`WithMemory` owns: conversation-history loading and persistence, cross-thread message recall, semantic trimming, per-thread compaction, per-turn compression, thread title generation, `MemoryItem` ingest (fact extraction, deduplication, embedder), `MemoryItem` retrieval (pinned items, semantic recall, working memory), and agent-callable memory tools.

See [concepts/compaction.md](compaction.md) for the full compaction deep-dive.

## Opting into Agent-Callable Tools

By default the agent cannot read or write memory on its own. Enable it explicitly:

```go
var mem memory.AgentMemory
mem.Init(memory.BuildConfig(
    memory.WithStore(store),
    memory.WithEmbedding(embedding),
))

agent := oasis.NewLLMAgent("assistant", "Helpful assistant", llm,
    oasis.WithMemory(
        memory.WithStore(store),
        memory.WithEmbedding(embedding),
        memory.WithTools(mem.AllTools()...),
    ),
)
```

The four tools the agent can then call:

| Tool name | What it does |
|-----------|-------------|
| `memory.remember` | Save a new `MemoryItem` (content, kind, scope, tags, pinned) |
| `memory.recall` | Semantic search over stored items (query, kind, scope, k) |
| `memory.forget` | Delete items by ID, substring match, kind, or age |
| `memory.pin` | Pin or unpin an item (pinned items are always loaded into context) |

## Developer API

`AgentMemory` also exposes a direct Go API for use outside the agent loop:

```go
// Remember — persist a single MemoryItem (embedding backfilled if empty)
err := mem.Remember(ctx, memory.MemoryItem{
    Kind:    memory.KindFact,
    Content: "User's favorite language is Go",
    Scope:   memory.Scoped(memory.ScopeResource, "user_123"),
})

// Recall — semantic search
items, err := mem.Recall(ctx, "programming preferences",
    memory.RecallKind(memory.KindFact),
    memory.RecallScope(memory.Scoped(memory.ScopeResource, "user_123")),
    memory.RecallLimit(5),
)

// Forget — delete by various specs
n, err := mem.Forget(ctx, memory.ForgetByID("item-id"))
n, err = mem.Forget(ctx, memory.ForgetByMatch("Go"))
n, err = mem.Forget(ctx, memory.ForgetByKind(memory.KindNote))
n, err = mem.Forget(ctx, memory.ForgetOlderThan(30 * 24 * time.Hour))

// Pin — always load this item into context
err = mem.Pin(ctx, "item-id", true)

// List — filtered query
items2, err := mem.List(ctx, memory.Filter{
    Kinds: []memory.Kind{memory.KindFact},
    Since: time.Now().Add(-7 * 24 * time.Hour).Unix(),
})

// Get — fetch one item by ID
item, err := mem.Get(ctx, "item-id")
```

## What Is NOT Memory

Not every piece of state belongs in `MemoryItem`. Use the right primitive:

| Data | Where it lives |
|------|---------------|
| Live task progress (tool call state, partial results) | Agent execution context — gone when the turn ends |
| Deferred / scheduled actions | `ScheduledAction` via `Store.CreateScheduledAction` |
| Conversation messages (what was said, when) | `Store.StoreMessage` / `Store.GetMessages` — accessed via `WithMemory` (history loader) |
| Document knowledge (articles, manuals, code) | `Chunk` via the ingest pipeline + `Store.SearchChunks` |
| Working scratchpad that resets per turn | `WithWorkingMemory()` slot in `AgentMemory` |

If data is live task state, it doesn't need to survive the current turn. If data is user-facing durable knowledge the agent should recall next week, put it in a `MemoryItem`.

## Backpressure and Graceful Shutdown

The ingest pipeline runs in a background goroutine per turn. A semaphore (cap 16) bounds concurrency. When all slots are busy:

1. **Lightweight fallback** — waits up to 30 seconds, then falls back to messages-only persistence (no LLM extraction, no fact upsert, no title generation).
2. **Drop** — if no slot frees within 30 seconds, the persist is dropped with an `ERROR` log.

Call `Close()` on the `AgentMemory` during shutdown to wait for in-flight goroutines:

```go
defer mem.Close()
```

## Conversation History and WithMemory

Conversation history (loading past messages before each LLM call, persisting the exchange afterward, cross-thread recall, and compaction) is configured through `WithMemory`. If you only need conversation history — not structured `MemoryItem` storage — pass just the history-related options:

```go
agent := oasis.NewLLMAgent("assistant", "Helpful assistant", llm,
    oasis.WithMemory(
        memory.WithStore(store),
        memory.WithMaxHistory(30),
        memory.WithSemanticRecall(),
    ),
)
```

See [concepts/compaction.md](compaction.md) for the full reference including semantic trimming, per-thread compaction, and context compression.

## See Also

- [Store](store.md) — persistence layer (`Store`, `ItemStore`)
- [Compaction](compaction.md) — per-thread compaction, semantic trimming, per-turn compression
- [Memory & Recall Guide](../guides/memory-and-recall.md) — practical patterns
- [API: Memory](../api/memory.md) — full `memory` package reference
