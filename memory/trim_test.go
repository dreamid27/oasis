// memory/trim_test.go
package memory

import (
	"testing"

	"github.com/nevindra/oasis/core"
)

func TestEstimateTokens(t *testing.T) {
	msg := core.ChatMessage{Role: core.RoleUser, Content: "hello world"} // 11 runes
	got := estimateTokens(msg)
	if got != 11/4+4 {
		t.Fatalf("got %d, want %d", got, 11/4+4)
	}
}

func TestTrimHistory_OldestFirst(t *testing.T) {
	msgs := []core.ChatMessage{
		core.SystemMessage("sys"),
		{Role: core.RoleUser, Content: "first msg"},
		{Role: core.RoleAssistant, Content: "first reply"},
		{Role: core.RoleUser, Content: "second msg"},
	}
	out := trimHistoryOldestFirst(msgs, 1, len(msgs), 100, 20) // budget 20 tokens
	if len(out) >= len(msgs) {
		t.Fatalf("trim did not happen: %d", len(out))
	}
	if out[0].Role != "system" {
		t.Fatal("system prompt dropped")
	}
}
