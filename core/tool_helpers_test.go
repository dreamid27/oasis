package core

import (
	"context"
	"encoding/json"
	"testing"
)

// --- JSONResult ---

func TestJSONResult_String(t *testing.T) {
	r := JSONResult("hello")
	if string(r.Content) != `"hello"` {
		t.Errorf("JSONResult(string): got %q, want %q", r.Content, `"hello"`)
	}
	if r.Error != "" {
		t.Errorf("JSONResult(string): unexpected Error %q", r.Error)
	}
}

func TestJSONResult_Struct(t *testing.T) {
	type point struct {
		X int `json:"x"`
		Y int `json:"y"`
	}
	r := JSONResult(point{X: 1, Y: 2})
	var got point
	if err := json.Unmarshal([]byte(r.Content), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.X != 1 || got.Y != 2 {
		t.Errorf("JSONResult(struct): got %+v, want {1 2}", got)
	}
}

func TestJSONResult_Map(t *testing.T) {
	m := map[string]int{"a": 10, "b": 20}
	r := JSONResult(m)
	var got map[string]int
	if err := json.Unmarshal([]byte(r.Content), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["a"] != 10 || got["b"] != 20 {
		t.Errorf("JSONResult(map): got %v, want {a:10 b:20}", got)
	}
}

func TestJSONResult_PanicsOnUnmarshalable(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("JSONResult(chan): expected panic, got none")
		}
	}()
	// Channels cannot be marshaled to JSON; this must panic.
	JSONResult(make(chan int))
}

// --- ErrorResult ---

func TestErrorResult(t *testing.T) {
	r := ErrorResult("something went wrong")
	if r.Error != "something went wrong" {
		t.Errorf("ErrorResult: Error = %q, want %q", r.Error, "something went wrong")
	}
	if len(r.Content) != 0 {
		t.Errorf("ErrorResult: Content should be empty, got %q", r.Content)
	}
}

func TestErrorResult_EmptyString(t *testing.T) {
	r := ErrorResult("")
	if r.Error != "" {
		t.Errorf("ErrorResult(%q): Error = %q, want empty", "", r.Error)
	}
}

// --- ToolResult.Text ---

func TestText_FromTextResult(t *testing.T) {
	r := TextResult("plain text")
	if got := r.Text(); got != "plain text" {
		t.Errorf("Text(): got %q, want %q", got, "plain text")
	}
}

func TestText_FromJSONResultWithString(t *testing.T) {
	r := JSONResult("json string")
	// JSONResult encodes the string as JSON, so Content = `"json string"` (with quotes).
	// Text() returns Content as-is.
	if got := r.Text(); got != `"json string"` {
		t.Errorf("Text(): got %q, want %q", got, `"json string"`)
	}
}

func TestText_NilContent(t *testing.T) {
	r := ToolResult{}
	if got := r.Text(); got != "" {
		t.Errorf("Text() on nil Content: got %q, want %q", got, "")
	}
}

func TestText_NonStringJSONContent(t *testing.T) {
	// Content is now a plain string; Text() returns it as-is.
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"number", `42`, `42`},
		{"object", `{"x":1}`, `{"x":1}`},
		{"array", `[1,2,3]`, `[1,2,3]`},
		{"bool", `true`, `true`},
		{"empty", ``, ``},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := ToolResult{Content: c.content}
			if got := r.Text(); got != c.want {
				t.Errorf("Text() on %s content: got %q, want %q", c.name, got, c.want)
			}
		})
	}
}

// --- RawTool ---

func TestRawTool_Name(t *testing.T) {
	tool := RawTool("echo", "echoes args", nil, func(_ context.Context, args json.RawMessage) (ToolResult, error) {
		return TextResult("ok"), nil
	})
	if tool.Name() != "echo" {
		t.Errorf("Name(): got %q, want %q", tool.Name(), "echo")
	}
}

func TestRawTool_Definition(t *testing.T) {
	schema := json.RawMessage(`{"type":"object"}`)
	tool := RawTool("my-tool", "does stuff", schema, func(_ context.Context, _ json.RawMessage) (ToolResult, error) {
		return ToolResult{}, nil
	})
	def := tool.Definition()
	if def.Name != "my-tool" {
		t.Errorf("Definition().Name: got %q, want %q", def.Name, "my-tool")
	}
	if def.Description != "does stuff" {
		t.Errorf("Definition().Description: got %q, want %q", def.Description, "does stuff")
	}
	if string(def.Parameters) != `{"type":"object"}` {
		t.Errorf("Definition().Parameters: got %q, want %q", def.Parameters, `{"type":"object"}`)
	}
}

func TestRawTool_ExecuteRaw(t *testing.T) {
	called := false
	tool := RawTool("check", "checks input", nil, func(_ context.Context, args json.RawMessage) (ToolResult, error) {
		called = true
		return TextResult(string(args)), nil
	})

	result, err := tool.ExecuteRaw(context.Background(), json.RawMessage(`"payload"`))
	if err != nil {
		t.Fatalf("ExecuteRaw: unexpected error: %v", err)
	}
	if !called {
		t.Error("ExecuteRaw: fn was not called")
	}
	if result.Text() != "\"payload\"" {
		t.Errorf("ExecuteRaw: Text() = %q, want %q", result.Text(), "\"payload\"")
	}
}

func TestRawTool_ImplementsAnyTool(t *testing.T) {
	// Compile-time interface check expressed as a runtime assertion.
	var _ AnyTool = RawTool("x", "", nil, func(_ context.Context, _ json.RawMessage) (ToolResult, error) {
		return ToolResult{}, nil
	})
}
