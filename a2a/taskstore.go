package a2a

import (
	"container/list"
	"context"
	"encoding/json"
	"sync"

	"github.com/nevindra/oasis/core"
)

// resumable matches *agent.ErrSuspended without importing agent/ —
// depend on behavior, not implementation.
type resumable interface {
	Resume(ctx context.Context, data json.RawMessage) (core.AgentResult, error)
	ResumeStream(ctx context.Context, data json.RawMessage, ch chan<- core.StreamEvent) (core.AgentResult, error)
}

// TaskRecord is the server-side record for one task: the serializable protocol
// state plus process-bound runtime handles. It is the unit a TaskStore persists
// and serves.
//
// # Serialization contract
//
// Task is the serializable protocol state — a persistent store marshals and
// unmarshals exactly that field and nothing else. The unexported runtime fields
// (the resume closure, the cancel handle, the registered webhook config) are
// process-bound: they are live only inside the process that created them and are
// zero in any record a store reconstructs from durable storage after a restart.
// This is precisely why a suspended run cannot resume after a process restart —
// the resume closure does not survive (see TaskStore, which documents the
// limitation). A store reconstructing a record from durable state constructs it
// as &TaskRecord{Task: t}; the runtime fields stay zero, which the server
// tolerates (a recovered task is visible and cancelable, just not resumable).
//
// # Same-instance-while-live contract (CRITICAL for custom stores)
//
// For a LIVE (non-terminal) task the server mutates the SAME *TaskRecord
// instance it passed to Save — it advances Status, attaches artifacts, and
// clears the cancel handle in place under the record's lock. A store MUST return
// that same instance from Get and List for as long as the task is live;
// otherwise a poller or resubscriber re-fetching the record would read a stale
// copy and never observe progress (handleResubscribe re-Gets on every tick for
// exactly this reason). The standard pattern for a persistent store is an
// in-memory overlay of live records layered over the durable backend: serve the
// live *TaskRecord from the overlay, persist Task to the backend on each Save,
// and fall back to a freshly constructed &TaskRecord{Task: t} only for
// terminal/restart-recovered tasks that are no longer being mutated.
//
// Lock order: always acquire m.mu (memoryStore) before e.mu (TaskRecord).
// TaskRecord methods never call back into the store, so the reverse order
// (e.mu then m.mu) cannot occur — no deadlock cycle is possible.
type TaskRecord struct {
	mu   sync.Mutex
	Task Task

	// resume is non-nil while the task is input-required. Single-use.
	resume resumable
	// cancel aborts the in-flight Execute. Non-nil while working.
	cancel context.CancelFunc
	// push is the registered webhook config, if any.
	push *PushNotificationConfig
}

// TaskStore persists A2A tasks between protocol requests. It is the extension
// point behind WithTaskStore: implement it to back task state with durable
// storage (e.g. SQL, Redis) instead of the bounded in-memory default.
//
// The in-memory default supports the full protocol including resume of
// input-required tasks. Custom persistent implementations preserve task
// visibility across restarts, but suspended runs cannot resume after a
// process restart (the resume closure is process-bound — see TaskRecord).
//
// Implementations must honor the same-instance-while-live contract documented on
// TaskRecord: while a task is non-terminal, Get and List must return the very
// *TaskRecord that was last Saved (the server mutates it in place), not a copy.
// Implementations must be safe for concurrent use.
type TaskStore interface {
	// Save inserts or replaces the record keyed by rec.Task.ID. Persistent
	// stores marshal rec.Task; the runtime fields are not serializable.
	Save(ctx context.Context, rec *TaskRecord) error
	// Get returns the record or an error satisfying errors.Is(_, ErrTaskNotFound).
	// For a live task it must return the same instance previously Saved.
	Get(ctx context.Context, id string) (*TaskRecord, error)
	// List returns records for a context ID, newest first. Empty (non-nil) when
	// none. Live records must be the same instances Save received.
	List(ctx context.Context, contextID string) ([]*TaskRecord, error)
}

// memoryStore is a bounded store: live tasks (working / input-required)
// are never evicted; terminal tasks evict oldest-first once cap is
// exceeded (memory bounding is non-negotiable).
//
// Lock order: m.mu must be acquired before e.mu. evictLocked acquires
// e.mu briefly to read e.Task.Status.State while m.mu is already held.
// No TaskRecord method acquires m.mu, so no reverse ordering is possible.
type memoryStore struct {
	mu    sync.Mutex
	cap   int
	items map[string]*list.Element // taskID -> element holding *TaskRecord
	order *list.List               // insertion order, oldest at front
}

func newMemoryStore(capacity int) *memoryStore {
	if capacity <= 0 {
		capacity = 1024
	}
	return &memoryStore{cap: capacity, items: make(map[string]*list.Element), order: list.New()}
}

func (m *memoryStore) Save(_ context.Context, e *TaskRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if el, ok := m.items[e.Task.ID]; ok {
		el.Value = e
		return nil
	}
	m.items[e.Task.ID] = m.order.PushBack(e)
	m.evictLocked()
	return nil
}

func (m *memoryStore) Get(_ context.Context, id string) (*TaskRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	el, ok := m.items[id]
	if !ok {
		return nil, taskError(ErrTaskNotFound, id)
	}
	return el.Value.(*TaskRecord), nil
}

func (m *memoryStore) List(_ context.Context, contextID string) ([]*TaskRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*TaskRecord
	for el := m.order.Back(); el != nil; el = el.Prev() {
		e := el.Value.(*TaskRecord)
		if e.Task.ContextID == contextID {
			out = append(out, e)
		}
	}
	return out, nil
}

// evictLocked removes oldest terminal entries until len(m.items) <= m.cap.
// Called with m.mu held. Acquires e.mu briefly per candidate entry to read
// the task state (lock order: m.mu -> e.mu, never reversed).
func (m *memoryStore) evictLocked() {
	for len(m.items) > m.cap {
		evicted := false
		for el := m.order.Front(); el != nil; el = el.Next() {
			e := el.Value.(*TaskRecord)
			e.mu.Lock()
			terminal := e.Task.Status.State.Terminal()
			e.mu.Unlock()
			if terminal {
				m.order.Remove(el)
				delete(m.items, e.Task.ID)
				evicted = true
				break
			}
		}
		if !evicted {
			// Every entry is live; exceed cap rather than drop live tasks.
			return
		}
	}
}
