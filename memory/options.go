// memory/options.go
package memory

import (
	"log/slog"
	"time"

	"github.com/nevindra/oasis/core"
)

// Option configures an AgentMemoryConfig.
type Option func(*AgentMemoryConfig)

// WithStore binds the unified Store (core.Store + ItemStore).
func WithStore(s Store) Option { return func(c *AgentMemoryConfig) { c.Store = s } }

// WithEmbedding sets the embedding provider used for recall + dedupe + working memory.
func WithEmbedding(p core.EmbeddingProvider) Option {
	return func(c *AgentMemoryConfig) { c.Embedding = p }
}

// WithProvider sets the LLM provider used for extraction and title generation.
func WithProvider(p core.Provider) Option {
	return func(c *AgentMemoryConfig) { c.Provider = p }
}

// WithMaxHistory sets how many recent messages to load (default 10).
func WithMaxHistory(n int) Option { return func(c *AgentMemoryConfig) { c.MaxHistory = n } }

// WithMaxTokens caps the history portion of the prompt at n tokens.
func WithMaxTokens(n int) Option { return func(c *AgentMemoryConfig) { c.MaxTokens = n } }

// WithSemanticTrimming enables semantic-similarity trimming when over MaxTokens.
func WithSemanticTrimming() Option {
	return func(c *AgentMemoryConfig) { c.SemanticTrimming = true }
}

// WithSemanticTrimEmbedding configures a separate embedding provider for semantic
// history trimming, so a smaller/faster model can be used here than for
// cross-thread recall (WithEmbedding). If unset, WithEmbedding's provider is used.
func WithSemanticTrimEmbedding(e core.EmbeddingProvider) Option {
	return func(c *AgentMemoryConfig) { c.TrimmingEmbedding = e }
}

// WithKeepRecent sets how many recent messages SemanticTrim preserves regardless
// of relevance. Default 3.
func WithKeepRecent(n int) Option {
	return func(c *AgentMemoryConfig) { c.KeepRecent = n }
}

// WithCompaction wires a Compactor that runs when stored history exceeds
// threshold × effectiveWindow. Threshold is 0.0–1.0; recommended 0.80. Passing
// nil compactor or threshold <= 0 is a no-op. Requires a Store.
func WithCompaction(c core.Compactor, threshold float64) Option {
	return func(cfg *AgentMemoryConfig) {
		if c == nil || threshold <= 0 {
			return
		}
		cfg.Compactor = c
		cfg.CompactThreshold = threshold
	}
}

// WithCompress enables per-turn LLM-driven summarization when the in-memory
// message slice exceeds threshold runes. fn returns the model used for
// summarization (falls back to the agent's main provider if fn returns nil).
// threshold <= 0 disables. Does NOT require a Store — works on the in-memory
// slice during a single Execute.
func WithCompress(fn core.ModelFunc, threshold int) Option {
	return func(c *AgentMemoryConfig) {
		c.CompressModel = fn
		c.CompressThreshold = threshold
	}
}

// WithSemanticRecall enables cross-thread message recall (today's CrossThreadSearch).
func WithSemanticRecall() Option { return func(c *AgentMemoryConfig) { c.SemanticRecall = true } }

// WithSemanticRecallMinScore sets the cosine threshold for cross-thread recall.
func WithSemanticRecallMinScore(s float32) Option {
	return func(c *AgentMemoryConfig) { c.SemanticMinScore = s }
}

// WithRecallKinds configures which MemoryItem kinds are included in BatchedRecall.
// Defaults to [KindFact] when not set.
func WithRecallKinds(kinds ...Kind) Option {
	return func(c *AgentMemoryConfig) { c.RecallKinds = append([]Kind{}, kinds...) }
}

// WithRecallTopK sets the total top-K for BatchedRecall (default 8).
func WithRecallTopK(k int) Option { return func(c *AgentMemoryConfig) { c.RecallTopK = k } }

// WithWorkingMemory enables a markdown working-memory slot at the configured scope.
func WithWorkingMemory() Option {
	return func(c *AgentMemoryConfig) {
		c.WorkingMemory = true
		if c.WorkingMemoryScope == "" { c.WorkingMemoryScope = ScopeResource }
	}
}

// WithWorkingMemoryScope overrides the default Resource scope for working memory.
func WithWorkingMemoryScope(s ScopeKind) Option {
	return func(c *AgentMemoryConfig) { c.WorkingMemoryScope = s }
}

// WithAutoTitle enables LLM-driven thread title generation on the first turn.
func WithAutoTitle() Option { return func(c *AgentMemoryConfig) { c.AutoTitle = true } }

// WithDecayInterval reserved for future use; v1 uses probabilistic per-turn decay.
func WithDecayInterval(d time.Duration) Option {
	return func(c *AgentMemoryConfig) { /* reserved */ _ = d }
}

// WithTools registers agent-callable memory tools. Default OFF; pass
// the tools you want — typically constructed from an AgentMemory like:
//
//	var m memory.AgentMemory
//	oasis.WithMemory(memory.WithTools(m.AllTools()...), ...)
//
// In practice the agent layer wires this for you when the option chain
// includes WithTools — the tools are stored in AgentMemoryConfig.Tools
// and registered with the LLMAgent during oasis.WithMemory application.
func WithTools(tools ...core.AnyTool) Option {
	return func(c *AgentMemoryConfig) { c.Tools = append(c.Tools, tools...) }
}

// WithIngestProcessors appends user-supplied processors after the default chain.
func WithIngestProcessors(ps ...IngestProcessor) Option {
	return func(c *AgentMemoryConfig) { c.IngestProcs = append(c.IngestProcs, ps...) }
}

// WithRetrieveProcessors appends user-supplied processors after the default chain.
func WithRetrieveProcessors(ps ...RetrieveProcessor) Option {
	return func(c *AgentMemoryConfig) { c.RetrieveProcs = append(c.RetrieveProcs, ps...) }
}

// WithLogger sets the slog logger.
func WithLogger(l *slog.Logger) Option { return func(c *AgentMemoryConfig) { c.Logger = l } }

// WithTracer sets the OpenTelemetry tracer.
func WithTracer(t core.Tracer) Option { return func(c *AgentMemoryConfig) { c.Tracer = t } }

// BuildConfig applies the options and returns the resulting config.
func BuildConfig(opts ...Option) AgentMemoryConfig {
	var cfg AgentMemoryConfig
	for _, o := range opts { o(&cfg) }
	return cfg
}
