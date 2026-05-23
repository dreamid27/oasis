// memory/retrieve.go
package memory

import (
	"context"
	"log/slog"
	"strings"

	"github.com/nevindra/oasis/core"
)

const (
	defaultMaxHistory             = 10
	defaultKeepRecent             = 3
	defaultSemanticRecallMinScore = float32(0.60)
	maxRecallContentLen           = 500
	defaultRecallTopK             = 8
)

// RetrieveProcessor transforms a RetrieveContext on the hot path.
// Errors from processors are logged and the offending processor is skipped;
// the pipeline continues. The agent always gets some prompt.
type RetrieveProcessor interface {
	Process(ctx context.Context, in *RetrieveContext) error
}

// RetrieveContext carries everything a retrieve processor needs.
type RetrieveContext struct {
	AgentName string
	Task      core.AgentTask
	Embedding []float32

	History     []core.Message
	Selected    map[Kind][]MemoryItem // by Kind, set by BatchedRecall
	Pinned      []MemoryItem
	CrossThread []core.ScoredMessage

	SystemPrompt string
	PromptParts  []string

	Store        Store
	HistoryStore core.Store
	Embedder     core.EmbeddingProvider
	Logger       *slog.Logger
}

func runRetrievePipeline(ctx context.Context, in *RetrieveContext, procs []RetrieveProcessor) {
	for _, p := range procs {
		if err := p.Process(ctx, in); err != nil {
			in.Logger.Warn("retrieve processor error", "error", err)
		}
	}
}

// BuildMessages runs the retrieve pipeline and returns the LLM-ready message list.
func (m *AgentMemory) BuildMessages(ctx context.Context, agentName, systemPrompt string, task core.AgentTask) []core.ChatMessage {
	if m.tracer != nil {
		var span core.Span
		ctx, span = m.tracer.Start(ctx, "agent.memory.load",
			core.StringAttr("thread_id", task.ThreadID))
		defer span.End()
	}

	in := &RetrieveContext{
		AgentName:    agentName,
		Task:         task,
		Selected:     map[Kind][]MemoryItem{},
		SystemPrompt: systemPrompt,
		Store:        m.store,
		HistoryStore: m.store,
		Embedder:     m.embedding,
		Logger:       m.logger,
	}

	runRetrievePipeline(ctx, in, m.defaultRetrieveChain())

	// Assemble final []core.ChatMessage.
	var out []core.ChatMessage
	if assembled := strings.Join(append([]string{systemPrompt}, in.PromptParts...), "\n\n"); strings.TrimSpace(assembled) != "" {
		out = append(out, core.SystemMessage(assembled))
	}
	for _, msg := range in.History {
		out = append(out, core.ChatMessage{Role: core.Role(msg.Role), Content: msg.Content})
	}
	out = append(out, core.ChatMessage{
		Role: core.RoleUser, Content: task.Input, Attachments: task.Attachments,
	})
	return mergeAdjacentSystemMessages(out)
}

func (m *AgentMemory) defaultRetrieveChain() []RetrieveProcessor {
	chain := []RetrieveProcessor{
		EmbedInput{},
		LoadHistory{Limit: m.maxHistory},
	}
	if m.store != nil {
		chain = append(chain, LoadPinned{})
		chain = append(chain, BatchedRecall{
			Kinds: m.recallKinds,
			TopK:  m.recallTopK,
		})
	}
	if m.semanticRecall {
		chain = append(chain, RecallCrossThread{MinScore: m.semanticMinScore})
	}
	if m.maxTokens > 0 {
		chain = append(chain, TrimToBudget{Budget: m.maxTokens, Semantic: m.semanticTrimming})
	}
	chain = append(chain, m.retrieveProcs...)
	return chain
}

func mergeAdjacentSystemMessages(messages []core.ChatMessage) []core.ChatMessage {
	if len(messages) < 2 {
		return messages
	}
	out := make([]core.ChatMessage, 0, len(messages))
	for _, m := range messages {
		if len(out) > 0 && out[len(out)-1].Role == "system" && m.Role == "system" {
			prev := out[len(out)-1]
			if prev.Content == "" {
				prev.Content = m.Content
			} else if m.Content != "" {
				prev.Content = prev.Content + "\n\n" + m.Content
			}
			out[len(out)-1] = prev
			continue
		}
		out = append(out, m)
	}
	return out
}
