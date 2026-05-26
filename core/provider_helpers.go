package core

import (
	"context"
	"sync"
)

// donePool recycles the chan struct{} used to signal drain completion in Chat.
// This eliminates one small allocation per non-streaming call.
var donePool = sync.Pool{
	New: func() any {
		// Unbuffered: sender (goroutine) closes it, receiver blocks until close.
		return make(chan struct{})
	},
}

// Chat is a non-streaming convenience wrapper around Provider.ChatStream.
// It discards stream events and returns the final assembled response.
// For UI-facing streaming, call ChatStream directly.
//
// The event channel is sized 1 — sufficient to keep the provider unblocked
// since the drain goroutine immediately discards every received event.
// This avoids the ~5 KB ring-buffer allocation of the former cap-64 channel
// while preserving the same concurrency contract.
func Chat(ctx context.Context, p Provider, req ChatRequest) (ChatResponse, error) {
	// cap=1: provider never stalls on the first send; the drain goroutine is
	// always immediately ready to receive (it does nothing else).
	ch := make(chan StreamEvent, 1)

	done := donePool.Get().(chan struct{})
	go func() {
		for range ch { // discard all events
		}
		// Signal completion then return the done channel to the pool.
		// We close a fresh wrapper instead of the pooled channel itself so the
		// pool entry remains reusable.  A one-shot struct{}{} send (non-closing)
		// lets us PUT the channel back without resetting it.
		done <- struct{}{}
	}()
	resp, err := p.ChatStream(ctx, req, ch)
	<-done
	donePool.Put(done)
	return resp, err
}
