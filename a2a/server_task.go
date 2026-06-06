package a2a

import (
	"context"
	"encoding/json"
)

// handleTasksGet implements the GetTask method. It returns a bare Task (not the
// sendResult oneof that SendMessage returns — GetTask is a pure read, not a
// send-and-wait). No lock is held across the store.Get call; the entry lock is
// acquired only for the snapshot to keep critical sections narrow.
func (s *Server) handleTasksGet(ctx context.Context, p json.RawMessage) (any, *rpcError) {
	var params taskIDParams
	if err := json.Unmarshal(p, &params); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "invalid params: " + err.Error()}
	}
	if params.ID == "" {
		return nil, &rpcError{Code: codeInvalidParams, Message: "invalid params: id required"}
	}
	entry, err := s.store.Get(ctx, params.ID)
	if err != nil {
		return nil, &rpcError{Code: codeTaskNotFound, Message: err.Error()}
	}
	// Lock only long enough to copy the value; no I/O inside the critical section.
	entry.mu.Lock()
	defer entry.mu.Unlock()
	return entry.Task, nil
}

// handleTasksCancel implements the CancelTask method. The cancel path has two
// branches:
//
//  1. Working task — entry.cancel() fires the context and runTask's settle
//     observes ctx.Err() == context.Canceled, transitioning the task to
//     TaskStateCanceled. The response returns the pre-cancel snapshot; the
//     caller can follow up with GetTask to observe the settled state.
//
//  2. Input-required task — there is no running goroutine to interrupt. We
//     settle the task directly here, drop the single-use resumable, and persist
//     via store.Save outside the lock so custom stores stay consistent.
//
// Terminal tasks reject cancellation with TaskNotCancelable; unknown IDs return
// TaskNotFound. Both are protocol-level failures, not RPC infrastructure errors.
func (s *Server) handleTasksCancel(ctx context.Context, p json.RawMessage) (any, *rpcError) {
	var params taskIDParams
	if err := json.Unmarshal(p, &params); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "invalid params: " + err.Error()}
	}
	if params.ID == "" {
		return nil, &rpcError{Code: codeInvalidParams, Message: "invalid params: id required"}
	}
	entry, err := s.store.Get(ctx, params.ID)
	if err != nil {
		return nil, &rpcError{Code: codeTaskNotFound, Message: err.Error()}
	}

	entry.mu.Lock()
	if entry.Task.Status.State.Terminal() {
		entry.mu.Unlock()
		return nil, &rpcError{Code: codeTaskNotCancelable, Message: taskError(ErrTaskNotCancelable, params.ID).Error()}
	}

	if entry.cancel != nil {
		// Working path: signal the in-flight runTask to abort. runTask's settle
		// call will observe runCtx.Err() != nil and write TaskStateCanceled.
		entry.cancel()
	}

	// inputRequired tracks whether we took the direct-settle path. The working
	// path's settle() (called by runTask after cancel()) handles its own
	// persistence; the input-required path has no goroutine to do that, so we
	// must save explicitly after releasing the lock.
	inputRequired := entry.resume != nil
	if inputRequired {
		// No running goroutine to interrupt; settle directly and drop the
		// single-use resumable to prevent a stale resume after cancellation.
		entry.resume = nil
		entry.Task.Status = TaskStatus{State: TaskStateCanceled, Timestamp: nowRFC3339()}
	}

	task := entry.Task
	entry.mu.Unlock()

	if inputRequired {
		// Why: persist outside the lock so custom store implementations can do
		// I/O without holding entry.mu; settle() in server_send.go follows the
		// same discipline. Log-and-continue on failure: in-memory state is
		// correct, and a persistence failure is non-fatal for the response.
		_ = s.store.Save(ctx, entry)
	}

	return task, nil
}
