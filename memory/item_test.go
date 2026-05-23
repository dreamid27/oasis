// memory/item_test.go
package memory

import (
	"testing"
)

func TestMemoryItem_ZeroValueIsUsable(t *testing.T) {
	var item MemoryItem
	if item.ID != "" || item.Kind != "" || item.Pinned {
		t.Fatalf("zero value not clean: %+v", item)
	}
}

func TestKindConstants(t *testing.T) {
	cases := map[Kind]string{
		KindFact:       "fact",
		KindNote:       "note",
		KindEvent:      "event",
		KindPlaybook:   "playbook",
		KindReflection: "reflection",
		KindSummary:    "summary",
	}
	for k, want := range cases {
		if string(k) != want {
			t.Errorf("kind %v = %q, want %q", k, string(k), want)
		}
	}
}

func TestScopeConstants(t *testing.T) {
	cases := map[ScopeKind]string{
		ScopeThread:   "thread",
		ScopeResource: "resource",
		ScopeAgent:    "agent",
		ScopeGlobal:   "global",
	}
	for s, want := range cases {
		if string(s) != want {
			t.Errorf("scope %v = %q, want %q", s, string(s), want)
		}
	}
}

func TestScopedHelper(t *testing.T) {
	s := Scoped(ScopeResource, "user_123")
	if s.Kind != ScopeResource || s.Ref != "user_123" {
		t.Fatalf("Scoped wrong: %+v", s)
	}
}

func TestUserExtensibleKind(t *testing.T) {
	// Kind is a string type; users can define their own kinds.
	const KindDecision Kind = "decision"
	item := MemoryItem{Kind: KindDecision, Content: "go with sqlite"}
	if string(item.Kind) != "decision" {
		t.Fatalf("user-defined kind not preserved: %q", item.Kind)
	}
}
