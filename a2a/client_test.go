package a2a

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nevindra/oasis/a2a/a2atest"
)

// TestDialErrors proves Dial returns observable errors when the card cannot be
// fetched (404) or cannot be decoded (invalid JSON), and the error mentions the
// URL it tried.
func TestDialErrors(t *testing.T) {
	t.Run("card 404", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "not found", http.StatusNotFound)
		}))
		defer ts.Close()

		_, err := Dial(context.Background(), ts.URL)
		if err == nil {
			t.Fatal("Dial: want error for 404 card, got nil")
		}
		if !strings.Contains(err.Error(), ts.URL) {
			t.Errorf("error %q does not mention URL %q", err, ts.URL)
		}
	})

	t.Run("invalid json card", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == WellKnownCardPath {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{not valid json`))
				return
			}
			http.Error(w, "not found", http.StatusNotFound)
		}))
		defer ts.Close()

		_, err := Dial(context.Background(), ts.URL)
		if err == nil {
			t.Fatal("Dial: want decode error for invalid card, got nil")
		}
	})
}

// TestRPCErrToGo table-tests every wire-code → sentinel mapping plus the
// unknown-code fallback. This is the single point where errors cross the wire
// back into errors.Is-able sentinels.
func TestRPCErrToGo(t *testing.T) {
	cases := []struct {
		name    string
		code    int
		want    error // sentinel that errors.Is must satisfy; nil = unknown
		message string
	}{
		{"task not found", codeTaskNotFound, ErrTaskNotFound, "no such task"},
		{"task not cancelable", codeTaskNotCancelable, ErrTaskNotCancelable, "terminal"},
		{"push not supported", codePushNotSupported, ErrPushNotSupported, "no push"},
		{"unsupported op", codeUnsupportedOp, ErrUnsupportedOp, "nope"},
		{"content type", codeContentType, ErrContentType, "bad mime"},
		{"invalid agent resp", codeInvalidAgentResp, ErrInvalidAgentResp, "garbled"},
		{"unknown code", codeInternalError, nil, "boom"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := rpcErrToGo(&rpcError{Code: tc.code, Message: tc.message}, "agent-x")
			if err == nil {
				t.Fatal("rpcErrToGo returned nil")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Errorf("error %v does not satisfy errors.Is(_, %v)", err, tc.want)
			}
			if !strings.Contains(err.Error(), "agent-x") {
				t.Errorf("error %q does not mention agent name", err)
			}
			if !strings.Contains(err.Error(), tc.message) {
				t.Errorf("error %q does not carry the wire message %q", err, tc.message)
			}
		})
	}
}

// TestClientGetCancel proves the low-level Client round-trips SendMessage,
// GetTask, and CancelTask against a loopback server, and that canceling an
// already-completed task surfaces ErrTaskNotCancelable across the wire.
func TestClientGetCancel(t *testing.T) {
	ts := httptest.NewServer(NewServer(a2atest.NewEchoAgent("echo", "echoes")))
	defer ts.Close()

	remote, err := Dial(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	c := remote.Client()

	task, err := c.SendMessage(context.Background(), Message{
		MessageID: "m1", Role: RoleUser, Parts: []Part{TextPart("hello")},
	}, nil)
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if task.Status.State != TaskStateCompleted {
		t.Fatalf("state = %s, want completed", task.Status.State)
	}

	got, err := c.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.ID != task.ID {
		t.Errorf("GetTask id = %q, want %q", got.ID, task.ID)
	}

	// Canceling a completed (terminal) task must map back to the sentinel.
	_, err = c.CancelTask(context.Background(), task.ID)
	if !errors.Is(err, ErrTaskNotCancelable) {
		t.Errorf("CancelTask error = %v, want errors.Is(_, ErrTaskNotCancelable)", err)
	}
}
