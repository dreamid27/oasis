# API Reference: memory package

**Import:** `github.com/nevindra/oasis/memory`

The `memory` package provides the unified `MemoryItem`-based memory system. See [concepts/memory.md](../concepts/memory.md) for the conceptual overview.

---

## Core Types

### MemoryItem

```go
type MemoryItem struct {
    ID        string
    Kind      Kind
    Content   string
    Scope     Scope
    Source    Source
    Pinned    bool
    Tags      []string
    Embedding []float32
    CreatedAt int64
    UpdatedAt int64
    ExpiresAt int64  // 0 = never
}
```

### ScoredItem

```go
type ScoredItem struct {
    Item  MemoryItem
    Score float32  // cosine similarity, 0–1
}
```

### Kind

```go
type Kind string

const (
    KindFact       Kind = "fact"
    KindNote       Kind = "note"
    KindEvent      Kind = "event"
    KindPlaybook   Kind = "playbook"
    KindReflection Kind = "reflection"
    KindSummary    Kind = "summary"
)
```

Kind is an open string type — custom values are valid and work with all pipelines, tools, and filters.

### Scope and ScopeKind

```go
type ScopeKind string

const (
    ScopeThread   ScopeKind = "thread"
    ScopeResource ScopeKind = "resource"
    ScopeAgent    ScopeKind = "agent"
    ScopeGlobal   ScopeKind = "global"
)

type Scope struct {
    Kind ScopeKind
    Ref  string
}

// Scoped is a shorthand constructor.
func Scoped(k ScopeKind, ref string) Scope
```

### Source

```go
type Source struct {
    Kind    string  // "message" | "tool" | "user" | "agent" | "extraction"
    Ref     string  // foreign key — may be empty
    AgentID string  // which agent created or extracted this
}
```

---

## ItemStore Interface

**File:** `memory/store.go`

```go
type ItemStore interface {
    Init(ctx context.Context) error
    Upsert(ctx context.Context, item MemoryItem) error
    UpsertBatch(ctx context.Context, items []MemoryItem) error
    Delete(ctx context.Context, id string) error
    DeleteWhere(ctx context.Context, filter Filter) (int, error)
    Get(ctx context.Context, id string) (MemoryItem, error)
    List(ctx context.Context, filter Filter) ([]MemoryItem, error)
    SearchSemantic(ctx context.Context, embedding []float32, filter Filter, topK int) ([]ScoredItem, error)
}
```

`DeleteWhere` rejects an empty `Filter` with an error (safeguard against accidental full deletes).

### Store (composite interface)

```go
type Store interface {
    core.Store   // conversation history, threads, messages, documents, chunks
    ItemStore    // memory items
}
```

Satellite store packages (`store/sqlite`, `store/postgres`) implement `Store` — pass one object to `memory.WithStore(store)`.

### Filter

```go
type Filter struct {
    Kinds      []Kind   // OR; empty = any kind
    Scope      *Scope   // nil = any; non-nil = exact match on Kind+Ref
    Tags       []string // AND; all tags must be present
    Pinned     *bool    // nil = any; true = only pinned; false = only unpinned
    Since      int64    // CreatedAt >= Since (0 = no lower bound)
    Until      int64    // CreatedAt <= Until (0 = no upper bound)
    Limit      int      // 0 = implementation default (50)
    IncludeExp bool     // include expired items (default false)
}

func (f Filter) IsEmpty() bool
```

---

## AgentMemory

**File:** `memory/memory.go`

`AgentMemory` orchestrates memory I/O for an `LLMAgent`. All fields are optional — a zero `AgentMemory` with no `Store` does nothing.

### Constructor

```go
func (m *AgentMemory) Init(cfg AgentMemoryConfig)
```

Typically you do not call `Init` directly — `oasis.WithMemory(opts...)` wires it for you. Use `Init` when you need access to an `AgentMemory` value for the developer API (e.g., to call `AllTools()`).

### Developer Methods

```go
// Remember persists a MemoryItem. Defaults applied:
//   - ID: generated if empty
//   - Scope: ScopeResource with empty Ref when zero
//   - Source.Kind: "user" when zero
//   - Embedding: backfilled if EmbeddingProvider is set
func (m *AgentMemory) Remember(ctx context.Context, item MemoryItem) error

// Recall returns items semantically similar to query.
func (m *AgentMemory) Recall(ctx context.Context, query string, opts ...RecallOption) ([]ScoredItem, error)

// Forget deletes items matching the spec. Returns count deleted.
func (m *AgentMemory) Forget(ctx context.Context, spec ForgetSpec) (int, error)

// Pin sets or clears the pinned flag on an item.
func (m *AgentMemory) Pin(ctx context.Context, id string, pinned bool) error

// List returns items matching the filter.
func (m *AgentMemory) List(ctx context.Context, f Filter) ([]MemoryItem, error)

// Get fetches one item by ID.
func (m *AgentMemory) Get(ctx context.Context, id string) (MemoryItem, error)

// Close waits for all background ingest goroutines to finish.
func (m *AgentMemory) Close() error
```

### RecallOption

```go
func RecallKind(k Kind) RecallOption      // filter by kind
func RecallScope(s Scope) RecallOption    // filter by scope
func RecallLimit(n int) RecallOption      // result limit (default 5)
```

### ForgetSpec

```go
type ForgetSpec struct {
    ID     string        // delete by exact ID
    Match  string        // delete by content substring
    Kind   Kind          // narrow by kind
    Older  time.Duration // delete items older than this
    Filter *Filter       // power user: pass a full Filter directly
}

// Constructors:
func ForgetByID(id string) ForgetSpec
func ForgetByMatch(s string) ForgetSpec
func ForgetByKind(k Kind) ForgetSpec
func ForgetOlderThan(d time.Duration) ForgetSpec
```

### Agent-Callable Tools

```go
func (m *AgentMemory) RememberTool() core.AnyTool  // memory.remember
func (m *AgentMemory) RecallTool() core.AnyTool     // memory.recall
func (m *AgentMemory) ForgetTool() core.AnyTool     // memory.forget
func (m *AgentMemory) PinTool() core.AnyTool        // memory.pin
func (m *AgentMemory) AllTools() []core.AnyTool     // all four above
```

Pass these to `memory.WithTools(...)` inside `oasis.WithMemory(...)` to make them available to the agent.

---

## AgentMemoryConfig

**File:** `memory/memory.go`

```go
type AgentMemoryConfig struct {
    Store     Store
    Embedding core.EmbeddingProvider
    Provider  core.Provider

    IngestProcs   []IngestProcessor
    RetrieveProcs []RetrieveProcessor

    MaxHistory       int
    MaxTokens        int
    SemanticTrimming bool

    SemanticRecall   bool
    SemanticMinScore float32
    RecallKinds      []Kind
    RecallTopK       int

    WorkingMemory      bool
    WorkingMemoryScope ScopeKind

    AutoTitle bool
    Tools     []core.AnyTool

    Logger *slog.Logger
    Tracer core.Tracer
}

func BuildConfig(opts ...Option) AgentMemoryConfig
```

---

## memory.Option (AgentOption configuration)

**File:** `memory/options.go`

Passed to `oasis.WithMemory(...)`:

| Option | Default | Description |
|--------|---------|-------------|
| `WithStore(s Store)` | nil | Bind the unified `Store` (core.Store + ItemStore) |
| `WithEmbedding(p EmbeddingProvider)` | nil | Embedding provider for recall + dedupe + working memory |
| `WithProvider(p Provider)` | nil | LLM provider for extraction and title generation |
| `WithMaxHistory(n int)` | 10 | Max recent messages loaded into LLM context |
| `WithMaxTokens(n int)` | 0 (disabled) | Token budget for history — trim oldest-first to fit |
| `WithSemanticTrimming()` | disabled | Enable semantic-similarity trimming when over MaxTokens |
| `WithSemanticRecall()` | disabled | Enable cross-thread message recall |
| `WithSemanticRecallMinScore(s float32)` | 0.60 | Cosine threshold for cross-thread recall |
| `WithRecallKinds(kinds ...Kind)` | [KindFact] | Which item kinds are included in BatchedRecall |
| `WithRecallTopK(k int)` | 8 | Total top-K for BatchedRecall |
| `WithWorkingMemory()` | disabled | Enable a markdown working-memory slot (ScopeResource by default) |
| `WithWorkingMemoryScope(s ScopeKind)` | ScopeResource | Override the default scope for working memory |
| `WithAutoTitle()` | disabled | LLM-driven thread title generation on the first turn |
| `WithTools(tools ...core.AnyTool)` | nil | Register agent-callable memory tools |
| `WithIngestProcessors(ps ...IngestProcessor)` | nil | Append user-supplied processors after the default ingest chain |
| `WithRetrieveProcessors(ps ...RetrieveProcessor)` | nil | Append user-supplied processors after the default retrieve chain |
| `WithLogger(l *slog.Logger)` | discard | Structured logger |
| `WithTracer(t core.Tracer)` | nil | OpenTelemetry tracer |

---

## Pipeline Interfaces

### IngestProcessor

```go
type IngestProcessor interface {
    Process(ctx context.Context, in *IngestContext) error
}

type IngestContext struct {
    AgentName  string
    Task       core.AgentTask
    UserText   string
    AsstText   string
    Steps      []core.StepTrace
    Candidates []MemoryItem   // working set — processors append; Upserter writes
    ThreadCreated bool
    Store      Store
    Embedding  core.EmbeddingProvider
    Provider   core.Provider
    Logger     *slog.Logger
}
```

### RetrieveProcessor

```go
type RetrieveProcessor interface {
    Process(ctx context.Context, in *RetrieveContext) error
}

type RetrieveContext struct {
    AgentName    string
    Task         core.AgentTask
    Embedding    []float32
    History      []core.Message
    Selected     map[Kind][]MemoryItem  // items by Kind, set by BatchedRecall
    Pinned       []MemoryItem
    CrossThread  []core.ScoredMessage
    SystemPrompt string
    PromptParts  []string
    Store        Store
    HistoryStore core.Store
    Embedder     core.EmbeddingProvider
    Logger       *slog.Logger
}
```

### Built-in Processors

**Ingest:**

| Processor | What it does |
|-----------|-------------|
| `EnsureThread{}` | Creates the thread row if it doesn't exist |
| `PersistMessages{}` | Writes user + assistant messages to the conversation store |
| `FactExtractor{}` | Uses the LLM to extract `KindFact` items from the conversation turn |
| `Deduper{}` | Merges semantically similar facts (cosine > 0.85 = reinforce; 0.80 = supersedes) |
| `Embedder{}` | Backfills missing embeddings via the configured EmbeddingProvider |
| `Upserter{}` | Writes `Candidates` to the ItemStore |
| `TitleGenerator{}` | Generates a thread title on the first turn |
| `DecayProbabilistic{}` | Runs confidence decay (~5% probability per turn) |

**Retrieve:**

| Processor | What it does |
|-----------|-------------|
| `EmbedInput{}` | Embeds the current user query for downstream semantic steps |
| `LoadHistory{Limit}` | Loads recent conversation messages |
| `LoadPinned{}` | Fetches all pinned items |
| `BatchedRecall{Kinds, TopK}` | Semantic search over items matching the configured kinds |
| `RecallCrossThread{MinScore}` | Cross-thread message recall |
| `TrimToBudget{Budget, Semantic}` | Trims history to the token budget (oldest-first or semantic) |

---

## See Also

- [concepts/memory.md](../concepts/memory.md) — full conceptual guide
- [guides/memory-and-recall.md](../guides/memory-and-recall.md) — practical patterns
- [api/interfaces.md](interfaces.md) — `ItemStore` in context of all interfaces
- [api/options.md](options.md) — `WithMemory` in the full agent option table
