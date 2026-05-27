// bench_e2e_test.go benchmarks end-to-end agent execution overhead with mocked
// LLM providers (instant responses, zero latency). Every nanosecond and byte
// measured is framework tax, not LLM time.
package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/nevindra/oasis/agent"
	"github.com/nevindra/oasis/core"
)

type scriptedProvider struct {
	responses []core.ChatResponse
	idx       int
}

func (p *scriptedProvider) Name() string { return "scripted" }
func (p *scriptedProvider) ChatStream(_ context.Context, _ core.ChatRequest, ch chan<- core.StreamEvent) (core.ChatResponse, error) {
	if ch != nil {
		defer close(ch)
	}
	resp := p.responses[p.idx]
	p.idx++
	return resp, nil
}

type benchTool struct{ name string }

func (t *benchTool) Name() string { return t.name }
func (t *benchTool) Definition() core.ToolDefinition {
	return core.ToolDefinition{
		Name:        t.name,
		Description: "benchmark tool",
		Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
	}
}
func (t *benchTool) ExecuteRaw(_ context.Context, _ json.RawMessage) (core.ToolResult, error) {
	return core.ToolResult{Content: "ok"}, nil
}

type benchPreProcessor struct{}

func (benchPreProcessor) PreLLM(_ context.Context, _ *core.ChatRequest) error { return nil }

type benchPostProcessor struct{}

func (benchPostProcessor) PostLLM(_ context.Context, _ *core.ChatResponse) error { return nil }

func makeTools(n int) []core.AnyTool {
	tools := make([]core.AnyTool, n)
	for i := range tools {
		tools[i] = &benchTool{name: "tool_" + strings.Repeat("x", 3) + string(rune('a'+i%26))}
	}
	return tools
}

func toolCallResponses(nCalls int) []core.ChatResponse {
	calls := make([]core.ToolCall, nCalls)
	for i := range calls {
		name := "tool_" + strings.Repeat("x", 3) + string(rune('a'+i%26))
		calls[i] = core.ToolCall{ID: "call_" + string(rune('0'+i%10)), Name: name, Args: json.RawMessage(`{}`)}
	}
	return []core.ChatResponse{
		{ToolCalls: calls},
		{Content: "done"},
	}
}

func deepIterationResponses(nIters int) []core.ChatResponse {
	resps := make([]core.ChatResponse, 0, nIters+1)
	for range nIters {
		resps = append(resps, core.ChatResponse{
			ToolCalls: []core.ToolCall{{ID: "c1", Name: "tool_xxxa", Args: json.RawMessage(`{}`)}},
		})
	}
	resps = append(resps, core.ChatResponse{Content: "done"})
	return resps
}

var benchTask = core.AgentTask{Input: "hello"}

func BenchmarkAgentExecute_SingleTurn(b *testing.B) {
	p := newFakeProviderReturning("hi")
	a := agent.New("bench", "bench", p, agent.WithoutPromptCaching())
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		a.Execute(ctx, benchTask)
	}
}

func BenchmarkAgentExecute_WithTools(b *testing.B) {
	for _, n := range []int{1, 5, 10} {
		b.Run(fmt.Sprintf("tools=%d", n), func(b *testing.B) {
			p := newFakeProviderReturning("hi")
			tools := makeTools(n)
			a := agent.New("bench", "bench", p, agent.WithTools(tools...), agent.WithoutPromptCaching())
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				a.Execute(ctx, benchTask)
			}
		})
	}
}

func BenchmarkAgentExecute_ToolLoop(b *testing.B) {
	for _, nCalls := range []int{1, 3, 5} {
		b.Run(fmt.Sprintf("calls=%d", nCalls), func(b *testing.B) {
			tools := makeTools(nCalls)
			ctx := context.Background()
			resps := toolCallResponses(nCalls)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				p := &scriptedProvider{responses: resps}
				a := agent.New("bench", "bench", p, agent.WithTools(tools...), agent.WithoutPromptCaching())
				a.Execute(ctx, benchTask)
			}
		})
	}
}

func BenchmarkAgentExecute_DeepIteration(b *testing.B) {
	for _, nIters := range []int{1, 3, 5, 10} {
		b.Run(fmt.Sprintf("iters=%d", nIters), func(b *testing.B) {
			tools := []core.AnyTool{&benchTool{name: "tool_xxxa"}}
			ctx := context.Background()
			resps := deepIterationResponses(nIters)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				p := &scriptedProvider{responses: resps}
				a := agent.New("bench", "bench", p,
					agent.WithTools(tools...),
					agent.WithLimits(agent.Limits{MaxIter: nIters + 1}),
					agent.WithoutPromptCaching(),
				)
				a.Execute(ctx, benchTask)
			}
		})
	}
}

func BenchmarkAgentExecute_ParallelToolDispatch(b *testing.B) {
	for _, nParallel := range []int{1, 5, 10, 20} {
		b.Run(fmt.Sprintf("parallel=%d", nParallel), func(b *testing.B) {
			tools := makeTools(nParallel)
			calls := make([]core.ToolCall, nParallel)
			for i := range calls {
				calls[i] = core.ToolCall{
					ID:   "call_" + string(rune('a'+i%26)),
					Name: tools[i].Name(),
					Args: json.RawMessage(`{}`),
				}
			}
			resps := []core.ChatResponse{
				{ToolCalls: calls},
				{Content: "done"},
			}
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				p := &scriptedProvider{responses: resps}
				a := agent.New("bench", "bench", p,
					agent.WithTools(tools...),
					agent.WithLimits(agent.Limits{MaxParallelDispatch: nParallel}),
					agent.WithoutPromptCaching(),
				)
				a.Execute(ctx, benchTask)
			}
		})
	}
}

func BenchmarkAgentExecute_Stream(b *testing.B) {
	p := newFakeProviderReturning("hi")
	a := agent.New("bench", "bench", p, agent.WithoutPromptCaching())
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		ch := make(chan core.StreamEvent, 1)
		done := make(chan struct{})
		go func() {
			for range ch {
			}
			close(done)
		}()
		a.Execute(ctx, benchTask, core.WithStream(ch))
		<-done
	}
}

func BenchmarkAgentExecute_StreamWithToolCalls(b *testing.B) {
	tools := makeTools(3)
	resps := toolCallResponses(3)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		p := &scriptedProvider{responses: resps}
		a := agent.New("bench", "bench", p, agent.WithTools(tools...), agent.WithoutPromptCaching())
		ch := make(chan core.StreamEvent, 1)
		done := make(chan struct{})
		go func() {
			for range ch {
			}
			close(done)
		}()
		a.Execute(ctx, benchTask, core.WithStream(ch))
		<-done
	}
}

func BenchmarkAgentExecute_Processors(b *testing.B) {
	for _, n := range []int{1, 3, 5} {
		b.Run(fmt.Sprintf("processors=%d", n), func(b *testing.B) {
			p := newFakeProviderReturning("hi")
			pre := make([]core.PreProcessor, n)
			post := make([]core.PostProcessor, n)
			for i := range n {
				pre[i] = benchPreProcessor{}
				post[i] = benchPostProcessor{}
			}
			a := agent.New("bench", "bench", p,
				agent.WithProcessors(agent.Processors{Pre: pre, Post: post}),
				agent.WithoutPromptCaching(),
			)
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				a.Execute(ctx, benchTask)
			}
		})
	}
}

func BenchmarkAgentExecute_LargeSystemPrompt(b *testing.B) {
	for _, kb := range []int{10, 50, 100} {
		b.Run(fmt.Sprintf("prompt=%dKB", kb), func(b *testing.B) {
			prompt := strings.Repeat("x", kb*1024)
			p := newFakeProviderReturning("hi")
			a := agent.New("bench", "bench", p, agent.WithPrompt(prompt), agent.WithoutPromptCaching())
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				a.Execute(ctx, benchTask)
			}
		})
	}
}

func BenchmarkAgentExecute_LargeInput(b *testing.B) {
	for _, kb := range []int{10, 50, 100} {
		b.Run(fmt.Sprintf("input=%dKB", kb), func(b *testing.B) {
			input := strings.Repeat("y", kb*1024)
			p := newFakeProviderReturning("hi")
			a := agent.New("bench", "bench", p, agent.WithoutPromptCaching())
			ctx := context.Background()
			task := core.AgentTask{Input: input}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				a.Execute(ctx, task)
			}
		})
	}
}

func BenchmarkAgentExecute_LargeToolResult(b *testing.B) {
	for _, label := range []struct {
		name string
		size int
	}{
		{"10KB", 10 * 1024},
		{"100KB", 100 * 1024},
		{"1MB", 1024 * 1024},
	} {
		b.Run("result="+label.name, func(b *testing.B) {
			bigContent := strings.Repeat("z", label.size)
			bigTool := &largeBenchTool{content: bigContent}
			resps := []core.ChatResponse{
				{ToolCalls: []core.ToolCall{{ID: "c1", Name: "big_tool", Args: json.RawMessage(`{}`)}}},
				{Content: "done"},
			}
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				p := &scriptedProvider{responses: resps}
				a := agent.New("bench", "bench", p, agent.WithTools(bigTool), agent.WithoutPromptCaching())
				a.Execute(ctx, benchTask)
			}
		})
	}
}

type largeBenchTool struct{ content string }

func (t *largeBenchTool) Name() string { return "big_tool" }
func (t *largeBenchTool) Definition() core.ToolDefinition {
	return core.ToolDefinition{
		Name:        "big_tool",
		Description: "benchmark tool with large result",
		Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
	}
}
func (t *largeBenchTool) ExecuteRaw(_ context.Context, _ json.RawMessage) (core.ToolResult, error) {
	return core.ToolResult{Content: t.content}, nil
}
