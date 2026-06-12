package a2a

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGoldenRoundTrip unmarshals every official spec fixture into our types
// and re-marshals, requiring semantic equality. Catches drift from the
// standard, not just from ourselves.
func TestGoldenRoundTrip(t *testing.T) {
	files, err := filepath.Glob("testdata/golden/*.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("no golden fixtures: %v", err)
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			var target any
			base := filepath.Base(f)
			switch {
			case strings.HasPrefix(base, "agent-card"):
				target = &AgentCard{}
			case strings.HasPrefix(base, "task"):
				target = &Task{}
			case strings.HasPrefix(base, "message"):
				target = &Message{}
			default:
				target = &StreamResponse{}
			}
			if err := json.Unmarshal(raw, target); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			out, err := json.Marshal(target)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var want, got any
			json.Unmarshal(raw, &want)
			json.Unmarshal(out, &got)
			if !jsonEqual(want, got) {
				t.Errorf("round-trip mismatch:\nwant %s\ngot  %s", raw, out)
			}
		})
	}
}

func jsonEqual(a, b any) bool {
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	return string(aj) == string(bj)
}

func TestTaskStateTerminal(t *testing.T) {
	for _, tc := range []struct {
		s    TaskState
		want bool
	}{
		{TaskStateCompleted, true},
		{TaskStateFailed, true},
		{TaskStateCanceled, true},
		{TaskStateRejected, true},
		{TaskStateWorking, false},
		{TaskStateInputRequired, false},
		{TaskStateSubmitted, false},
	} {
		if got := tc.s.Terminal(); got != tc.want {
			t.Errorf("%s.Terminal() = %v, want %v", tc.s, got, tc.want)
		}
	}
}
