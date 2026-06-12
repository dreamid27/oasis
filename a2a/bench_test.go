package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/nevindra/oasis/a2a/a2atest"
	"github.com/nevindra/oasis/core"
)

// benchSendParams serializes a minimal sendParams for BenchmarkA2AServer_MessageSend
// to reuse across iterations — avoiding marshal overhead inside the hot loop.
func benchSendParams(t testing.TB) json.RawMessage {
	t.Helper()
	msg := Message{
		MessageID: "bench-msg",
		Role:      RoleUser,
		Parts:     []Part{TextPart("hello")},
	}
	raw, err := json.Marshal(sendParams{Message: msg})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// BenchmarkA2AServer_MessageSend measures the handler path in isolation:
// decode → execute (echo agent, instant) → persist → encode. No network.
// This is the server-side baseline tax.
func BenchmarkA2AServer_MessageSend(b *testing.B) {
	srv := NewServer(a2atest.NewEchoAgent("echo", "echoes"))
	defer srv.Close()
	params := benchSendParams(b)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, rpcErr := srv.handleMessageSend(ctx, params); rpcErr != nil {
			b.Fatalf("handleMessageSend: %+v", rpcErr)
		}
	}
}

// BenchmarkA2ARoundTrip measures the full client→server loopback over a real
// httptest listener (real TCP sockets, localhost). Dial is called once outside
// the loop; Execute is the measured hot path.
func BenchmarkA2ARoundTrip(b *testing.B) {
	ts := httptest.NewServer(NewServer(a2atest.NewEchoAgent("echo", "echoes")))
	defer ts.Close()

	remote, err := Dial(context.Background(), ts.URL)
	if err != nil {
		b.Fatal(err)
	}

	task := core.AgentTask{Input: "hello"}
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := remote.Execute(ctx, task); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkA2ARoundTrip_Stream measures the streaming loopback path:
// SSE event translation both directions over a real httptest listener.
// A fresh buffered channel is created per iteration; a drain goroutine
// consumes events so the benchmark does not deadlock on a full channel.
func BenchmarkA2ARoundTrip_Stream(b *testing.B) {
	ts := httptest.NewServer(NewServer(a2atest.NewEchoAgent("echo", "echoes")))
	defer ts.Close()

	remote, err := Dial(context.Background(), ts.URL)
	if err != nil {
		b.Fatal(err)
	}

	task := core.AgentTask{Input: "hello"}
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ch := make(chan core.StreamEvent, 16)
		done := make(chan struct{})
		go func() {
			for range ch {
			}
			close(done)
		}()
		if _, err := remote.Execute(ctx, task, core.WithStream(ch)); err != nil {
			b.Fatal(err)
		}
		<-done
	}
}

// BenchmarkA2ARoundTrip_LargeArtifact measures payload scaling: the blob
// agent returns a binary attachment of the given size. b.SetBytes tracks
// throughput; B/op must remain ~1x payload (zero-copy passthrough).
func BenchmarkA2ARoundTrip_LargeArtifact(b *testing.B) {
	sizes := []struct {
		name  string
		bytes int
	}{
		{"10KB", 10 * 1024},
		{"100KB", 100 * 1024},
		{"1024KB", 1024 * 1024},
	}

	for _, tc := range sizes {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			blob := bytes.Repeat([]byte("x"), tc.bytes)
			ts := httptest.NewServer(NewServer(a2atest.NewBlobAgent("blob", "returns blob", blob)))
			defer ts.Close()

			remote, err := Dial(context.Background(), ts.URL)
			if err != nil {
				b.Fatal(err)
			}

			task := core.AgentTask{Input: fmt.Sprintf("send %d bytes", tc.bytes)}
			ctx := context.Background()

			b.SetBytes(int64(tc.bytes))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := remote.Execute(ctx, task); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkA2ATaskStore measures the in-memory store under parallel read
// contention: a store pre-populated with 1024 completed tasks, read by
// b.RunParallel with sequential task IDs cycling through the 1024 entries.
func BenchmarkA2ATaskStore(b *testing.B) {
	const preload = 1024
	store := newMemoryStore(4096)
	ctx := context.Background()

	ids := make([]string, preload)
	for i := 0; i < preload; i++ {
		e := &TaskRecord{
			Task: Task{
				ID:        fmt.Sprintf("task-%04d", i),
				ContextID: "bench-ctx",
				Status:    TaskStatus{State: TaskStateWorking, Timestamp: nowRFC3339()},
			},
		}
		ids[i] = e.Task.ID
		if err := store.Save(ctx, e); err != nil {
			b.Fatal(err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			id := ids[i%preload]
			i++
			if _, err := store.Get(ctx, id); err != nil {
				b.Fatal(err)
			}
		}
	})
}
