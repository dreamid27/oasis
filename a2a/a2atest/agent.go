// Package a2atest provides test doubles for A2A integrations.
package a2atest

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/nevindra/oasis/core"
)

// EchoAgent is a core.Agent that echoes its input. Zero dependencies,
// instant responses — usable in unit tests and benchmarks.
type EchoAgent struct {
	name, desc string
}

func NewEchoAgent(name, desc string) *EchoAgent { return &EchoAgent{name: name, desc: desc} }

func (e *EchoAgent) Name() string        { return e.name }
func (e *EchoAgent) Description() string { return e.desc }

func (e *EchoAgent) Execute(ctx context.Context, task core.AgentTask, opts ...core.RunOption) (core.AgentResult, error) {
	cfg := core.ApplyRunOptions(opts...)
	if cfg.Stream != nil {
		cfg.Stream <- core.StreamEvent{Type: core.EventTextDelta, Content: "echo: "}
		cfg.Stream <- core.StreamEvent{Type: core.EventTextDelta, Content: task.Input}
		close(cfg.Stream)
	}
	return core.AgentResult{Output: "echo: " + task.Input, FinishReason: core.FinishStop}, nil
}

// FailingAgent always returns a Go error from Execute. Used to verify the
// server maps an agent failure to a failed TASK (not a dropped connection or
// an RPC-level error).
type FailingAgent struct{ name, desc string }

// NewFailingAgent constructs a FailingAgent with the given name and description.
func NewFailingAgent(name, desc string) *FailingAgent { return &FailingAgent{name, desc} }

func (f *FailingAgent) Name() string        { return f.name }
func (f *FailingAgent) Description() string { return f.desc }
func (f *FailingAgent) Execute(context.Context, core.AgentTask, ...core.RunOption) (core.AgentResult, error) {
	return core.AgentResult{}, errors.New("provider unreachable: connect timeout to llm.internal:443")
}

// SuspendingAgent suspends on first Execute and completes on Resume. It
// hand-rolls the resumable contract (an *ErrSuspended-shaped error) so the
// a2atest package stays free of the agent/ import.
type SuspendingAgent struct{ name, desc string }

// NewSuspendingAgent constructs a SuspendingAgent with the given name and
// description.
func NewSuspendingAgent(name, desc string) *SuspendingAgent { return &SuspendingAgent{name, desc} }

func (s *SuspendingAgent) Name() string        { return s.name }
func (s *SuspendingAgent) Description() string { return s.desc }

// stubSuspend is the resumable error the SuspendingAgent returns. It mirrors
// *agent.ErrSuspended's behavior (Error + Resume + ResumeStream) without the
// dependency.
type stubSuspend struct{ payload json.RawMessage }

func (e *stubSuspend) Error() string { return "suspended" }
func (e *stubSuspend) Resume(ctx context.Context, data json.RawMessage) (core.AgentResult, error) {
	return core.AgentResult{Output: "resumed with: " + string(data), FinishReason: core.FinishStop}, nil
}
func (e *stubSuspend) ResumeStream(ctx context.Context, data json.RawMessage, ch chan<- core.StreamEvent) (core.AgentResult, error) {
	if ch != nil {
		ch <- core.StreamEvent{Type: core.EventTextDelta, Content: "resumed"}
		close(ch)
	}
	return core.AgentResult{Output: "resumed with: " + string(data), FinishReason: core.FinishStop}, nil
}

func (s *SuspendingAgent) Execute(ctx context.Context, task core.AgentTask, opts ...core.RunOption) (core.AgentResult, error) {
	payload := json.RawMessage(`{"question":"which fiscal year?"}`)
	return core.AgentResult{
		FinishReason:   core.FinishSuspended,
		SuspendPayload: payload,
	}, &stubSuspend{payload: payload}
}

// BlockingAgent blocks in Execute until its context is canceled, then returns
// a context.Canceled error. Used to test that Server.Close() and other
// cancellation paths correctly abort streaming runs.
type BlockingAgent struct{ name, desc string }

// NewBlockingAgent constructs a BlockingAgent with the given name and description.
func NewBlockingAgent(name, desc string) *BlockingAgent { return &BlockingAgent{name, desc} }

func (b *BlockingAgent) Name() string        { return b.name }
func (b *BlockingAgent) Description() string { return b.desc }
func (b *BlockingAgent) Execute(ctx context.Context, _ core.AgentTask, opts ...core.RunOption) (core.AgentResult, error) {
	cfg := core.ApplyRunOptions(opts...)
	if cfg.Stream != nil {
		defer close(cfg.Stream)
	}
	<-ctx.Done() // block until canceled
	return core.AgentResult{}, ctx.Err()
}

// PanicAgent always panics from Execute. Used to verify the server recovers
// from a panicking agent and settles the task as FAILED rather than crashing
// the process.
type PanicAgent struct{ name, desc string }

// NewPanicAgent constructs a PanicAgent with the given name and description.
func NewPanicAgent(name, desc string) *PanicAgent { return &PanicAgent{name, desc} }

func (p *PanicAgent) Name() string        { return p.name }
func (p *PanicAgent) Description() string { return p.desc }
func (p *PanicAgent) Execute(context.Context, core.AgentTask, ...core.RunOption) (core.AgentResult, error) {
	panic("simulated agent panic")
}
