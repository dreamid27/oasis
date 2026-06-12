# A2A (Agent2Agent)

## TL;DR

A2A is the Linux Foundation's open standard (v1.0, Apache 2.0) for agent-to-agent
interoperability: agents built on different frameworks delegate work to each other
through stateful tasks over HTTP. It is to agent-to-agent what MCP is to
agent-to-tool.

`a2a.NewServer` exposes any `core.Agent` as an A2A server. `a2a.Dial` consumes
a remote A2A server and returns a `*RemoteAgent` that satisfies `core.Agent` — a
remote agent becomes a first-class framework value that drops into a Network, a
Workflow, or a tool list unchanged. `a2a.AsTool` wraps a `*RemoteAgent` for
LLM-driven delegation without a Network.

---

## When to reach for A2A

**Use A2A when:**
- You need to interoperate with agents built in other frameworks (Google ADK,
  Semantic Kernel, any A2A-compliant server).
- You want to expose an Oasis agent to external consumers across organizational
  or trust boundaries.
- You need long-running async tasks with webhook delivery (hours-long jobs where
  polling is impractical).

**Use [Network](../network/index.md) instead** for in-process multi-agent
coordination within Oasis. Network requires no HTTP, no serialization, and no
task store — it is strictly faster and simpler when all agents are in the same
process. A2A adds the wire protocol tax for the interoperability benefit.

| | Network | A2A |
|---|---|---|
| Agents in same process | yes | no (wire) |
| Cross-framework/org | no | yes |
| Protocol overhead | none | JSON-RPC + HTTP |
| Suspend/resume | yes | yes (over the wire) |
| Discovery | none | agent card at `/.well-known/agent-card.json` |

---

## Two directions, one import

### Server — expose an agent

```go
srv := a2a.NewServer(agent, a2a.WithURL("https://agents.example.com"))
log.Fatal(http.ListenAndServe(":8080", srv))
```

### Client — consume a remote agent

```go
remote, err := a2a.Dial(ctx, "https://agents.example.com")
result, err := remote.Execute(ctx, core.AgentTask{Input: "summarize this"})
```

---

## Semantic mapping

The protocol maps cleanly onto Oasis concepts:

| A2A concept | Oasis concept |
|---|---|
| `SendMessage` | `agent.Execute(ctx, task)` |
| `SendStreamingMessage` SSE events | `core.WithStream(ch)` → per-event translation |
| Task `TASK_STATE_WORKING` | run in flight |
| Task `TASK_STATE_COMPLETED` + Artifacts | `AgentResult.Output` / `.Files` / `.Object` |
| Task `TASK_STATE_INPUT_REQUIRED` | `FinishSuspended` + `SuspendPayload` |
| Task `TASK_STATE_FAILED` | `Execute` error → actionable message, never a dropped connection |
| `CancelTask` | context cancellation |
| `contextId` | `AgentTask.ThreadID` — conversation memory works across A2A turns |
| Agent card | `agent.Name()` + `agent.Description()` + server options |

---

## Suspend and resume — input-required round-trips

Oasis suspend/resume translates to A2A's `TASK_STATE_INPUT_REQUIRED` state in
both directions.

### Server side (exposing an Oasis agent)

When an agent's `Execute` returns `FinishSuspended`, the server transitions the
task to `TASK_STATE_INPUT_REQUIRED` and includes the suspend question in the
task status message. The task ID stays live. A follow-up `SendMessage` carrying
that task ID resumes the suspended run.

```
Client                          Server (Oasis agent)
  |                                   |
  |--- SendMessage("Q1") -----------> |
  |                              agent suspends (needs input)
  |<-- Task(INPUT_REQUIRED, "Q2?") -- |
  |                                   |
  |--- SendMessage(taskId, "A2") ---> |
  |                              agent resumes and completes
  |<-- Task(COMPLETED, artifacts) --- |
```

Re-suspension (multi-round HITL) works: each follow-up that causes another
suspend re-arms the task for another resume.

### Client side (consuming a remote agent)

When a remote task transitions to `TASK_STATE_INPUT_REQUIRED`, `RemoteAgent.Execute`
returns `AgentResult{FinishReason: FinishSuspended}` with the suspend question
in `SuspendPayload`. The next `Execute` on the same `ThreadID` automatically
carries the pending task ID so the remote server resumes instead of starting a
new task.

`TASK_STATE_AUTH_REQUIRED` is treated as a suspend state on the client side —
the same resume-by-ThreadID path applies.

---

## Auth boundary

The A2A package owns the **protocol**, not the **policy**.

- **Server**: `WithSecurityScheme` declares accepted auth on the agent card.
  Actual verification is your HTTP middleware wrapped around the `*Server`
  handler — Oasis does not build an auth framework.
- **Client**: `WithBearerToken` / `WithHeader` / `WithHTTPClient` (custom
  transport for OAuth, mTLS) pass credentials on every request. The card's
  declared schemes tell callers what is expected.

---

## Wire format

The package implements the JSON binding of the A2A v1.0 specification. Each
`*Server` mount speaks both transports:

- **JSON-RPC 2.0** — PascalCase method names per v1.0 §5.3
  (`SendMessage`, `GetTask`, `CancelTask`, `SendStreamingMessage`,
  `SubscribeToTask`, plus the four `*TaskPushNotificationConfig` methods).
- **REST (HTTP+JSON)** — four routes per v1.0 §11.3:
  `POST /message:send`, `POST /message:stream`, `GET /tasks/{id}`,
  `POST /tasks/{id}:cancel`.
- **SSE streaming** — JSON-RPC-enveloped frames for `SendStreamingMessage`
  and `SubscribeToTask`.
- **Agent card** at `GET /.well-known/agent-card.json`.

Field names are camelCase; enum values are `SCREAMING_SNAKE_CASE` (ProtoJSON
convention per A2A ADR-001).

---

## Limitations

- **Resume is process-bound.** A suspended task cannot be resumed after a
  process restart even with a persistent `TaskStore`. The resume closure is
  held in memory only. Persistent stores preserve task visibility but not
  resumability.
- **Resume over `SendStreamingMessage` is rejected.** Use `SendMessage` for
  resume turns. The streaming transport starts fresh tasks only.
- **`SubscribeToTask` is poll-based.** The server re-fetches task state every
  200 ms; there is no server-side event buffer. A resubscription replays the
  final state, not the event history a live subscriber would have seen.
- **Unmapped stream event types are dropped.** Only `core.EventTextDelta` maps
  to an A2A artifact chunk; all other `core.StreamEvent` types are silently
  skipped.
- **One push config per task.** The spec allows multiple; this implementation
  supports one. Revisit post-v1 if multi-config is needed.
- **Push delivery is best-effort.** Missed webhooks are logged but not retried.
  The authoritative task state is always available via `GetTask`.
- **`ListTasks` and `GetExtendedAgentCard` return `unsupported`.** Deliberate
  YAGNI — not enough demand to justify the store query surface pre-v1.
