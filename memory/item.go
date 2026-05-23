// memory/item.go
package memory

// Kind discriminates the role of a MemoryItem. The framework defines six
// canonical kinds below, but Kind is an open string type — users may define
// their own kinds (e.g. "decision", "hypothesis", "todo-event") and every
// pipeline, tool, and filter operates on them identically.
type Kind string

const (
	KindFact       Kind = "fact"       // semantic fact about user/world
	KindNote       Kind = "note"       // working memory scratchpad
	KindEvent      Kind = "event"      // episodic event (happened at a time)
	KindPlaybook   Kind = "playbook"   // procedural memory ("when X, do Y")
	KindReflection Kind = "reflection" // agent's self-critique
	KindSummary    Kind = "summary"    // hierarchical compaction
)

// ScopeKind is the partition kind for memory visibility.
type ScopeKind string

const (
	ScopeThread   ScopeKind = "thread"   // visible only inside one thread
	ScopeResource ScopeKind = "resource" // visible across all threads of one user/chat
	ScopeAgent    ScopeKind = "agent"    // visible across all users for this agent
	ScopeGlobal   ScopeKind = "global"   // visible to every agent
)

// Scope anchors a MemoryItem to a specific instance of a ScopeKind.
// Example: {Kind: ScopeResource, Ref: "user_123"}.
type Scope struct {
	Kind ScopeKind
	Ref  string
}

// Scoped is shorthand for Scope{Kind: k, Ref: ref}.
func Scoped(k ScopeKind, ref string) Scope { return Scope{Kind: k, Ref: ref} }

// Source records provenance — where this MemoryItem came from. Powers
// "where did I learn this" queries and forgetting by source.
type Source struct {
	Kind    string // "message" | "tool" | "user" | "agent" | "extraction"
	Ref     string // foreign key (message ID, tool call ID, etc.) — may be empty
	AgentID string // which agent created or extracted this
}

// MemoryItem is the universal record type for all memory layers.
// One struct, discriminated by Kind, covering facts, notes, events,
// playbooks, reflections, summaries, and any user-defined kinds.
//
// Content is the canonical text shown to the LLM. If a developer needs
// structured data, they JSON-encode it into Content and decode on read —
// the framework intentionally has no Data field, complying with Oasis's
// "no any at the boundary" rule.
type MemoryItem struct {
	ID        string
	Kind      Kind
	Content   string    // canonical text; used for embedding + display + LLM consumption
	Scope     Scope
	Source    Source
	Pinned    bool      // pinned items are always included in retrieval
	Tags      []string  // arbitrary labels for filtering
	Embedding []float32 // optional; backfilled by Embedder ingest processor when absent
	CreatedAt int64     // unix seconds
	UpdatedAt int64
	ExpiresAt int64     // 0 = never
}

// ScoredItem is a MemoryItem paired with a similarity score, returned
// from semantic search.
type ScoredItem struct {
	Item  MemoryItem
	Score float32 // cosine similarity, 0-1
}
