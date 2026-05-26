# Functional Tools (`core.Func`) — Design Spec

Origin: DX ergonomics initiative — tool authoring is 13 lines for a trivial tool. `Func` cuts it to 1 line at the call site.

Release: **0.18.0** (ships alongside the DX ergonomics changes).

---

## Problem

Every tool author must:
1. Define an input struct (unavoidable — schema source)
2. Define a named tool struct
3. Implement `Tool[In, Out]` interface (`Definition()` + `Execute()`)
4. Call `Erase()` at registration

Steps 2–4 are pure ceremony. The meaningful information is: name, description, and a function.

## Solution

```go
func Func[In, Out any](name, description string,
    fn func(context.Context, In) (Out, error),
) AnyTool
```

One generic free function. Schema derived from `In` via existing `DeriveSchema`. Output marshaled to JSON automatically. Returns `AnyTool` ready for `WithTools`.

### Before vs After

```go
// Before: struct + interface + Erase (13 lines)
type AddInput struct {
    A int `json:"a" describe:"first number"`
    B int `json:"b" describe:"second number"`
}
type AddTool struct{}
func (AddTool) Definition() core.ToolMeta {
    return core.ToolMeta{Name: "add", Description: "Add two numbers"}
}
func (AddTool) Execute(ctx context.Context, in AddInput) (int, error) {
    return in.A + in.B, nil
}
oasis.WithTools(oasis.Erase(&AddTool{}))

// After: function (1 line at call site, input struct still needed for schema)
oasis.WithTools(oasis.Func("add", "Add two numbers",
    func(ctx context.Context, in AddInput) (int, error) {
        return in.A + in.B, nil
    }))
```

## Implementation

### File: `core/func.go` (new)

```go
func Func[In, Out any](name, description string,
    fn func(context.Context, In) (Out, error),
) AnyTool {
    return &funcTool[In, Out]{
        name: name,
        def: ToolDefinition{
            Name:        name,
            Description: description,
            Parameters:  DeriveSchema[In](),
        },
        fn: fn,
    }
}

type funcTool[In, Out any] struct {
    name string
    def  ToolDefinition
    fn   func(context.Context, In) (Out, error)
}

func (t *funcTool[In, Out]) Name() string               { return t.name }
func (t *funcTool[In, Out]) Definition() ToolDefinition { return t.def }

func (t *funcTool[In, Out]) ExecuteRaw(ctx context.Context, args json.RawMessage) (ToolResult, error) {
    var in In
    args = coerceArgs(args)
    if err := json.Unmarshal(args, &in); err != nil {
        return ToolResult{Error: "invalid args: " + err.Error()}, nil
    }
    out, err := t.fn(ctx, in)
    if err != nil {
        return ToolResult{Error: err.Error()}, err
    }
    body, err := json.Marshal(out)
    if err != nil {
        return ToolResult{Error: "marshal result: " + err.Error()}, nil
    }
    return ToolResult{Content: body}, nil
}
```

`ExecuteRaw` follows the same contract as `erasedTool.ExecuteRaw` in `erase.go`:
- Invalid args → `ToolResult{Error}`, nil Go error (LLM can retry)
- Function error → `ToolResult{Error}` + Go error propagated (dispatch policy can inspect)
- Marshal failure → `ToolResult{Error}`, nil Go error

### Umbrella: `oasis.go`

Generic functions can't be `var`-aliased. Use wrapper (same pattern as `Erase`):

```go
func Func[In, Out any](name, desc string,
    fn func(context.Context, In) (Out, error),
) AnyTool {
    return core.Func[In, Out](name, desc, fn)
}
```

### Output schema

`Func` does not derive an output schema (no `OutSchemaProvider` check). The
`ToolDefinition.OutputSchema` field is left nil. Tools that need output schema
should use the `Tool[In, Out]` + `Erase` path.

### Relationship to existing helpers

| Helper | Input schema | Output schema | Use case |
|--------|-------------|---------------|----------|
| `Func[In,Out]` | Auto-derived from `In` | None | **80% case** — pure functions |
| `Erase(Tool[In,Out])` | Auto-derived from `In` | Auto-derived from `Out` (+ `OutSchemaProvider`) | Stateful tools, custom output schema |
| `RawTool(name,desc,schema,fn)` | Caller-supplied JSON | None | Dynamic schemas, raw JSON tools |

## Tests (`core/func_test.go`)

1. Basic round-trip: `Func` with struct input, int output
2. Struct output: verify JSON marshaling
3. String output: verify quoted JSON
4. Schema derivation: `Func` schema matches `DeriveSchema` directly
5. Name and Definition: verify fields populated correctly
6. Error propagation: fn returns error → ToolResult.Error set + Go error returned
7. Invalid args: bad JSON → ToolResult.Error, nil Go error
8. Panic on unsupported type: channel in input struct panics at registration

## Files changed

| File | Change |
|------|--------|
| `core/func.go` (new) | `Func[In, Out]`, `funcTool` struct |
| `core/func_test.go` (new) | 8 test cases |
| `oasis.go` | Add `Func` generic wrapper |

## What this spec does NOT do

- No `FuncTool(name, desc, fn any)` untyped variant — violates "no any at boundary"
- No output schema derivation — YAGNI for functional tools
- No streaming variant — `Func` tools are synchronous; streaming tools use `EraseStreaming`
