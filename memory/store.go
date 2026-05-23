// memory/store.go
package memory

import (
	"context"

	"github.com/nevindra/oasis/core"
)

// ItemStore stores MemoryItems. Independent of core.Store, which continues
// to handle conversation messages and threads. Satellite stores in this
// repo (store/sqlite, store/postgres) implement both interfaces against
// separate tables.
type ItemStore interface {
	// Init creates any required tables/indices. Safe to call repeatedly.
	Init(ctx context.Context) error

	// Upsert writes a MemoryItem. If an item with the same ID already
	// exists, all fields except CreatedAt are overwritten and UpdatedAt
	// is set to NowUnix().
	Upsert(ctx context.Context, item MemoryItem) error

	// UpsertBatch is like Upsert for many items in one transaction.
	UpsertBatch(ctx context.Context, items []MemoryItem) error

	// Delete removes one item by ID. Returns nil if not found.
	Delete(ctx context.Context, id string) error

	// DeleteWhere removes all items matching the filter and returns the
	// count deleted. Empty filter is rejected with an error to prevent
	// accidental "delete everything".
	DeleteWhere(ctx context.Context, filter Filter) (int, error)

	// Get returns a single item by ID, or core.ErrNotFound.
	Get(ctx context.Context, id string) (MemoryItem, error)

	// List returns items matching the filter in CreatedAt-descending order
	// (or the order specified by future filter extensions). Never nil.
	List(ctx context.Context, filter Filter) ([]MemoryItem, error)

	// SearchSemantic returns up to topK items matching the filter, ranked
	// by cosine similarity to the embedding (descending). Items without
	// embeddings are skipped (not an error).
	SearchSemantic(ctx context.Context, embedding []float32, filter Filter, topK int) ([]ScoredItem, error)
}

// Filter selects MemoryItems for read or delete queries.
type Filter struct {
	Kinds      []Kind   // OR; empty = any
	Scope      *Scope   // nil = any; non-nil = exact match on Kind+Ref
	Tags       []string // AND; all tags must be present
	Pinned     *bool    // nil = any; true = only pinned; false = only unpinned
	Since      int64    // CreatedAt >= Since (0 = no lower bound)
	Until      int64    // CreatedAt <= Until (0 = no upper bound)
	Limit      int      // 0 = implementation default (50)
	IncludeExp bool     // include items where ExpiresAt > 0 AND ExpiresAt <= NowUnix() (default: false)
}

// IsEmpty reports whether the filter would match every item.
// DeleteWhere uses this to reject unbounded deletes.
func (f Filter) IsEmpty() bool {
	return len(f.Kinds) == 0 && f.Scope == nil && len(f.Tags) == 0 &&
		f.Pinned == nil && f.Since == 0 && f.Until == 0 && f.Limit == 0 && !f.IncludeExp
}

// Store is the union of core.Store (conversation history) and ItemStore
// (memory items). Satellite stores implement both; the developer passes
// one object to memory.WithStore.
type Store interface {
	core.Store
	ItemStore
}
