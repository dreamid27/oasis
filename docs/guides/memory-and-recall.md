# Memory and Recall

This guide covers practical patterns for wiring conversation history and structured semantic memory into your agents.

Oasis has a single, unified memory entry point:

- **`WithMemory(memory.Option...)`** — conversation history per thread, cross-thread message recall, semantic trimming, per-thread compaction, per-turn compression, auto-titling, and structured `MemoryItem` storage (facts, notes, events, playbooks, etc.) with automatic extraction, retrieval, and opt-in agent-callable tools.

Pass only the options you need.

## Conversation History

Load/persist message history per thread:

```go
import (
    oasis "github.com/nevindra/oasis"
    "github.com/nevindra/oasis/memory"
)

agent := oasis.NewLLMAgent("assistant", "Helpful assistant", llm,
    oasis.WithMemory(
        memory.WithStore(store),
    ),
)

// Must pass ThreadID for history to work
result, _ := agent.Execute(ctx, oasis.AgentTask{
    Input: "What did we just talk about?",
}.WithThreadID("thread-123"))
```

Without `ThreadID`, the agent runs stateless — no history loaded or persisted.

### History Limits

```go
// By message count (default: 10 most recent messages)
oasis.WithMemory(
    memory.WithStore(store),
    memory.WithMaxHistory(30),
)

// By estimated token budget — trim oldest-first to fit
oasis.WithMemory(
    memory.WithStore(store),
    memory.WithMaxTokens(4000),
)
```

### Semantic Trimming

By default, `MaxTokens` drops the oldest messages first. Semantic trimming instead scores messages by cosine similarity to the current query:

```go
agent := oasis.NewLLMAgent("assistant", "Helpful assistant", llm,
    oasis.WithMemory(
        memory.WithStore(store),
        memory.WithEmbedding(embedding),
        memory.WithMaxTokens(4000),
        memory.WithSemanticTrimming(),
        memory.WithKeepRecent(5),
    ),
)
```

If you want trimming to use a smaller/faster embedding model than cross-thread recall, set it separately:

```go
memory.WithSemanticTrimEmbedding(smallEmbedding),
```

### Cross-Thread Recall

Search past conversations for relevant context:

```go
agent := oasis.NewLLMAgent("assistant", "Helpful assistant", llm,
    oasis.WithMemory(
        memory.WithStore(store),
        memory.WithEmbedding(embedding),
        memory.WithSemanticRecall(),
        memory.WithSemanticRecallMinScore(0.7),
    ),
)
```

### Auto-Generated Thread Titles

```go
oasis.WithMemory(
    memory.WithStore(store),
    memory.WithAutoTitle(),
)
```

### Per-Thread Compaction

When a thread grows large, compact it into a structured 9-section summary:

```go
import "github.com/nevindra/oasis/compaction"

oasis.WithMemory(
    memory.WithStore(store),
    memory.WithMaxTokens(100_000),
    memory.WithCompaction(compaction.NewStructuredCompactor(summarizer), 0.80),
)
```

See [Compaction](../concepts/compaction.md) for the full reference.

### Per-Turn Compression

For single-execution turns that bloat on large tool results:

```go
oasis.WithMemory(
    memory.WithCompress(func(ctx context.Context, t oasis.AgentTask) oasis.Provider {
        return summarizer // or return nil to fall back to the main provider
    }, 200_000),
)
```

`WithCompress` does NOT require a Store — it operates on the in-memory slice during one `Execute` call. For long-running chat threads, prefer per-thread compaction via `WithCompaction`.

## Structured Memory (MemoryItem)

`WithMemory` also wires the `memory` package's `MemoryItem`-based system. This replaces the old `WithUserMemory` API.

### Basic Setup

```go
import (
    oasis "github.com/nevindra/oasis"
    "github.com/nevindra/oasis/memory"
    "github.com/nevindra/oasis/store/sqlite"
)

store := sqlite.New("oasis.db")
store.Init(ctx)

agent := oasis.NewLLMAgent("assistant", "Helpful assistant", llm,
    oasis.WithMemory(
        memory.WithStore(store),
        memory.WithEmbedding(embedding),
    ),
)
```

After each conversation turn, the agent automatically:
1. Extracts durable facts from the conversation using its own LLM
2. Deduplicates against existing facts (cosine > 0.85 = reinforce; 0.80 = supersedes)
3. Embeds and upserts to the `ItemStore`

Before each LLM call, it retrieves relevant facts and injects them into the system prompt.

### Custom Recall Configuration

```go
oasis.WithMemory(
    memory.WithStore(store),
    memory.WithEmbedding(embedding),
    memory.WithRecallKinds(memory.KindFact, memory.KindPlaybook),
    memory.WithRecallTopK(12),
    memory.WithSemanticRecall(),                    // cross-thread message recall
    memory.WithSemanticRecallMinScore(0.70),
)
```

### Agent-Callable Tools

By default the agent cannot read or write memory on its own. Enable it explicitly by constructing an `AgentMemory` and passing its tools:

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

The four tools available to the agent:

| Tool | What it does |
|------|-------------|
| `memory.remember` | Save a `MemoryItem` (content, kind, scope, tags, pinned) |
| `memory.recall` | Semantic search (query, kind, scope, k) |
| `memory.forget` | Delete items by ID, substring, kind, or age |
| `memory.pin` | Pin or unpin an item |

### Storing Different Kinds of Memory

```go
// Developer-side: directly remember items via AgentMemory
err := mem.Remember(ctx, memory.MemoryItem{
    Kind:    memory.KindFact,
    Content: "User's favorite language is Go",
    Scope:   memory.Scoped(memory.ScopeResource, "user_123"),
})

err = mem.Remember(ctx, memory.MemoryItem{
    Kind:    memory.KindPlaybook,
    Content: "When user asks about performance, always suggest profiling first",
    Scope:   memory.Scoped(memory.ScopeAgent, "assistant"),
})
```

### Recalling Items

```go
items, err := mem.Recall(ctx, "programming preferences",
    memory.RecallKind(memory.KindFact),
    memory.RecallScope(memory.Scoped(memory.ScopeResource, "user_123")),
    memory.RecallLimit(5),
)
for _, it := range items {
    fmt.Printf("[%.2f] %s\n", it.Score, it.Item.Content)
}
```

### Forgetting and Pinning

```go
// Forget by ID
mem.Forget(ctx, memory.ForgetByID("item-abc"))

// Forget by content substring
mem.Forget(ctx, memory.ForgetByMatch("old employer"))

// Forget items older than 30 days
mem.Forget(ctx, memory.ForgetOlderThan(30 * 24 * time.Hour))

// Pin an item — always loaded into every prompt
mem.Pin(ctx, "item-abc", true)
```

## Putting It All Together

Combine every feature in a single `WithMemory` call:

```go
agent := oasis.NewLLMAgent("assistant", "Personal assistant", llm,
    oasis.WithTools(searchTool, scheduleTool),
    oasis.WithMemory(
        memory.WithStore(store),
        memory.WithEmbedding(embedding),
        memory.WithMaxTokens(4000),
        memory.WithSemanticTrimming(),
        memory.WithKeepRecent(5),
        memory.WithSemanticRecall(),
        memory.WithSemanticRecallMinScore(0.7),
        memory.WithRecallKinds(memory.KindFact, memory.KindPlaybook),
    ),
    oasis.WithPrompt("You are a personal assistant. Use your memory of the user to give personalized responses."),
)

result, _ := agent.Execute(ctx, oasis.AgentTask{
    Input: "What are my preferences for code style?",
}.WithThreadID("thread-123").WithUserID("user-42"))
```

### What Happens During Execute

1. Embed the input (reused for both cross-thread recall and semantic trimming)
2. Load recent conversation history from the store
3. Recall relevant `MemoryItem` records (facts, playbooks, etc.)
4. Search for similar messages from past threads (cross-thread recall)
5. Build system prompt: base + memory items + cross-thread context
6. Run the tool-calling loop
7. Persist user and assistant messages
8. (Background) Extract new facts and upsert to the ItemStore

## Graceful Shutdown

Background persist goroutines run after `Execute` returns. Call `Close()` on the agent during shutdown:

```go
// On shutdown:
agent.Close()   // waits for in-flight history + ingest goroutines
```

`LLMAgent` and `Network` both expose `Close()`.

## Without Memory

Agents are fully functional without memory:

```go
agent := oasis.NewLLMAgent("worker", "Task executor", llm,
    oasis.WithTools(searchTool),
)
// No history, no recall, no fact extraction. Just tools.
```

## Migration from WithUserMemory and WithHistory

Both `WithUserMemory` and `WithHistory` are removed. Combine the old option lists into one `WithMemory` call:

```go
// Before
agent := oasis.NewLLMAgent("assistant", "...", llm,
    oasis.WithEmbedding(embedding),
    oasis.WithHistory(
        history.Store(store),
        history.MaxHistory(30),
        history.CrossThreadSearch(),
        history.Compaction(compactor, 0.80),
        history.Compress(modelFunc, 200_000),
    ),
    oasis.WithUserMemory(memoryStore),
)

// After
agent := oasis.NewLLMAgent("assistant", "...", llm,
    oasis.WithMemory(
        memory.WithStore(store),
        memory.WithEmbedding(embedding),
        memory.WithMaxHistory(30),
        memory.WithSemanticRecall(),
        memory.WithCompaction(compactor, 0.80),
        memory.WithCompress(modelFunc, 200_000),
    ),
)
```

Drop your existing `user_facts` table — data is not auto-migrated.

## See Also

- [Memory Concept](../concepts/memory.md) — MemoryItem, pipelines, tools, what is NOT memory
- [Compaction Concept](../concepts/compaction.md) — per-thread compaction, history strategies
- [Store Concept](../concepts/store.md) — persistence layer
- [API: memory](../api/memory.md) — full memory package reference
