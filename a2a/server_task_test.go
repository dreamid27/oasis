package a2a

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/nevindra/oasis/a2a/a2atest"
)

// TestTasksGetAndCancel exercises GetTask and CancelTask against the full
// routing stack. Scenarios:
//
//  1. GetTask on a settled (completed) task → bare Task, state completed.
//  2. GetTask on an unknown ID → TaskNotFound.
//  3. CancelTask on a terminal task → TaskNotCancelable.
//  4. CancelTask on an input-required task → TaskStateCanceled returned; a
//     subsequent resume attempt fails with codeUnsupportedOp.
func TestTasksGetAndCancel(t *testing.T) {
	ts := httptest.NewServer(NewServer(a2atest.NewEchoAgent("echo", "echoes")))
	defer ts.Close()

	task := sendTask(t, rpcCall(t, ts.URL, methodSendMessage, sendParams{
		Message: Message{MessageID: "m1", Role: RoleUser, Parts: []Part{TextPart("hi")}},
	}))

	// GetTask returns the settled task — a bare Task, NOT the sendResult oneof.
	resp := rpcCall(t, ts.URL, methodGetTask, taskIDParams{ID: task.ID})
	var got Task
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatalf("unmarshal GetTask result: %v", err)
	}
	if got.ID != task.ID || got.Status.State != TaskStateCompleted {
		t.Errorf("GetTask = %+v", got)
	}

	// Unknown ID → TaskNotFound error code.
	resp = rpcCall(t, ts.URL, methodGetTask, taskIDParams{ID: "missing"})
	if resp.Error == nil || resp.Error.Code != codeTaskNotFound {
		t.Errorf("want %d, got %+v", codeTaskNotFound, resp.Error)
	}

	// Canceling a terminal task → TaskNotCancelable error code.
	resp = rpcCall(t, ts.URL, methodCancelTask, taskIDParams{ID: task.ID})
	if resp.Error == nil || resp.Error.Code != codeTaskNotCancelable {
		t.Errorf("want %d, got %+v", codeTaskNotCancelable, resp.Error)
	}
}

// TestCancelInputRequiredTask verifies the direct-settle path: a suspended
// (input-required) task has no running goroutine, so CancelTask settles it
// synchronously and returns the canceled task. A follow-up resume attempt
// (SendMessage with the same TaskID) must fail with codeUnsupportedOp because
// the task is now terminal and the resumable has been dropped.
func TestCancelInputRequiredTask(t *testing.T) {
	ts := httptest.NewServer(NewServer(a2atest.NewSuspendingAgent("hitl", "asks first")))
	defer ts.Close()

	// First send: agent suspends immediately → TaskStateInputRequired.
	task := sendTask(t, rpcCall(t, ts.URL, methodSendMessage, sendParams{
		Message: Message{MessageID: "m1", Role: RoleUser, Parts: []Part{TextPart("start")}},
	}))
	if task.Status.State != TaskStateInputRequired {
		t.Fatalf("pre-cancel state = %s, want input-required", task.Status.State)
	}

	// CancelTask on the suspended task → synchronous cancel, returns the task.
	resp := rpcCall(t, ts.URL, methodCancelTask, taskIDParams{ID: task.ID})
	if resp.Error != nil {
		t.Fatalf("CancelTask: unexpected error %+v", resp.Error)
	}
	var canceled Task
	if err := json.Unmarshal(resp.Result, &canceled); err != nil {
		t.Fatalf("unmarshal CancelTask result: %v", err)
	}
	if canceled.Status.State != TaskStateCanceled {
		t.Errorf("after cancel: state = %s, want canceled", canceled.Status.State)
	}

	// Follow-up resume (SendMessage with TaskID) must fail — the task is now
	// terminal and the single-use resumable has been dropped.
	resumeResp := rpcCall(t, ts.URL, methodSendMessage, sendParams{
		Message: Message{MessageID: "m2", TaskID: task.ID, Role: RoleUser, Parts: []Part{TextPart("too late")}},
	})
	if resumeResp.Error == nil || resumeResp.Error.Code != codeUnsupportedOp {
		t.Errorf("resume of canceled task: want %d, got %+v", codeUnsupportedOp, resumeResp.Error)
	}
}
