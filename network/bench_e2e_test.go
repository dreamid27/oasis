// bench_e2e_test.go measures end-to-end Network.Execute overhead with mocked
// providers and agents. All LLM calls are scripted stubs — the benchmarks
// isolate the framework's routing, delegation, and result-forwarding tax.
package network

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/nevindra/oasis/agent"
	"github.com/nevindra/oasis/core"
)

func BenchmarkNetworkExecute_SingleAgent(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		router := &mockProvider{
			name: "router",
			responses: []core.ChatResponse{
				{ToolCalls: []core.ToolCall{{ID: "1", Name: "agent_worker", Args: []byte(`{"task":"work"}`)}}},
				{Content: "done"},
			},
		}
		child := &stubAgent{
			name: "worker",
			desc: "does work",
			fn:   func(_ agent.AgentTask) (agent.AgentResult, error) { return agent.AgentResult{Output: "ok"}, nil },
		}
		net := New("bench", "bench", router, WithChildren(child))
		if _, err := net.Execute(context.Background(), agent.AgentTask{Input: "go"}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNetworkExecute_AgentScaling(b *testing.B) {
	for _, n := range []int{1, 3, 5, 10, 20} {
		n := n
		b.Run(fmt.Sprintf("agents=%d", n), func(b *testing.B) {
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				agents := make([]core.Agent, n)
				for j := range agents {
					name := fmt.Sprintf("worker%d", j)
					agents[j] = &stubAgent{
						name: name,
						desc: "does work",
						fn:   func(_ agent.AgentTask) (agent.AgentResult, error) { return agent.AgentResult{Output: "ok"}, nil },
					}
				}
				router := &mockProvider{
					name: "router",
					responses: []core.ChatResponse{
						{ToolCalls: []core.ToolCall{{ID: "1", Name: "agent_worker0", Args: []byte(`{"task":"work"}`)}}},
						{Content: "done"},
					},
				}
				net := New("bench", "bench", router, WithChildren(agents...))
				if _, err := net.Execute(context.Background(), agent.AgentTask{Input: "go"}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkNetworkExecute_MultiDelegation(b *testing.B) {
	for _, delegations := range []int{1, 2, 3, 5} {
		delegations := delegations
		b.Run(fmt.Sprintf("delegations=%d", delegations), func(b *testing.B) {
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				agents := make([]core.Agent, delegations)
				for j := range agents {
					name := fmt.Sprintf("worker%d", j)
					agents[j] = &stubAgent{
						name: name,
						desc: "does work",
						fn:   func(_ agent.AgentTask) (agent.AgentResult, error) { return agent.AgentResult{Output: "ok"}, nil },
					}
				}

				responses := make([]core.ChatResponse, 0, delegations+1)
				for j := 0; j < delegations; j++ {
					agentName := fmt.Sprintf("agent_worker%d", j)
					responses = append(responses, core.ChatResponse{
						ToolCalls: []core.ToolCall{{ID: fmt.Sprintf("%d", j), Name: agentName, Args: []byte(`{"task":"work"}`)}},
					})
				}
				responses = append(responses, core.ChatResponse{Content: "done"})

				router := &mockProvider{name: "router", responses: responses}
				net := New("bench", "bench", router, WithChildren(agents...))
				if _, err := net.Execute(context.Background(), agent.AgentTask{Input: "go"}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkNetworkExecute_Stream(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		router := &mockProvider{
			name: "router",
			responses: []core.ChatResponse{
				{ToolCalls: []core.ToolCall{{ID: "1", Name: "agent_worker", Args: []byte(`{"task":"work"}`)}}},
				{Content: "done"},
			},
		}
		child := &stubAgent{
			name: "worker",
			desc: "does work",
			fn:   func(_ agent.AgentTask) (agent.AgentResult, error) { return agent.AgentResult{Output: "ok"}, nil },
		}
		net := New("bench", "bench", router, WithChildren(child))

		ch := make(chan core.StreamEvent, 64)
		go func() {
			for range ch {
			}
		}()

		if _, err := net.Execute(context.Background(), agent.AgentTask{Input: "go"}, core.WithStream(ch)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNetworkExecute_LargeAgentOutput(b *testing.B) {
	for _, kb := range []int{10, 100} {
		kb := kb
		b.Run(fmt.Sprintf("output=%dKB", kb), func(b *testing.B) {
			payload := strings.Repeat("x", kb*1024)
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				router := &mockProvider{
					name: "router",
					responses: []core.ChatResponse{
						{ToolCalls: []core.ToolCall{{ID: "1", Name: "agent_worker", Args: []byte(`{"task":"work"}`)}}},
						{Content: "done"},
					},
				}
				child := &stubAgent{
					name: "worker",
					desc: "does work",
					fn:   func(_ agent.AgentTask) (agent.AgentResult, error) { return agent.AgentResult{Output: payload}, nil },
				}
				net := New("bench", "bench", router, WithChildren(child))
				if _, err := net.Execute(context.Background(), agent.AgentTask{Input: "go"}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
