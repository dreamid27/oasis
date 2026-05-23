// memory/retrieve_test.go
package memory

import (
	"context"
	"strings"
	"testing"

	"github.com/nevindra/oasis/core"
)

func TestBuildMessages_Minimal(t *testing.T) {
	store := newConformanceStore(t)
	var m AgentMemory
	m.Init(AgentMemoryConfig{Store: store, Logger: discardLogger()})
	task := core.AgentTask{ThreadID: "t1", ChatID: "c1", Input: "hello"}
	msgs := m.BuildMessages(context.Background(), "agent", "you are helpful", task)
	if len(msgs) < 2 {
		t.Fatalf("expected system + user, got %d", len(msgs))
	}
	if msgs[len(msgs)-1].Role != core.RoleUser {
		t.Fatal("user msg should be last")
	}
}

func TestBuildMessages_BatchedRecallIncludesFacts(t *testing.T) {
	store := newConformanceStore(t)
	must(t, store.Upsert(context.Background(), MemoryItem{
		ID: "f1", Kind: KindFact, Content: "User likes dark mode",
		Scope: Scoped(ScopeResource, "c1"), Embedding: []float32{1, 0, 0},
	}))
	emb := &fakeEmbedder{out: [][]float32{{1, 0, 0}}}
	var m AgentMemory
	m.Init(AgentMemoryConfig{
		Store: store, Embedding: emb,
		RecallKinds: []Kind{KindFact}, RecallTopK: 5,
		Logger: discardLogger(),
	})
	task := core.AgentTask{ThreadID: "t1", ChatID: "c1", Input: "what color"}
	msgs := m.BuildMessages(context.Background(), "agent", "", task)
	combined := ""
	for _, mm := range msgs {
		combined += "\n" + mm.Content
	}
	if !strings.Contains(combined, "dark mode") {
		t.Fatalf("recall not injected:\n%s", combined)
	}
}
