package core

import "context"

// Chat is a non-streaming convenience wrapper around Provider.ChatStream.
// It passes a nil channel, which tells the provider to skip event emission.
// For UI-facing streaming, call ChatStream directly with a non-nil channel.
func Chat(ctx context.Context, p Provider, req ChatRequest) (ChatResponse, error) {
	return p.ChatStream(ctx, req, nil)
}
