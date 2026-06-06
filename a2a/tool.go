package a2a

import (
	"context"
	"encoding/json"

	"github.com/nevindra/oasis/core"
)

// remoteTool adapts a RemoteAgent to core.AnyTool so an LLM can delegate to a
// remote A2A agent mid-run. It is safe for concurrent use (the wrapped
// RemoteAgent is). Construct via AsTool.
type remoteTool struct {
	agent *RemoteAgent
	def   core.ToolDefinition
}

// remoteToolArgs is the single-parameter input schema: one natural-language
// task string the LLM fills in when it decides to delegate.
type remoteToolArgs struct {
	Input string `json:"input"`
}

// remoteToolSchema is the JSON Schema for remoteToolArgs. Built once at AsTool
// time and shared (immutable) — no per-call schema allocation.
var remoteToolSchema = json.RawMessage(`{` +
	`"type":"object",` +
	`"properties":{"input":{"type":"string","description":"The task to send to the remote agent, in natural language."}},` +
	`"required":["input"]}`)

// AsTool wraps a RemoteAgent as a core.AnyTool so an LLMAgent can call it like
// any other tool, delegating a sub-task to the remote agent mid-run. The tool's
// name and description come from the remote agent's card; it takes one "input"
// string parameter (the natural-language task).
//
// Error contract: a remote failure becomes a tool-level ToolResult.Error the
// LLM can read and react to — NOT a Go error that would abort the run. A Go
// error is returned only for infrastructure failures surfaced by the transport.
// Malformed arguments are a tool-level error too. Safe for concurrent use.
func AsTool(agent *RemoteAgent) core.AnyTool {
	return &remoteTool{
		agent: agent,
		def: core.ToolDefinition{
			Name:        agent.Name(),
			Description: agent.Description(),
			Parameters:  remoteToolSchema,
		},
	}
}

// Name returns the tool name (the sanitized remote agent name).
func (t *remoteTool) Name() string { return t.def.Name }

// Definition returns the tool definition shown to the LLM.
func (t *remoteTool) Definition() core.ToolDefinition { return t.def }

// ExecuteRaw delegates the call to the remote agent. Bad arguments and remote
// task failures are returned as ToolResult.Error (business failures the LLM
// handles); only transport/infrastructure errors return a Go error.
func (t *remoteTool) ExecuteRaw(ctx context.Context, args json.RawMessage) (core.ToolResult, error) {
	var in remoteToolArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return core.ToolResult{Error: "invalid arguments: " + err.Error()}, nil
	}

	res, err := t.agent.Execute(ctx, core.AgentTask{Input: in.Input})
	if err != nil {
		// A remote FAILED task surfaces as a Go error (with FinishError); present
		// it to the LLM as a tool-level failure rather than aborting the run.
		return core.ToolResult{Error: err.Error()}, nil
	}

	out := res.Output
	// A suspended remote agent relays its prompt as the tool result text so the
	// LLM can see what input is needed. A follow-up tool call starts a FRESH
	// remote task — AgentTask has no ThreadID here, so the remote task cannot
	// be resumed. For multi-turn HITL across the wire, use RemoteAgent.Execute
	// directly with a consistent ThreadID instead of AsTool.
	if out == "" && len(res.SuspendPayload) > 0 {
		out = string(res.SuspendPayload)
	}
	return core.ToolResult{Content: out, Attachments: res.Files}, nil
}
