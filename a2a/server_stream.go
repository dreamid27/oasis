package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/nevindra/oasis/core"
)

// sseWriter writes JSON-RPC-enveloped StreamResponse frames over a single SSE
// connection. It reuses one bytes.Buffer and one json.Encoder for the lifetime
// of the connection — the allocation-bounded translation path required of the
// streaming hot path. Not safe for concurrent use; one goroutine owns the
// connection and drives all sends.
type sseWriter struct {
	w   http.ResponseWriter
	f   http.Flusher
	id  json.RawMessage // the request's JSON-RPC id, echoed byte-identical
	buf bytes.Buffer
	enc *json.Encoder
}

// newSSEWriter promotes w to an SSE stream. It returns ok=false when the
// writer cannot flush (SSE is impossible without per-frame flushing). The SSE
// headers are set before the first write — serveJSONRPC set Content-Type to
// application/json, but the body has not been flushed yet, so overriding it
// here is valid.
func newSSEWriter(w http.ResponseWriter, id json.RawMessage) (*sseWriter, bool) {
	f, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	s := &sseWriter{w: w, f: f, id: id}
	s.enc = json.NewEncoder(&s.buf)
	return s, true
}

// send writes one SSE frame: `data: {"jsonrpc":"2.0","id":<id>,"result":<sr>}`
// followed by a blank line. The envelope is assembled by hand around the
// encoder's output so there is no intermediate response struct to allocate per
// event — only the StreamResponse value itself crosses the boundary.
//
// A StreamResponse that cannot be encoded is dropped (the frame is skipped);
// the run still settles and the final status frame is emitted, so the consumer
// always sees a terminal event. json.Encoder appends a trailing newline to the
// encoded result, which we strip before closing the envelope brace.
func (s *sseWriter) send(sr StreamResponse) {
	s.buf.Reset()
	s.buf.WriteString(`data: {"jsonrpc":"2.0","id":`)
	if len(s.id) > 0 {
		s.buf.Write(s.id)
	} else {
		s.buf.WriteString("null")
	}
	s.buf.WriteString(`,"result":`)
	if err := s.enc.Encode(sr); err != nil {
		return // unencodable frame — skip it; the terminal frame still settles
	}
	// Encode wrote the result JSON plus a trailing '\n' into buf. Drop that
	// newline (Truncate keeps the buffer's backing capacity for reuse on the
	// next frame — no per-frame reallocation), then close the JSON-RPC envelope
	// and terminate the SSE frame with the mandatory blank line.
	if b := s.buf.Bytes(); len(b) > 0 && b[len(b)-1] == '\n' {
		s.buf.Truncate(len(b) - 1)
	}
	s.buf.WriteString("}\n\n")
	s.w.Write(s.buf.Bytes())
	s.f.Flush()
}

// handleMessageStream implements SendStreamingMessage. It runs the agent and
// translates core.StreamEvent values into A2A SSE frames in real time, then
// settles the task by reusing the same settle()/snapshot() machinery as the
// blocking SendMessage path — there is exactly one settlement code path.
//
// Resume over the streaming transport (a message carrying a TaskID) is rejected
// in v1: the simplest correct behavior, since silently starting a fresh task
// would lose the caller's resume intent. Callers resume via SendMessage.
func (s *Server) handleMessageStream(w http.ResponseWriter, r *http.Request, req rpcRequest) {
	var p sendParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		writeJSON(w, rpcResponse{JSONRPC: "2.0", ID: req.ID,
			Error: &rpcError{Code: codeInvalidParams, Message: "invalid params: " + err.Error()}})
		return
	}
	if len(p.Message.Parts) == 0 {
		writeJSON(w, rpcResponse{JSONRPC: "2.0", ID: req.ID,
			Error: &rpcError{Code: codeInvalidParams, Message: "invalid params: message has no parts"}})
		return
	}
	if p.Message.TaskID != "" {
		writeJSON(w, rpcResponse{JSONRPC: "2.0", ID: req.ID,
			Error: &rpcError{Code: codeUnsupportedOp,
				Message: "resume over SendStreamingMessage not supported; use SendMessage"}})
		return
	}

	// Errors above are normal JSON-RPC error responses (no SSE yet). From here
	// the writer becomes an SSE stream — failures are surfaced as status frames.
	sw, ok := newSSEWriter(w, req.ID)
	if !ok {
		writeJSON(w, rpcResponse{JSONRPC: "2.0", ID: req.ID,
			Error: &rpcError{Code: codeInternalError, Message: "streaming unsupported: response writer cannot flush"}})
		return
	}

	at := agentTaskFromMessage(p.Message)
	contextID := p.Message.ContextID
	if contextID == "" {
		contextID = newID() // mirror handleMessageSend: the server assigns a scope
		at.ThreadID = contextID
	}

	taskID := newID()
	entry := &TaskRecord{
		Task: Task{
			ID:        taskID,
			ContextID: contextID,
			Status:    TaskStatus{State: TaskStateSubmitted, Timestamp: nowRFC3339()},
			History:   []Message{p.Message},
		},
	}

	// runCtx derives from the request context so a dropped client cancels the
	// run; entry.cancel is published so a concurrent CancelTask can abort it,
	// mirroring runTask's cancel-handle discipline.
	//
	// context.AfterFunc wires Server.Close() into the streaming run: when
	// s.baseCtx is canceled (via Close()), the AfterFunc fires cancel() in a
	// new goroutine, aborting runCtx. Without this, only a client-drop or a
	// CancelTask would abort a streaming run; Close() would leave it running
	// until the HTTP request context expired naturally.
	runCtx, cancel := context.WithCancel(r.Context())
	stop := context.AfterFunc(s.baseCtx, cancel) // Close() also aborts streaming runs
	defer stop()
	entry.mu.Lock()
	entry.cancel = cancel
	entry.Task.Status = TaskStatus{State: TaskStateWorking, Timestamp: nowRFC3339()}
	entry.mu.Unlock()
	if err := s.store.Save(runCtx, entry); err != nil {
		// Persistence failed before the run started; report it as a final failed
		// status so the consumer still gets a terminal frame, then stop.
		cancel()
		sw.send(statusFrame(taskID, contextID, TaskStatus{
			State:     TaskStateFailed,
			Message:   agentMessage(taskID, contextID, "save task: "+err.Error()),
			Timestamp: nowRFC3339(),
		}, true))
		return
	}

	// Initial working frame: the consumer learns the task started before any
	// content arrives.
	sw.send(statusFrame(taskID, contextID, TaskStatus{State: TaskStateWorking, Timestamp: nowRFC3339()}, false))

	// The agent streams into ch; the goroutine reports its terminal (result,err)
	// on done. Both channels are buffered so neither the agent nor the launcher
	// blocks on a slow peer. Why 64: matches the framework's streaming buffer
	// convention (core streaming uses buffered channels of this magnitude).
	ch := make(chan core.StreamEvent, 64)
	type agentDone struct {
		res core.AgentResult
		err error
	}
	done := make(chan agentDone, 1)
	go func() {
		// Parity with runTask: a panicking agent becomes a FAILED task, not a
		// crashed process. PanicAgent never closes ch, so the recover here is the
		// only thing that unwedges the translation loop below (via done).
		defer func() {
			if v := recover(); v != nil {
				done <- agentDone{err: fmt.Errorf("agent panicked: %v", v)}
			}
		}()
		res, err := s.agent.Execute(runCtx, at, core.WithStream(ch))
		done <- agentDone{res: res, err: err}
	}()

	// artifactID is generated once so all append chunks land on one artifact.
	// Name "response" stays consistent with artifactsFromResult's settled
	// artifact (server_send.go).
	artifactID := newID()

	// Translate events until the agent reports done. We MUST NOT use a bare
	// `for ev := range ch`: a misbehaving agent (e.g. PanicAgent) can return
	// without closing ch, which would wedge a range forever. Selecting on done
	// guarantees termination.
	//
	// Closed-channel discipline: a receive from a closed channel is always
	// ready and yields the zero value, so once a well-behaved agent closes ch
	// the select would busy-spin on that case forever. The comma-ok form
	// detects the close and nils out ch — a receive on a nil channel blocks
	// forever, removing the case so the select waits only on done.
	var fin agentDone
	gotDone := false
	for !gotDone {
		select {
		case ev, ok := <-ch:
			if !ok {
				ch = nil // closed: stop selecting it, wait for done
				continue
			}
			s.translateEvent(sw, taskID, contextID, artifactID, ev)
		case fin = <-done:
			gotDone = true
		}
	}
	// Drain whatever the agent buffered before signaling done. Non-blocking:
	// stop at the first empty read. If ch was already closed (nil'd above) this
	// reads from a nil channel — never ready — and exits immediately.
drain:
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				break drain // closed mid-drain: nothing more to read
			}
			s.translateEvent(sw, taskID, contextID, artifactID, ev)
		default:
			break drain
		}
	}

	// Settle through the shared path (handles completed/failed/canceled/
	// input-required, persists, delivers push). cancel() after settle so
	// settle() observes external cancellation (runCtx.Err()) rather than this
	// local cleanup — identical ordering to runTask.
	s.settle(runCtx, entry, fin.res, fin.err)
	cancel()

	// Replay the settled artifacts as terminal chunks, then the final status.
	snap := s.snapshot(entry)
	for _, art := range snap.Artifacts {
		sw.send(artifactFrame(taskID, contextID, art, false, true))
	}
	sw.send(statusFrame(taskID, contextID, snap.Status, true))
}

// handleResubscribe implements SubscribeToTask. v1 uses poll-based replay
// semantics: events are not buffered server-side, so a resubscribe replays the
// task's current artifacts and status rather than the exact event sequence a
// live subscriber would have seen.
//
// Documented polling limitations:
//   - A late-joining subscriber to a still-running task polls until the task
//     pauses or terminates, then receives the settled artifacts and final
//     status. Intermediate events are not replayed.
//   - Each poll iteration re-fetches the record from the store (IMPORTANT-3):
//     this is required for correctness with custom persistent TaskStore
//     implementations that return a new *TaskRecord on each Get (rather than
//     the same pointer mutated in place as memoryStore does). Snapshotting
//     the record pointer once before the loop would never observe progress
//     through such a store. On Get error mid-loop (e.g. eviction), the poll
//     stops and returns without a final frame.
func (s *Server) handleResubscribe(w http.ResponseWriter, r *http.Request, req rpcRequest) {
	var params taskIDParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeJSON(w, rpcResponse{JSONRPC: "2.0", ID: req.ID,
			Error: &rpcError{Code: codeInvalidParams, Message: "invalid params: " + err.Error()}})
		return
	}
	if params.ID == "" {
		writeJSON(w, rpcResponse{JSONRPC: "2.0", ID: req.ID,
			Error: &rpcError{Code: codeInvalidParams, Message: "invalid params: id required"}})
		return
	}
	// Initial Get: establishes the task exists before upgrading to SSE.
	// Subsequent Gets happen inside the poll loop.
	if _, err := s.store.Get(r.Context(), params.ID); err != nil {
		writeJSON(w, rpcResponse{JSONRPC: "2.0", ID: req.ID,
			Error: &rpcError{Code: codeTaskNotFound, Message: err.Error()}})
		return
	}

	sw, ok := newSSEWriter(w, req.ID)
	if !ok {
		writeJSON(w, rpcResponse{JSONRPC: "2.0", ID: req.ID,
			Error: &rpcError{Code: codeInternalError, Message: "streaming unsupported: response writer cannot flush"}})
		return
	}

	// Why 200ms: a resubscribe to a live task polls for the task to reach a
	// pause/terminal state. 200ms balances latency against busy-looping; the
	// loop exits immediately once the state is reportable.
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		// Re-Get the record on every iteration so custom persistent TaskStore
		// implementations (which may return a fresh *TaskRecord on each call)
		// can observe progress. memoryStore returns the same pointer so this is
		// also correct there, just a map lookup per tick.
		entry, err := s.store.Get(r.Context(), params.ID)
		if err != nil {
			// Task was evicted or otherwise lost mid-poll; stop silently.
			return
		}
		snap := s.snapshot(entry)
		state := snap.Status.State
		// Report once the interaction pauses (input-required / auth-required) or
		// terminates — the same boundary at which a live stream ends.
		if state.Terminal() || state == TaskStateInputRequired || state == TaskStateAuthRequired {
			for _, art := range snap.Artifacts {
				sw.send(artifactFrame(params.ID, snap.ContextID, art, false, true))
			}
			sw.send(statusFrame(params.ID, snap.ContextID, snap.Status, state.Terminal()))
			return
		}
		select {
		case <-ticker.C:
		case <-r.Context().Done():
			return // client gave up; stop polling
		case <-s.baseCtx.Done():
			return // Server.Close() called; abort the poll loop
		}
	}
}

// translateEvent maps one core.StreamEvent onto an SSE frame. Text deltas
// become append chunks on the single response artifact; errors are intentionally
// not surfaced mid-stream (the final settled status carries the failure). All
// other event types are intentionally dropped — A2A v1 streams text and
// lifecycle, not the framework's full internal event taxonomy.
func (s *Server) translateEvent(sw *sseWriter, taskID, contextID, artifactID string, ev core.StreamEvent) {
	switch ev.Type {
	case core.EventTextDelta:
		if ev.Content == "" {
			return
		}
		art := Artifact{ArtifactID: artifactID, Name: "response", Parts: []Part{{Text: ev.Content}}}
		sw.send(artifactFrame(taskID, contextID, art, true, false))
	}
}

// statusFrame builds a StreamResponse carrying a TaskStatusUpdateEvent.
func statusFrame(taskID, contextID string, status TaskStatus, final bool) StreamResponse {
	return StreamResponse{StatusUpdate: &TaskStatusUpdateEvent{
		TaskID:    taskID,
		ContextID: contextID,
		Status:    status,
		Final:     final,
	}}
}

// artifactFrame builds a StreamResponse carrying a TaskArtifactUpdateEvent.
func artifactFrame(taskID, contextID string, art Artifact, appendChunk, lastChunk bool) StreamResponse {
	return StreamResponse{ArtifactUpdate: &TaskArtifactUpdateEvent{
		TaskID:    taskID,
		ContextID: contextID,
		Artifact:  art,
		Append:    appendChunk,
		LastChunk: lastChunk,
	}}
}
