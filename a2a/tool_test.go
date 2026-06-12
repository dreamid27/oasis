package a2a

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/nevindra/oasis/a2a/a2atest"
	"github.com/nevindra/oasis/core"
)

// TestAsTool proves a RemoteAgent wraps cleanly into a core.AnyTool: the tool
// name is the sanitized card name, the definition is non-empty, a well-formed
// call returns the remote output as Content, and a malformed call returns a
// tool-level ToolResult.Error (NOT a Go error) the LLM can react to.
func TestAsTool(t *testing.T) {
	ts := httptest.NewServer(NewServer(a2atest.NewEchoAgent("research helper", "finds sources")))
	defer ts.Close()

	remote, err := Dial(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	tool := AsTool(remote)

	var _ core.AnyTool = tool // compile-time: AsTool yields a core.AnyTool

	if tool.Name() != "research_helper" {
		t.Errorf("tool name = %q, want %q", tool.Name(), "research_helper")
	}
	if tool.Definition().Description == "" {
		t.Error("Definition().Description is empty")
	}
	if len(tool.Definition().Parameters) == 0 {
		t.Error("Definition().Parameters is empty; want an input schema")
	}

	res, err := tool.ExecuteRaw(context.Background(), json.RawMessage(`{"input":"find sources"}`))
	if err != nil {
		t.Fatalf("ExecuteRaw: unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Errorf("ToolResult.Error = %q, want empty", res.Error)
	}
	if res.Content != "echo: find sources" {
		t.Errorf("Content = %q, want %q", res.Content, "echo: find sources")
	}

	// Malformed args: business failure, surfaced as ToolResult.Error, nil Go error.
	bad, err := tool.ExecuteRaw(context.Background(), json.RawMessage(`{not json`))
	if err != nil {
		t.Fatalf("ExecuteRaw(malformed): want nil Go error, got %v", err)
	}
	if bad.Error == "" {
		t.Error("ToolResult.Error is empty for malformed args; want a business failure")
	}
}
