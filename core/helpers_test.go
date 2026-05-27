package core_test

import (
	"encoding/json"
	"testing"

	"github.com/nevindra/oasis/core"
)

func TestTextResultContent(t *testing.T) {
	r := core.TextResult("hello")
	if r.Content != "hello" {
		t.Errorf("TextResult content = %q, want %q", r.Content, "hello")
	}
	// Text() must return the plain string as-is.
	if got := r.Text(); got != "hello" {
		t.Errorf("Text() = %q, want %q", got, "hello")
	}
}

func TestJSONContentPreservesBytes(t *testing.T) {
	input := []byte(`{"a":1}`)
	raw := core.JSONContent(input)
	if string(raw) != `{"a":1}` {
		t.Errorf("JSONContent = %q, want %q", raw, `{"a":1}`)
	}
}

func TestToolResultContentRoundTrip(t *testing.T) {
	// A ToolResult with TextContent("hi") should marshal with "hi" as the
	// content value (a JSON string literal), not double-encoded bytes.
	r := core.ToolResult{Content: core.TextContent("hi")}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	// Content in the wire JSON should be the JSON string "hi", not base64.
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	got := string(m["content"])
	if got != `"hi"` {
		t.Errorf("wire content = %q, want %q", got, `"hi"`)
	}
}
