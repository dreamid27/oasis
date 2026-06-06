package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nevindra/oasis/a2a/a2atest"
	"github.com/nevindra/oasis/core"
)

// TestDialAndExecute proves a RemoteAgent dialed against a loopback A2A server
// is a drop-in core.Agent: identity comes from the card and a blocking Execute
// returns the agent's output with FinishStop.
func TestDialAndExecute(t *testing.T) {
	ts := httptest.NewServer(NewServer(a2atest.NewEchoAgent("echo", "echoes input")))
	defer ts.Close()

	remote, err := Dial(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	var _ core.Agent = remote // compile-time: RemoteAgent IS a core.Agent

	if remote.Name() != "echo" {
		t.Errorf("Name() = %q, want %q", remote.Name(), "echo")
	}
	if remote.Description() != "echoes input" {
		t.Errorf("Description() = %q, want %q", remote.Description(), "echoes input")
	}

	res, err := remote.Execute(context.Background(), core.AgentTask{Input: "hello"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Output != "echo: hello" {
		t.Errorf("Output = %q, want %q", res.Output, "echo: hello")
	}
	if res.FinishReason != core.FinishStop {
		t.Errorf("FinishReason = %q, want %q", res.FinishReason, core.FinishStop)
	}
}

// TestRemoteExecuteStreaming proves text deltas stream through the SSE transport
// AND the final assembled Output is correct.
func TestRemoteExecuteStreaming(t *testing.T) {
	ts := httptest.NewServer(NewServer(a2atest.NewEchoAgent("echo", "echoes input")))
	defer ts.Close()

	remote, err := Dial(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	ch := make(chan core.StreamEvent, 64)
	var deltas string
	collected := make(chan struct{})
	go func() {
		defer close(collected)
		for ev := range ch {
			if ev.Type == core.EventTextDelta {
				deltas += ev.Content
			}
		}
	}()

	res, err := remote.Execute(context.Background(), core.AgentTask{Input: "hi"}, core.WithStream(ch))
	if err != nil {
		t.Fatalf("Execute(stream): %v", err)
	}
	<-collected

	if deltas != "echo: hi" {
		t.Errorf("streamed deltas = %q, want %q", deltas, "echo: hi")
	}
	if res.Output != "echo: hi" {
		t.Errorf("final Output = %q, want %q", res.Output, "echo: hi")
	}
}

// TestRemoteSuspendResume proves the RemoteAgent mirrors server-side suspend
// semantics: the first Execute suspends (FinishSuspended + payload), and a
// second Execute on the SAME ThreadID resumes the pending remote task.
func TestRemoteSuspendResume(t *testing.T) {
	ts := httptest.NewServer(NewServer(a2atest.NewSuspendingAgent("hitl", "asks first")))
	defer ts.Close()

	remote, err := Dial(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	first, err := remote.Execute(context.Background(), core.AgentTask{Input: "start", ThreadID: "th-1"})
	if err != nil {
		t.Fatalf("Execute(first): %v", err)
	}
	if first.FinishReason != core.FinishSuspended {
		t.Fatalf("first FinishReason = %q, want %q", first.FinishReason, core.FinishSuspended)
	}
	if len(first.SuspendPayload) == 0 {
		t.Error("first SuspendPayload is empty; want the agent's question")
	}

	second, err := remote.Execute(context.Background(), core.AgentTask{Input: "the answer", ThreadID: "th-1"})
	if err != nil {
		t.Fatalf("Execute(resume): %v", err)
	}
	if second.FinishReason != core.FinishStop {
		t.Errorf("resume FinishReason = %q, want %q", second.FinishReason, core.FinishStop)
	}
	if second.Output == "" {
		t.Error("resume Output is empty; want the completed result")
	}
}

// TestRemoteFailedTask proves a remote task that ends FAILED surfaces as a Go
// error satisfying errors.Is(err, ErrInvalidAgentResp) with FinishError — the
// sentinel survives the network boundary.
func TestRemoteFailedTask(t *testing.T) {
	ts := httptest.NewServer(NewServer(a2atest.NewFailingAgent("broken", "always fails")))
	defer ts.Close()

	remote, err := Dial(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	res, err := remote.Execute(context.Background(), core.AgentTask{Input: "x"})
	if err == nil {
		t.Fatal("Execute: want error for a FAILED remote task, got nil")
	}
	if !errors.Is(err, ErrInvalidAgentResp) {
		t.Errorf("error = %v, want errors.Is(err, ErrInvalidAgentResp)", err)
	}
	if res.FinishReason != core.FinishError {
		t.Errorf("FinishReason = %q, want %q", res.FinishReason, core.FinishError)
	}
}

// TestRemoteAuthRequired is a regression test for the poll-loop bug: when a
// remote task transitions to TASK_STATE_AUTH_REQUIRED the Execute call must
// return promptly with FinishSuspended instead of looping forever.
//
// The server is a raw httptest.Server (not a2a.NewServer) so we control the
// exact wire response — we serve the agent card at WellKnownCardPath and reply
// to SendMessage with an auth-required task, simulating a mid-flight auth
// challenge.
func TestRemoteAuthRequired(t *testing.T) {
	// Build the auth-required task that the stub server returns.
	authTask := Task{
		ID:        "task-auth-1",
		ContextID: "ctx-1",
		Status: TaskStatus{
			State: TaskStateAuthRequired,
			Message: &Message{
				MessageID: "msg-auth-1",
				Role:      RoleAgent,
				Parts:     []Part{TextPart("please authenticate via OAuth")},
			},
			Timestamp: nowRFC3339(),
		},
	}

	// Minimal agent card: no SupportedInterfaces so Dial falls back to baseURL
	// as the JSON-RPC endpoint.
	card := AgentCard{
		Name:         "auth-agent",
		Description:  "requires auth",
		Capabilities: AgentCapabilities{},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case WellKnownCardPath:
			json.NewEncoder(w).Encode(card) //nolint:errcheck
		default:
			// Any JSON-RPC call (SendMessage, GetTask, …) gets the auth-required
			// task back. We parse the request just enough to echo the ID.
			var req rpcRequest
			json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
			resp := marshalResult(req.ID, sendResult{Task: &authTask})
			json.NewEncoder(w).Encode(resp) //nolint:errcheck
		}
	}))
	defer ts.Close()

	remote, err := Dial(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	// The Execute must return well within a second — wrap it in a short timeout
	// so a regression (infinite poll) turns into a test failure rather than a
	// hung test.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	res, err := remote.Execute(ctx, core.AgentTask{Input: "do something", ThreadID: "th-auth"})
	if err != nil {
		t.Fatalf("Execute: unexpected error: %v", err)
	}
	if res.FinishReason != core.FinishSuspended {
		t.Errorf("FinishReason = %q, want %q", res.FinishReason, core.FinishSuspended)
	}
	// The status message text must surface as SuspendPayload.
	if string(res.SuspendPayload) != "please authenticate via OAuth" {
		t.Errorf("SuspendPayload = %q, want %q", res.SuspendPayload, "please authenticate via OAuth")
	}
	// Verify the test did NOT time out (ctx still valid).
	if ctx.Err() != nil {
		t.Errorf("context expired — Execute likely looped instead of returning: %v", ctx.Err())
	}
}

// TestRemoteStreamNoLastChunk is a regression test for the cross-vendor
// streaming bug: a server that emits append-delta artifact chunks followed by
// a final completed-status frame (WITHOUT a LastChunk artifact replay) must
// still yield a correct non-empty res.Output and forward all deltas to the
// stream channel.
//
// Why: LastChunk replay is an Oasis server behavior, not guaranteed by the A2A
// spec. Before the fix, executeStream only accumulated artifacts from LastChunk
// frames; a server that skipped them would produce Output == "" on Completed.
func TestRemoteStreamNoLastChunk(t *testing.T) {
	const taskID = "task-nlc-1"
	const ctxID = "ctx-nlc-1"

	// Minimal agent card — no SupportedInterfaces so Dial falls back to baseURL.
	card := AgentCard{
		Name:         "nlc-agent",
		Description:  "no-last-chunk streaming agent",
		Capabilities: AgentCapabilities{Streaming: true},
	}

	// writeSSEFrame wraps v in a JSON-RPC 2.0 SSE result envelope.
	writeSSEFrame := func(w http.ResponseWriter, v any) {
		raw, _ := json.Marshal(v)
		resp := rpcResponse{JSONRPC: "2.0", Result: raw}
		b, _ := json.Marshal(resp)
		fmt.Fprintf(w, "data: %s\n\n", b)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case WellKnownCardPath:
			json.NewEncoder(w).Encode(card) //nolint:errcheck
		default:
			// Decode the request to check the method.
			var req rpcRequest
			json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck

			if req.Method != methodSendStreamingMessage {
				// Fallback: non-streaming calls get a completed task.
				resp := marshalResult(req.ID, sendResult{Task: &Task{
					ID: taskID, ContextID: ctxID,
					Status: TaskStatus{State: TaskStateCompleted, Timestamp: nowRFC3339()},
				}})
				json.NewEncoder(w).Encode(resp) //nolint:errcheck
				return
			}

			// SSE response: working status → two append-only artifact chunks → final
			// completed status. NO LastChunk frame — simulates a non-Oasis server.
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)

			// 1. Working status
			writeSSEFrame(w, StreamResponse{
				StatusUpdate: &TaskStatusUpdateEvent{
					TaskID: taskID, ContextID: ctxID,
					Status: TaskStatus{State: TaskStateWorking, Timestamp: nowRFC3339()},
				},
			})
			// 2. First delta chunk ("he") — append=true, lastChunk=false
			writeSSEFrame(w, StreamResponse{
				ArtifactUpdate: &TaskArtifactUpdateEvent{
					TaskID:   taskID,
					Artifact: Artifact{Parts: []Part{TextPart("he")}},
					Append:   true,
				},
			})
			// 3. Second delta chunk ("llo") — append=true, lastChunk=false
			writeSSEFrame(w, StreamResponse{
				ArtifactUpdate: &TaskArtifactUpdateEvent{
					TaskID:   taskID,
					Artifact: Artifact{Parts: []Part{TextPart("llo")}},
					Append:   true,
				},
			})
			// 4. Final completed status — no LastChunk artifact replay
			writeSSEFrame(w, StreamResponse{
				StatusUpdate: &TaskStatusUpdateEvent{
					TaskID: taskID, ContextID: ctxID,
					Status: TaskStatus{State: TaskStateCompleted, Timestamp: nowRFC3339()},
					Final:  true,
				},
			})
		}
	}))
	defer ts.Close()

	remote, err := Dial(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	ch := make(chan core.StreamEvent, 64)
	var deltas string
	collected := make(chan struct{})
	go func() {
		defer close(collected)
		for ev := range ch {
			if ev.Type == core.EventTextDelta {
				deltas += ev.Content
			}
		}
	}()

	res, err := remote.Execute(context.Background(), core.AgentTask{Input: "hi"}, core.WithStream(ch))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	<-collected

	// Both delta chunks must have been forwarded.
	if deltas != "hello" {
		t.Errorf("streamed deltas = %q, want %q", deltas, "hello")
	}
	// Without the fix, Output would be "" because no LastChunk frame was sent.
	if res.Output != "hello" {
		t.Errorf("res.Output = %q, want %q (delta fallback must fill in missing artifacts)", res.Output, "hello")
	}
	if res.FinishReason != core.FinishStop {
		t.Errorf("FinishReason = %q, want %q", res.FinishReason, core.FinishStop)
	}
}
