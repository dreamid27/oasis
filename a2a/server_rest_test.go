package a2a

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nevindra/oasis/a2a/a2atest"
)

// TestRESTSendAndGet exercises the four core REST routes:
//   - POST /message:send → 200 application/json sendResult with a completed task
//   - GET /tasks/{id}    → 200 bare Task with matching ID
//   - GET /tasks/{bad}   → 404 with a JSON rpcError body (codeTaskNotFound)
//   - POST /tasks/{id}:cancel on a terminal task → 409 with a JSON rpcError body (codeTaskNotCancelable)
func TestRESTSendAndGet(t *testing.T) {
	ts := httptest.NewServer(NewServer(a2atest.NewEchoAgent("echo", "echoes")))
	defer ts.Close()

	// POST /message:send — expects a completed task in the sendResult oneof.
	body, _ := json.Marshal(sendParams{
		Message: Message{MessageID: "m1", Role: RoleUser, Parts: []Part{TextPart("hi")}},
	})
	resp, err := http.Post(ts.URL+restMessageSend, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s: status = %d, want 200", restMessageSend, resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("POST %s: Content-Type = %q, want application/json", restMessageSend, ct)
	}
	var sr sendResult
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		t.Fatalf("decode sendResult: %v", err)
	}
	if sr.Task == nil {
		t.Fatal("sendResult.task missing")
	}
	if sr.Task.Status.State != TaskStateCompleted {
		t.Errorf("task state = %s, want completed", sr.Task.Status.State)
	}
	taskID := sr.Task.ID

	// GET /tasks/{id} — expects the same task back, bare (not wrapped).
	getResp, err := http.Get(ts.URL + "/tasks/" + taskID)
	if err != nil {
		t.Fatal(err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /tasks/%s: status = %d, want 200", taskID, getResp.StatusCode)
	}
	var task Task
	if err := json.NewDecoder(getResp.Body).Decode(&task); err != nil {
		t.Fatalf("decode Task: %v", err)
	}
	if task.ID != taskID {
		t.Errorf("task.ID = %q, want %q", task.ID, taskID)
	}

	// GET /tasks/{unknown} → 404 + JSON rpcError body with codeTaskNotFound.
	notFoundResp, err := http.Get(ts.URL + "/tasks/does-not-exist")
	if err != nil {
		t.Fatal(err)
	}
	defer notFoundResp.Body.Close()
	if notFoundResp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /tasks/does-not-exist: status = %d, want 404", notFoundResp.StatusCode)
	}
	var nfErr rpcError
	if err := json.NewDecoder(notFoundResp.Body).Decode(&nfErr); err != nil {
		t.Fatalf("decode rpcError: %v", err)
	}
	if nfErr.Code != codeTaskNotFound {
		t.Errorf("not-found error code = %d, want %d", nfErr.Code, codeTaskNotFound)
	}

	// POST /tasks/{id}:cancel on a terminal (completed) task → 409 + codeTaskNotCancelable.
	cancelResp, err := http.Post(ts.URL+"/tasks/"+taskID+":cancel", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cancelResp.Body.Close()
	if cancelResp.StatusCode != http.StatusConflict {
		t.Fatalf("POST /tasks/%s:cancel: status = %d, want 409", taskID, cancelResp.StatusCode)
	}
	var cancelErr rpcError
	if err := json.NewDecoder(cancelResp.Body).Decode(&cancelErr); err != nil {
		t.Fatalf("decode cancel rpcError: %v", err)
	}
	if cancelErr.Code != codeTaskNotCancelable {
		t.Errorf("cancel error code = %d, want %d", cancelErr.Code, codeTaskNotCancelable)
	}
}

// TestRESTStream verifies POST /message:stream delivers a text/event-stream
// response whose frames can be parsed by the shared readSSE helper and include
// a final completed status event.
func TestRESTStream(t *testing.T) {
	ts := httptest.NewServer(NewServer(a2atest.NewEchoAgent("echo", "echoes")))
	defer ts.Close()

	body, _ := json.Marshal(sendParams{
		Message: Message{MessageID: "m1", Role: RoleUser, Parts: []Part{TextPart("hello")}},
	})
	resp, err := http.Post(ts.URL+restMessageStream, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s: status = %d, want 200", restMessageStream, resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("POST %s: Content-Type = %q, want text/event-stream", restMessageStream, ct)
	}

	// readSSE parses the JSON-RPC-enveloped frames that handleMessageStream emits
	// on both the JSON-RPC and REST paths (the SSE writer is shared).
	frames := readBodyWithTimeout(t, resp, 5*time.Second)
	if len(frames) == 0 {
		t.Fatal("REST stream produced no frames")
	}

	var sawFinal bool
	for _, fr := range frames {
		if fr.StatusUpdate != nil && fr.StatusUpdate.Final {
			sawFinal = true
			if fr.StatusUpdate.Status.State != TaskStateCompleted {
				t.Errorf("final state = %s, want completed", fr.StatusUpdate.Status.State)
			}
		}
	}
	if !sawFinal {
		t.Error("missing final status frame")
	}
}

// TestRESTMethodMismatch verifies that routes are only accessible via their
// correct HTTP method:
//   - GET /message:send  → 405 (POST-only route)
//   - POST /tasks/{id}   → 405 (GET-only route)
func TestRESTMethodMismatch(t *testing.T) {
	ts := httptest.NewServer(NewServer(a2atest.NewEchoAgent("echo", "echoes")))
	defer ts.Close()

	// GET on a POST-only REST route must return 405.
	getResp, err := http.Get(ts.URL + restMessageSend)
	if err != nil {
		t.Fatal(err)
	}
	getResp.Body.Close()
	if getResp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET %s: status = %d, want 405", restMessageSend, getResp.StatusCode)
	}

	// POST on a GET-only REST route must return 405.
	postResp, err := http.Post(ts.URL+"/tasks/some-id", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	postResp.Body.Close()
	if postResp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /tasks/some-id: status = %d, want 405", postResp.StatusCode)
	}
}
