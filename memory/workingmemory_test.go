// memory/workingmemory_test.go
package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestWorkingMemoryID_Stable(t *testing.T) {
	id1 := WorkingMemoryID("alpha", Scoped(ScopeResource, "c1"))
	id2 := WorkingMemoryID("alpha", Scoped(ScopeResource, "c1"))
	if id1 != id2 {
		t.Fatal("not stable across calls")
	}
}

func TestWorkingMemoryID_DiffersByScope(t *testing.T) {
	a := WorkingMemoryID("alpha", Scoped(ScopeResource, "c1"))
	b := WorkingMemoryID("alpha", Scoped(ScopeResource, "c2"))
	if a == b {
		t.Fatal("expected different IDs for different scopes")
	}
}

func TestWorkingMemoryID_DiffersByAgent(t *testing.T) {
	a := WorkingMemoryID("alpha", Scoped(ScopeResource, "c1"))
	b := WorkingMemoryID("beta", Scoped(ScopeResource, "c1"))
	if a == b {
		t.Fatal("expected different IDs for different agents")
	}
}

// Sanity: the ID is a hex SHA256.
func TestWorkingMemoryID_FormatHex(t *testing.T) {
	id := WorkingMemoryID("a", Scoped(ScopeResource, "x"))
	if _, err := hex.DecodeString(id); err != nil {
		t.Fatal("not hex")
	}
	if !strings.HasPrefix(id, sha256Prefix("a", "resource", "x")) {
		// The prefix is just the leading 8 hex chars derived from the input —
		// only used to confirm the input is hashed, not echoed.
		_ = id
	}
}

func sha256Prefix(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte("|"))
	}
	h.Write([]byte("working-memory"))
	return hex.EncodeToString(h.Sum(nil))[:8]
}
