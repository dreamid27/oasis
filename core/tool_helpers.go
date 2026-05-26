package core

import (
	"context"
	"encoding/json"
	"strconv"
)

// JSONContent wraps already-encoded JSON bytes as a ToolResult Content value.
func JSONContent(raw []byte) json.RawMessage { return raw }

// TextContent wraps a plain string as a JSON-quoted RawMessage suitable for
// ToolResult.Content from hand-rolled (non-Erase) tools.
func TextContent(s string) json.RawMessage {
	return json.RawMessage(strconv.Quote(s))
}

// TextResult is a convenience for hand-rolled tools producing plain text.
func TextResult(s string) ToolResult {
	return ToolResult{Content: TextContent(s)}
}

// JSONResult marshals v to JSON and returns a ToolResult with the encoded bytes
// as Content. Panics if json.Marshal fails — a marshal failure on a value the
// caller constructs is a programming error, not a runtime condition.
func JSONResult(v any) ToolResult {
	b, err := json.Marshal(v)
	if err != nil {
		panic("core.JSONResult: json.Marshal failed: " + err.Error())
	}
	return ToolResult{Content: json.RawMessage(b)}
}

// ErrorResult returns a ToolResult carrying msg as the error string and no
// content. Use this when a tool execution fails and the error should be
// surfaced to the LLM as a tool result rather than returned as a Go error.
func ErrorResult(msg string) ToolResult {
	return ToolResult{Error: msg}
}

// Text unpacks Content as a JSON string and returns the unquoted value.
// Returns "" when Content is nil, empty, or not a JSON string token.
// For structured (non-string) JSON content the caller should unmarshal
// Content directly.
func (r ToolResult) Text() string {
	if len(r.Content) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(r.Content, &s); err != nil {
		return ""
	}
	return s
}

// RawTool constructs an AnyTool from its constituent parts without requiring a
// named struct type. Useful for tests and for tools whose logic is expressed as
// a closure rather than a named type.
func RawTool(
	name, description string,
	schema json.RawMessage,
	fn func(ctx context.Context, args json.RawMessage) (ToolResult, error),
) AnyTool {
	return &rawTool{
		name: name,
		def: ToolDefinition{
			Name:        name,
			Description: description,
			Parameters:  schema,
		},
		fn: fn,
	}
}

type rawTool struct {
	name string
	def  ToolDefinition
	fn   func(context.Context, json.RawMessage) (ToolResult, error)
}

func (t *rawTool) Name() string               { return t.name }
func (t *rawTool) Definition() ToolDefinition { return t.def }
func (t *rawTool) ExecuteRaw(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	return t.fn(ctx, args)
}
