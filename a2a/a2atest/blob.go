package a2atest

import (
	"context"

	"github.com/nevindra/oasis/core"
)

// BlobAgent returns a fixed binary attachment — payload-scaling benchmarks.
// Execute returns a single "blob ready" text output plus one file attachment
// carrying the blob data verbatim. Zero dependencies, instant responses.
type BlobAgent struct {
	name, desc string
	blob       []byte
}

// NewBlobAgent constructs a BlobAgent with the given name, description, and
// binary payload. The blob is referenced directly (not copied) so callers can
// pre-allocate once and reuse across iterations.
func NewBlobAgent(name, desc string, blob []byte) *BlobAgent {
	return &BlobAgent{name: name, desc: desc, blob: blob}
}

func (a *BlobAgent) Name() string        { return a.name }
func (a *BlobAgent) Description() string { return a.desc }

func (a *BlobAgent) Execute(_ context.Context, _ core.AgentTask, opts ...core.RunOption) (core.AgentResult, error) {
	cfg := core.ApplyRunOptions(opts...)
	if cfg.Stream != nil {
		close(cfg.Stream)
	}
	return core.AgentResult{
		Output: "blob ready",
		Files: []core.Attachment{
			{MimeType: "application/octet-stream", Data: a.blob},
		},
		FinishReason: core.FinishStop,
	}, nil
}
