package a2a

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/nevindra/oasis/core"
)

// pollInterval is how often a blocking Execute re-fetches a still-running remote
// task. Why 500ms: small enough that interactive turns feel responsive, large
// enough not to hammer the server. The poll loop is ctx-aware, so cancellation
// is observed within one interval at worst.
const pollInterval = 500 * time.Millisecond

// interrupted reports whether s is an interrupted (suspend) state — one that
// pauses the task and requires the caller to resume it. Both InputRequired and
// AuthRequired are interrupted: the task is alive but blocked pending a
// response from the caller. The poll loop must stop on these states just as it
// stops on terminal states; otherwise a task that transitions to AuthRequired
// would never exit the loop (it is not terminal, but polling won't advance it).
func interrupted(s TaskState) bool {
	return s == TaskStateInputRequired || s == TaskStateAuthRequired
}

// RemoteAgent is a core.Agent backed by a remote A2A server: every Execute call
// is sent over the wire and the remote task's outcome is mapped onto a
// core.AgentResult. It makes a remote agent a first-class, drop-in member of an
// Oasis Network or LLMAgent toolset.
//
// Safe for concurrent use by multiple goroutines. The pending map records, per
// ThreadID, the input-required task ID so the next Execute on the same thread
// resumes the remote task (mirroring server-side suspend semantics); it is
// guarded by mu. Construct via Dial.
//
// Concurrency caveat: concurrent Execute calls sharing the SAME ThreadID race
// on the pending resume ID — callers should serialize turns per thread to avoid
// one call consuming the resume ID intended for another.
type RemoteAgent struct {
	client *Client

	mu      sync.Mutex
	pending map[string]string // ThreadID → input-required task ID awaiting resume
}

// Name returns the remote agent's card name sanitized to a tool-safe identifier:
// lowercased, with every character outside [a-z0-9] replaced by '_'. Network
// builds tool-call names from this, and spaces or punctuation would break
// tool-call syntax — so "research helper" becomes "research_helper".
func (a *RemoteAgent) Name() string { return sanitizeName(a.client.card.Name) }

// Description returns the remote agent's card description, used by Network to
// generate the routing-LLM tool definition.
func (a *RemoteAgent) Description() string { return a.client.card.Description }

// Client returns the underlying low-level protocol Client for callers that need
// direct GetTask/CancelTask access. The Client is safe for concurrent use.
func (a *RemoteAgent) Client() *Client { return a.client }

// sanitizeName lowercases s and replaces any non-[a-z0-9] rune with '_'. An
// all-invalid or empty name yields a non-empty identifier ("_") so downstream
// tool registration never sees an empty name.
func sanitizeName(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}

// Execute runs the task on the remote agent and maps the result onto a
// core.AgentResult.
//
// Streaming: when opts include WithStream and this is a fresh turn (no pending
// resume for the thread), text deltas stream live via SendStreamingMessage. The
// stream channel is ALWAYS closed exactly once before Execute returns, on every
// path including errors.
//
// Resume: when a prior turn on the same ThreadID left the remote task awaiting
// input, this Execute resumes it by carrying the task ID. Resume over the
// streaming transport is unsupported server-side, so a streaming resume falls
// back to a blocking SendMessage (deltas are not produced for the resume turn);
// the stream channel is still closed.
//
// Blocking (the default): SendMessage, then poll GetTask every pollInterval
// until the task reaches a terminal or input-required state. The poll loop is
// ctx-aware.
func (a *RemoteAgent) Execute(ctx context.Context, task core.AgentTask, opts ...core.RunOption) (core.AgentResult, error) {
	cfg := core.ApplyRunOptions(opts...)
	msg := messageFromTask(task)

	// Resume: if a previous turn on this thread suspended, carry the pending
	// task ID so the server resumes it instead of starting a fresh task.
	if task.ThreadID != "" {
		a.mu.Lock()
		if id, ok := a.pending[task.ThreadID]; ok {
			msg.TaskID = id
			delete(a.pending, task.ThreadID)
		}
		a.mu.Unlock()
	}

	// Streaming, fresh turn: deltas stream live. executeStream owns closing ch.
	if cfg.Stream != nil && msg.TaskID == "" {
		return a.executeStream(ctx, task.ThreadID, msg, cfg.Stream)
	}

	// Streaming resume: the server rejects resume-over-stream, so fall back to a
	// blocking send. The agent side owns closing the stream channel (mirrors how
	// core agents close cfg.Stream), so close it here even though no deltas flow.
	if cfg.Stream != nil {
		defer close(cfg.Stream)
	}

	t, err := a.client.SendMessage(ctx, msg, nil)
	if err != nil {
		return core.AgentResult{FinishReason: core.FinishError}, err
	}

	// Poll until the task settles or pauses for input/auth. Already-terminal /
	// interrupted tasks skip the loop entirely. Interrupted states (InputRequired,
	// AuthRequired) pause the poll — the caller is responsible for resuming; the
	// loop must exit on them just as it exits on terminal states.
	for !t.Status.State.Terminal() && !interrupted(t.Status.State) {
		select {
		case <-ctx.Done():
			return core.AgentResult{FinishReason: core.FinishError}, ctx.Err()
		case <-time.After(pollInterval):
		}
		t, err = a.client.GetTask(ctx, t.ID)
		if err != nil {
			return core.AgentResult{FinishReason: core.FinishError}, err
		}
	}
	return a.resultFromTask(task.ThreadID, t)
}

// executeStream consumes the SSE stream, forwarding text deltas to ch and
// assembling the terminal Task, then maps it. ch is closed exactly once via
// defer, on every path. final is built up from the stream events:
//   - artifact append chunks → forwarded as EventTextDelta
//   - artifact last-chunk frames → collected into final.Artifacts
//   - status updates → recorded into final's id/context/status
//   - a whole Task frame → replaces final wholesale
//
// Why: LastChunk replay is an Oasis server behavior, not a spec guarantee.
// A conformant non-Oasis server may stream append-delta chunks followed by a
// final completed-status frame with no LastChunk artifact replay. In that case
// final.Artifacts is empty and resultFromTask produces an empty Output. To
// handle this cross-vendor pattern, we accumulate all forwarded delta text and
// use it as a fallback when the assembled artifacts produce no text output.
func (a *RemoteAgent) executeStream(ctx context.Context, threadID string, msg Message, ch chan<- core.StreamEvent) (core.AgentResult, error) {
	defer close(ch)

	var final Task
	var deltaText strings.Builder // accumulates forwarded delta text for the fallback path
	err := a.client.Stream(ctx, msg, func(sr StreamResponse) bool {
		switch {
		case sr.Task != nil:
			final = *sr.Task
		case sr.StatusUpdate != nil:
			final.ID = sr.StatusUpdate.TaskID
			final.ContextID = sr.StatusUpdate.ContextID
			final.Status = sr.StatusUpdate.Status
		case sr.ArtifactUpdate != nil:
			au := sr.ArtifactUpdate
			if final.ID == "" {
				final.ID = au.TaskID
			}
			if au.Append {
				// Live delta: forward only the text parts as the LLM-visible stream.
				for _, p := range au.Artifact.Parts {
					if p.Text != "" {
						deltaText.WriteString(p.Text)
						ch <- core.StreamEvent{Type: core.EventTextDelta, Content: p.Text}
					}
				}
				return true
			}
			if au.LastChunk {
				final.Artifacts = append(final.Artifacts, au.Artifact)
			}
		}
		return true
	})
	if err != nil {
		return core.AgentResult{FinishReason: core.FinishError}, err
	}
	return a.resultFromTaskWithFallback(threadID, final, deltaText.String())
}

// resultFromTask maps a settled (or paused) remote Task onto a core.AgentResult.
// It is a thin wrapper over resultFromTaskWithFallback with an empty delta fallback,
// used by the blocking (non-streaming) Execute path where no delta text exists.
func (a *RemoteAgent) resultFromTask(threadID string, t Task) (core.AgentResult, error) {
	return a.resultFromTaskWithFallback(threadID, t, "")
}

// resultFromTaskWithFallback maps a settled (or paused) remote Task onto a
// core.AgentResult. deltaFallback is the concatenated delta text forwarded
// during a streaming run; it is used as the Output when the task completed but
// no text was found in its Artifacts (cross-vendor: LastChunk replay is an
// Oasis-specific behavior, not required by the spec).
//
//   - Completed → text parts concatenated into Output, the first data part into
//     Object, file parts into Files; FinishStop.
//   - InputRequired → the task ID is recorded under threadID for the next
//     Execute to resume (only when threadID != ""), SuspendPayload taken from
//     the status message text; FinishSuspended, nil error.
//   - Canceled → FinishHalted, nil error.
//   - Failed / rejected / anything else → FinishError plus a taskError wrapping
//     ErrInvalidAgentResp, carrying the task ID, state, and status message so the
//     failure is reconstructable from the error alone.
func (a *RemoteAgent) resultFromTaskWithFallback(threadID string, t Task, deltaFallback string) (core.AgentResult, error) {
	switch t.Status.State {
	case TaskStateCompleted:
		res := core.AgentResult{FinishReason: core.FinishStop}
		var sb strings.Builder
		for _, art := range t.Artifacts {
			for _, p := range art.Parts {
				switch {
				case p.Text != "":
					sb.WriteString(p.Text)
				case len(p.Data) > 0:
					if res.Object == nil {
						res.Object = p.Data // first data part is the structured output
					}
				case len(p.Raw) > 0:
					res.Files = append(res.Files, core.Attachment{MimeType: p.MediaType, Data: p.Raw})
				case p.URL != "":
					res.Files = append(res.Files, core.Attachment{MimeType: p.MediaType, URL: p.URL})
				}
			}
		}
		res.Output = sb.String()
		// Why: cross-vendor fallback — a non-Oasis server may stream append-delta
		// chunks without a LastChunk replay, leaving Artifacts empty. In that case
		// use the accumulated delta text so callers get a non-empty Output.
		if res.Output == "" && deltaFallback != "" {
			res.Output = deltaFallback
		}
		return res, nil

	case TaskStateInputRequired, TaskStateAuthRequired:
		if threadID != "" {
			a.mu.Lock()
			a.pending[threadID] = t.ID
			a.mu.Unlock()
		}
		res := core.AgentResult{FinishReason: core.FinishSuspended}
		if t.Status.Message != nil {
			res.SuspendPayload = []byte(firstText(t.Status.Message.Parts))
			res.Output = firstText(t.Status.Message.Parts)
		}
		return res, nil

	case TaskStateCanceled:
		return core.AgentResult{FinishReason: core.FinishHalted}, nil

	default: // failed, rejected, unspecified
		msg := ""
		if t.Status.Message != nil {
			msg = firstText(t.Status.Message.Parts)
		}
		return core.AgentResult{FinishReason: core.FinishError},
			taskError(ErrInvalidAgentResp, t.ID+" state="+string(t.Status.State)+" msg="+msg)
	}
}

// firstText returns the Text of the first text-bearing part, or "".
func firstText(parts []Part) string {
	for _, p := range parts {
		if p.Text != "" {
			return p.Text
		}
	}
	return ""
}

// messageFromTask maps a core.AgentTask onto an outbound A2A user Message:
// Input becomes a text part, Attachments become file parts (inline Raw bytes or
// a URL reference plus MediaType), ContextID is the thread, and a fresh
// MessageID is minted. The role is always RoleUser (client→agent).
func messageFromTask(task core.AgentTask) Message {
	parts := make([]Part, 0, 1+len(task.Attachments))
	if task.Input != "" {
		parts = append(parts, TextPart(task.Input))
	}
	for _, at := range task.Attachments {
		p := Part{MediaType: at.MimeType}
		if len(at.Data) > 0 {
			p.Raw = at.Data
		} else {
			p.URL = at.URL
		}
		parts = append(parts, p)
	}
	return Message{
		MessageID: core.NewID(),
		ContextID: task.ThreadID,
		Role:      RoleUser,
		Parts:     parts,
	}
}
