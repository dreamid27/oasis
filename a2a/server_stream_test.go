package a2a

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nevindra/oasis/a2a/a2atest"
)

// readSSE reads an SSE body, unwraps each "data:" frame's JSON-RPC envelope,
// and returns the StreamResponse payloads in order. It stops at EOF. The
// scanner buffer is sized generously because a single artifact frame can carry
// a large response part (16MB ceiling mirrors the server's own per-frame cap).
func readSSE(t *testing.T, body io.Reader) []StreamResponse {
	t.Helper()
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	var out []StreamResponse
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue // blank lines / comments between frames
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var env rpcResponse
		if err := json.Unmarshal([]byte(payload), &env); err != nil {
			t.Fatalf("frame is not a valid JSON-RPC envelope: %q: %v", payload, err)
		}
		if env.JSONRPC != "2.0" {
			t.Fatalf("frame envelope missing jsonrpc 2.0: %q", payload)
		}
		var sr StreamResponse
		if err := json.Unmarshal(env.Result, &sr); err != nil {
			t.Fatalf("frame result is not a StreamResponse: %q: %v", env.Result, err)
		}
		out = append(out, sr)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan SSE body: %v", err)
	}
	return out
}

// postStream issues a streaming JSON-RPC request and returns the live response.
// The caller closes resp.Body. A read deadline on the underlying transport is
// the test's safety net against a wedged handler — see the per-test timeouts.
func postStream(t *testing.T, url, method string, params any) *http.Response {
	t.Helper()
	p, _ := json.Marshal(params)
	body, _ := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: method, Params: p})
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// readBodyWithTimeout reads resp.Body fully but fails the test if the read does
// not complete within d — the guard that turns a wedged streaming handler into
// a test failure instead of a hung suite.
func readBodyWithTimeout(t *testing.T, resp *http.Response, d time.Duration) []StreamResponse {
	t.Helper()
	type result struct {
		frames []StreamResponse
	}
	done := make(chan result, 1)
	go func() {
		frames := readSSE(t, resp.Body)
		done <- result{frames: frames}
	}()
	select {
	case r := <-done:
		return r.frames
	case <-time.After(d):
		resp.Body.Close() // unblock the reader goroutine
		t.Fatalf("streaming handler did not terminate within %s (possible wedge)", d)
		return nil
	}
}

func TestMessageStream(t *testing.T) {
	srv := NewServer(a2atest.NewEchoAgent("echo", "echoes"))
	defer srv.Close()
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp := postStream(t, ts.URL, methodSendStreamingMessage, sendParams{
		Message: Message{MessageID: "m1", Role: RoleUser, Parts: []Part{TextPart("hi")}},
	})
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	frames := readBodyWithTimeout(t, resp, 5*time.Second)

	var sawWorking, sawFinal bool
	var finalState TaskState
	var appended strings.Builder
	for _, fr := range frames {
		switch {
		case fr.StatusUpdate != nil:
			if fr.StatusUpdate.Status.State == TaskStateWorking && !fr.StatusUpdate.Final {
				sawWorking = true
			}
			if fr.StatusUpdate.Final {
				sawFinal = true
				finalState = fr.StatusUpdate.Status.State
			}
		case fr.ArtifactUpdate != nil:
			// Only the streamed deltas carry Append:true; the final replay frame
			// is LastChunk and would double-count the text otherwise.
			if fr.ArtifactUpdate.Append {
				for _, p := range fr.ArtifactUpdate.Artifact.Parts {
					appended.WriteString(p.Text)
				}
			}
		}
	}

	if !sawWorking {
		t.Error("missing initial working status update")
	}
	if !sawFinal {
		t.Error("missing final status update")
	}
	if finalState != TaskStateCompleted {
		t.Errorf("final state = %s, want completed", finalState)
	}
	if got := appended.String(); got != "echo: hi" {
		t.Errorf("concatenated append chunks = %q, want %q", got, "echo: hi")
	}
}

func TestResubscribeAfterCompletion(t *testing.T) {
	srv := NewServer(a2atest.NewEchoAgent("echo", "echoes"))
	defer srv.Close()
	ts := httptest.NewServer(srv)
	defer ts.Close()

	// Complete a task over the regular blocking transport first.
	task := sendTask(t, rpcCall(t, ts.URL, methodSendMessage, sendParams{
		Message: Message{MessageID: "m1", Role: RoleUser, Parts: []Part{TextPart("hi")}},
	}))
	if task.Status.State != TaskStateCompleted {
		t.Fatalf("setup: state = %s", task.Status.State)
	}

	// Resubscribe to the already-terminal task: the replay tail must be a Final
	// completed status frame.
	resp := postStream(t, ts.URL, methodSubscribeToTask, taskIDParams{ID: task.ID})
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	frames := readBodyWithTimeout(t, resp, 5*time.Second)
	if len(frames) == 0 {
		t.Fatal("resubscribe produced no frames")
	}
	last := frames[len(frames)-1]
	if last.StatusUpdate == nil || !last.StatusUpdate.Final {
		t.Fatalf("last frame is not a final status update: %+v", last)
	}
	if last.StatusUpdate.Status.State != TaskStateCompleted {
		t.Errorf("replayed final state = %s, want completed", last.StatusUpdate.Status.State)
	}
}

func TestResubscribeUnknownTask(t *testing.T) {
	srv := NewServer(a2atest.NewEchoAgent("echo", "echoes"))
	defer srv.Close()
	ts := httptest.NewServer(srv)
	defer ts.Close()

	// An unknown task ID is a JSON-RPC error response (not SSE).
	resp := rpcCall(t, ts.URL, methodSubscribeToTask, taskIDParams{ID: "does-not-exist"})
	if resp.Error == nil || resp.Error.Code != codeTaskNotFound {
		t.Errorf("want %d, got %+v", codeTaskNotFound, resp.Error)
	}
}

func TestMessageStreamRejectsResume(t *testing.T) {
	srv := NewServer(a2atest.NewEchoAgent("echo", "echoes"))
	defer srv.Close()
	ts := httptest.NewServer(srv)
	defer ts.Close()

	// A streaming message carrying a TaskID is a resume attempt; v1 rejects it
	// with a JSON-RPC error rather than silently starting a fresh task.
	resp := rpcCall(t, ts.URL, methodSendStreamingMessage, sendParams{
		Message: Message{MessageID: "m1", TaskID: "t1", Role: RoleUser, Parts: []Part{TextPart("more")}},
	})
	if resp.Error == nil || resp.Error.Code != codeUnsupportedOp {
		t.Errorf("resume over stream: want %d, got %+v", codeUnsupportedOp, resp.Error)
	}
}

func TestMessageStreamEmptyParts(t *testing.T) {
	srv := NewServer(a2atest.NewEchoAgent("echo", "echoes"))
	defer srv.Close()
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp := rpcCall(t, ts.URL, methodSendStreamingMessage, sendParams{
		Message: Message{MessageID: "m1", Role: RoleUser},
	})
	if resp.Error == nil || resp.Error.Code != codeInvalidParams {
		t.Errorf("empty parts: want %d, got %+v", codeInvalidParams, resp.Error)
	}
}

func TestMessageStreamPanicAgent(t *testing.T) {
	srv := NewServer(a2atest.NewPanicAgent("panic-agent", "always panics"))
	defer srv.Close()
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp := postStream(t, ts.URL, methodSendStreamingMessage, sendParams{
		Message: Message{MessageID: "m1", Role: RoleUser, Parts: []Part{TextPart("boom")}},
	})
	defer resp.Body.Close()

	// The handler must terminate even though PanicAgent never closes the stream
	// channel — the timeout guard fails the test on a wedge.
	frames := readBodyWithTimeout(t, resp, 5*time.Second)
	if len(frames) == 0 {
		t.Fatal("panic stream produced no frames")
	}
	last := frames[len(frames)-1]
	if last.StatusUpdate == nil || !last.StatusUpdate.Final {
		t.Fatalf("last frame is not a final status update: %+v", last)
	}
	if last.StatusUpdate.Status.State != TaskStateFailed {
		t.Errorf("final state = %s, want failed", last.StatusUpdate.Status.State)
	}
	if msg := last.StatusUpdate.Status.Message; msg == nil || len(msg.Parts) == 0 ||
		!strings.Contains(msg.Parts[0].Text, "panicked") {
		t.Errorf("final failure message must mention 'panicked': %+v", last.StatusUpdate.Status.Message)
	}
}

func TestMessageStreamSuspend(t *testing.T) {
	srv := NewServer(a2atest.NewSuspendingAgent("hitl", "asks first"))
	defer srv.Close()
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp := postStream(t, ts.URL, methodSendStreamingMessage, sendParams{
		Message: Message{MessageID: "m1", Role: RoleUser, Parts: []Part{TextPart("start")}},
	})
	defer resp.Body.Close()

	frames := readBodyWithTimeout(t, resp, 5*time.Second)
	if len(frames) == 0 {
		t.Fatal("suspend stream produced no frames")
	}
	last := frames[len(frames)-1]
	if last.StatusUpdate == nil || !last.StatusUpdate.Final {
		t.Fatalf("last frame is not a final status update: %+v", last)
	}
	if last.StatusUpdate.Status.State != TaskStateInputRequired {
		t.Errorf("final state = %s, want input-required", last.StatusUpdate.Status.State)
	}
	if msg := last.StatusUpdate.Status.Message; msg == nil || len(msg.Parts) == 0 ||
		!strings.Contains(msg.Parts[0].Text, "fiscal year") {
		t.Errorf("input-required must carry the question: %+v", last.StatusUpdate.Status.Message)
	}
}

// TestStreamCanceledByClose verifies IMPORTANT-1: Server.Close() cancels an
// in-flight streaming run and the stream terminates promptly with a canceled
// final frame. A BlockingAgent blocks until its context is done, so the only
// way the stream can end is if Close() propagates cancellation via
// context.AfterFunc into the streaming run's runCtx.
func TestStreamCanceledByClose(t *testing.T) {
	srv := NewServer(a2atest.NewBlockingAgent("blocker", "blocks until canceled"))
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp := postStream(t, ts.URL, methodSendStreamingMessage, sendParams{
		Message: Message{MessageID: "m1", Role: RoleUser, Parts: []Part{TextPart("go")}},
	})
	defer resp.Body.Close()

	// Start reading frames in the background; give the request a moment to
	// reach the blocking agent before we close the server.
	type result struct{ frames []StreamResponse }
	ch := make(chan result, 1)
	go func() {
		frames := readSSE(t, resp.Body)
		ch <- result{frames: frames}
	}()

	// Small delay so the blocking agent is actually inside Execute before Close.
	// 50ms is generous for a goroutine that does nothing but park on ctx.Done.
	time.Sleep(50 * time.Millisecond)
	srv.Close() // must cancel the streaming run

	select {
	case r := <-ch:
		if len(r.frames) == 0 {
			t.Fatal("Close()-canceled stream produced no frames")
		}
		last := r.frames[len(r.frames)-1]
		if last.StatusUpdate == nil || !last.StatusUpdate.Final {
			t.Fatalf("last frame must be a final status update: %+v", last)
		}
		// The run was canceled by Close(), so the task must be canceled, not failed.
		if last.StatusUpdate.Status.State != TaskStateCanceled {
			t.Errorf("Close() must produce canceled final state, got %s", last.StatusUpdate.Status.State)
		}
	case <-time.After(3 * time.Second):
		resp.Body.Close()
		t.Fatal("stream did not terminate within 3s after Server.Close() — AfterFunc cancellation may be broken")
	}
}

// recordingFlusher captures SSE writes and satisfies http.Flusher so newSSEWriter
// accepts it.
type recordingFlusher struct {
	httptest.ResponseRecorder
}

func (r *recordingFlusher) Flush() {}

func TestSSEWriterFraming(t *testing.T) {
	rec := &recordingFlusher{ResponseRecorder: *httptest.NewRecorder()}
	sw, ok := newSSEWriter(rec, json.RawMessage(`7`))
	if !ok {
		t.Fatal("newSSEWriter rejected a flushable writer")
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}

	sw.send(StreamResponse{StatusUpdate: &TaskStatusUpdateEvent{
		TaskID:    "t1",
		ContextID: "c1",
		Status:    TaskStatus{State: TaskStateWorking},
	}})

	got := rec.Body.String()
	if !strings.HasPrefix(got, "data: ") {
		t.Fatalf("frame missing 'data: ' prefix: %q", got)
	}
	if !strings.HasSuffix(got, "\n\n") {
		t.Fatalf("frame must terminate with a blank line: %q", got)
	}
	// The payload after stripping "data: " and the trailing blank line must be
	// valid JSON: a JSON-RPC envelope wrapping the StreamResponse.
	payload := strings.TrimSpace(strings.TrimPrefix(got, "data: "))
	var env rpcResponse
	if err := json.Unmarshal([]byte(payload), &env); err != nil {
		t.Fatalf("frame payload is not valid JSON: %q: %v", payload, err)
	}
	if env.JSONRPC != "2.0" {
		t.Errorf("envelope jsonrpc = %q, want 2.0", env.JSONRPC)
	}
	if string(env.ID) != "7" {
		t.Errorf("envelope id = %s, want 7", env.ID)
	}
	var sr StreamResponse
	if err := json.Unmarshal(env.Result, &sr); err != nil {
		t.Fatalf("envelope result is not a StreamResponse: %v", err)
	}
	if sr.StatusUpdate == nil || sr.StatusUpdate.TaskID != "t1" {
		t.Errorf("decoded StreamResponse wrong: %+v", sr)
	}
}

// TestSSEWriterNilID verifies a missing request id renders as JSON null, not as
// an empty token that would break the envelope.
func TestSSEWriterNilID(t *testing.T) {
	rec := &recordingFlusher{ResponseRecorder: *httptest.NewRecorder()}
	sw, ok := newSSEWriter(rec, nil)
	if !ok {
		t.Fatal("newSSEWriter rejected a flushable writer")
	}
	sw.send(StreamResponse{StatusUpdate: &TaskStatusUpdateEvent{TaskID: "t1"}})
	payload := strings.TrimSpace(strings.TrimPrefix(rec.Body.String(), "data: "))
	var env rpcResponse
	if err := json.Unmarshal([]byte(payload), &env); err != nil {
		t.Fatalf("frame payload is not valid JSON with nil id: %q: %v", payload, err)
	}
}
