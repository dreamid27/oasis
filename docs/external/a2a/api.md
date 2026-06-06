# A2A API

Import path: `github.com/nevindra/oasis/a2a`

Test doubles: `github.com/nevindra/oasis/a2a/a2atest`

---

## Server side

### `NewServer`

```go
func NewServer(agent core.Agent, opts ...ServerOption) *Server
```

Wraps `agent` as an A2A server. The zero-config default serves JSON-RPC 2.0,
SSE, and REST with a bounded in-memory task store (capacity 1024).

Long-running servers should `defer srv.Close()` to cancel any background task
runs started by non-blocking push requests.

Thread-safe for concurrent HTTP requests after construction.

---

### `*Server`

```go
type Server struct { /* unexported */ }
```

Implements `http.Handler`. The application owns the listener, TLS termination,
and auth middleware.

#### `(*Server).ServeHTTP`

```go
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request)
```

Routes A2A traffic. Dispatch order:

1. `GET /.well-known/agent-card.json` — agent card (discovery).
2. REST routes: `POST /message:send`, `POST /message:stream`,
   `GET /tasks/{id}`, `POST /tasks/{id}:cancel`.
3. Any other `POST` falls through to the JSON-RPC dispatcher (`SendMessage`,
   `GetTask`, `CancelTask`, `SendStreamingMessage`, `SubscribeToTask`, and the
   four `*TaskPushNotificationConfig` methods).

Wrong-method requests on known REST paths return `405` before reaching the
JSON-RPC fallback.

#### `(*Server).Card`

```go
func (s *Server) Card() AgentCard
```

Returns the agent card this server publishes. Useful for inspection and testing.

#### `(*Server).Close`

```go
func (s *Server) Close()
```

Cancels all background task runs (non-blocking push requests). In-flight
blocking requests are unaffected — they ride their own HTTP request contexts.
Safe to call multiple times.

---

### `ServerOption`

```go
type ServerOption func(*serverOptions)
```

Functional option for `NewServer`. The following built-in options are available:

#### `WithURL`

```go
func WithURL(u string) ServerOption
```

Sets the public endpoint URL advertised on the agent card. The card lists both
`JSONRPC` and `HTTP+JSON` interfaces at this URL, since one `Server` mount
speaks both. Omitting `WithURL` produces a card with no `supportedInterfaces`
field; `Dial` falls back to `baseURL` as the endpoint in that case.

#### `WithVersion`

```go
func WithVersion(v string) ServerOption
```

Sets the agent version advertised on the card.

#### `WithSkill`

```go
func WithSkill(s AgentSkill) ServerOption
```

Appends one skill to the agent card. Call repeatedly to add multiple skills.

#### `WithSecurityScheme`

```go
func WithSecurityScheme(name string, s SecurityScheme) ServerOption
```

Declares an accepted auth scheme on the agent card. Verification is the
application's HTTP middleware; this option only populates the card's
`securitySchemes` field so clients know what credentials to send.

#### `WithCard`

```go
func WithCard(c AgentCard) ServerOption
```

Replaces the generated agent card entirely. All other card-related options
(`WithURL`, `WithSkill`, etc.) are ignored when this is set. Power-user
escape hatch.

#### `WithTaskStore`

```go
func WithTaskStore(s TaskStore) ServerOption
```

Replaces the bounded in-memory default with a custom `TaskStore`
implementation. Use this for persistent task visibility across restarts.
The store's methods operate on `*TaskRecord` (see [Task store](#task-store)),
so an implementation in any package can satisfy the interface. Note: suspended
tasks cannot be resumed after a process restart regardless of the store — the
resume closure is process-bound.

#### `WithPushNotifications`

```go
func WithPushNotifications() ServerOption
```

Enables webhook delivery of task updates. Without this option, push config
methods return `ErrPushNotSupported`. When enabled, clients can register a
webhook URL via `CreateTaskPushNotificationConfig`; the server POSTs
`StreamResponse` payloads to that URL when the task settles.

#### `WithTaskCapacity`

```go
func WithTaskCapacity(n int) ServerOption
```

Bounds the in-memory task store to `n` tasks (default 1024). Live tasks
(working or input-required) are never evicted; terminal tasks evict
oldest-first once the cap is exceeded. Ignored when `WithTaskStore` is set.

---

## Client side

### `Dial`

```go
func Dial(ctx context.Context, baseURL string, opts ...DialOption) (*RemoteAgent, error)
```

Fetches the agent card from `baseURL + "/.well-known/agent-card.json"`,
resolves the JSON-RPC endpoint, and returns a ready `*RemoteAgent`.

The JSON-RPC endpoint is taken from the card's first `JSONRPC`
`AgentInterface`; when the card advertises no interfaces (e.g. a server
mounted without `WithURL`), `baseURL` itself is used as the endpoint.

Performs one blocking HTTP GET; honors `ctx` for cancellation. Every error
carries `baseURL` and the operation so failures are reconstructable from logs.

The returned `*RemoteAgent` is safe for concurrent use.

---

### `DialOption`

```go
type DialOption func(*dialOptions)
```

Functional option for `Dial`. The following built-in options are available:

#### `WithBearerToken`

```go
func WithBearerToken(tok string) DialOption
```

Sets `Authorization: Bearer <tok>` on every request the `Client` makes,
including the initial card fetch. Convenience for the common HTTP bearer auth
scheme; use `WithHeader` for other schemes.

#### `WithHeader`

```go
func WithHeader(key, value string) DialOption
```

Sets a request header sent on every `Client` request (card fetch included).
Calling it repeatedly with the same key replaces the prior value.

#### `WithHTTPClient`

```go
func WithHTTPClient(c *http.Client) DialOption
```

Supplies the `*http.Client` used for all requests. Use it to set timeouts, a
custom transport (proxies, mTLS), or connection pooling. A nil client is
ignored (the default `http.DefaultClient` is kept).

---

### `*RemoteAgent`

```go
type RemoteAgent struct { /* unexported */ }
```

A `core.Agent` backed by a remote A2A server. Every `Execute` call is sent
over the wire and the remote task's outcome is mapped onto a `core.AgentResult`.
Construct via `Dial`, never directly.

Safe for concurrent use by multiple goroutines.

#### `(*RemoteAgent).Name`

```go
func (a *RemoteAgent) Name() string
```

Returns the remote agent's card name sanitized to a tool-safe identifier:
lowercased, with every character outside `[a-z0-9]` replaced by `_`.
Network builds tool-call names from this value, so "Research Helper" becomes
`"research_helper"`. An all-invalid or empty name yields `"_"`.

#### `(*RemoteAgent).Description`

```go
func (a *RemoteAgent) Description() string
```

Returns the remote agent's card description, used by Network to generate the
routing-LLM tool definition.

#### `(*RemoteAgent).Client`

```go
func (a *RemoteAgent) Client() *Client
```

Returns the underlying low-level protocol `Client` for callers that need
direct `GetTask` / `CancelTask` / `Stream` access. The `Client` is safe for
concurrent use.

#### `(*RemoteAgent).Execute`

```go
func (a *RemoteAgent) Execute(ctx context.Context, task core.AgentTask, opts ...core.RunOption) (core.AgentResult, error)
```

Runs the task on the remote agent and maps the result onto a `core.AgentResult`.

**Streaming (fresh turn):** when `opts` includes `core.WithStream(ch)` and
there is no pending resume for the thread, sends `SendStreamingMessage` and
forwards text deltas to `ch` live. The stream channel is always closed exactly
once before `Execute` returns, on every path including errors. If the server
streams append-delta chunks but omits a LastChunk artifact replay (a valid
non-Oasis pattern), the accumulated delta text is used as the final `Output`
so callers always receive a non-empty result on a completed task.

**Resume:** when a prior `Execute` on the same `ThreadID` left the remote task
at `TASK_STATE_INPUT_REQUIRED`, this call resumes it by carrying the task ID.
Resume over the streaming transport is unsupported server-side, so a streaming
resume falls back to a blocking `SendMessage` (no deltas are produced for the
resume turn); the stream channel is still closed.

**Blocking (the default):** sends `SendMessage`, then polls `GetTask` every
500 ms until the task reaches a terminal or interrupted state. The poll loop
is ctx-aware.

**Result mapping:**

| Remote task state | `AgentResult.FinishReason` | error |
|---|---|---|
| `TASK_STATE_COMPLETED` | `FinishStop` | nil |
| `TASK_STATE_INPUT_REQUIRED` / `TASK_STATE_AUTH_REQUIRED` | `FinishSuspended` | nil |
| `TASK_STATE_CANCELED` | `FinishHalted` | nil |
| `TASK_STATE_FAILED` / other | `FinishError` | wraps `ErrInvalidAgentResp` |

---

### `*Client`

```go
type Client struct { /* unexported */ }
```

Low-level A2A JSON-RPC client for one remote agent. It speaks the protocol
(SendMessage, GetTask, CancelTask, streaming) and maps wire errors back to
package sentinels, but holds no conversation state. Construct via `Dial`.

Safe for concurrent use by multiple goroutines.

#### `(*Client).SendMessage`

```go
func (c *Client) SendMessage(ctx context.Context, msg Message, cfg *SendConfiguration) (Task, error)
```

Sends one message and returns the resulting `Task`. Pass `cfg = nil` for the
default blocking behavior.

When the server answers with a bare `Message` (some servers reply
message-only), `SendMessage` synthesizes a completed `Task` holding the
message's parts as a single artifact so callers map every response uniformly.
A response carrying neither is a protocol violation and yields
`ErrInvalidAgentResp`.

#### `(*Client).GetTask`

```go
func (c *Client) GetTask(ctx context.Context, id string) (Task, error)
```

Fetches the current state of a task by ID. A missing task yields
`ErrTaskNotFound` (mapped across the wire via `errors.Is`).

#### `(*Client).CancelTask`

```go
func (c *Client) CancelTask(ctx context.Context, id string) (Task, error)
```

Requests cancellation of a task by ID and returns its post-cancel state.
Canceling a terminal task yields `ErrTaskNotCancelable`.

#### `(*Client).Stream`

```go
func (c *Client) Stream(ctx context.Context, msg Message, fn func(StreamResponse) bool) error
```

Sends a streaming message and invokes `fn` for each decoded `StreamResponse`
frame as it arrives. `fn` returns `false` to stop early (Stream then returns
nil after closing the connection).

Each event is parsed from a single `data:` line; the SSE multi-line `data:`
concatenation rule is not supported — Oasis servers emit single-line frames.

Frames that fail to unmarshal are tolerated and skipped (SSE keep-alives are
not JSON-RPC). An envelope error stops the stream and is mapped to a Go error
via the sentinel mapping. Does not retry; `ctx` cancellation aborts it.

#### `(*Client).Card`

```go
func (c *Client) Card() AgentCard
```

Returns the agent card fetched at `Dial` time. The returned value is a
shallow copy of the immutable card; contained slices and maps are shared with
the `Client`'s internal copy and must not be mutated.

---

### `AsTool`

```go
func AsTool(agent *RemoteAgent) core.AnyTool
```

Wraps a `*RemoteAgent` as a `core.AnyTool` so an `LLMAgent` can call it like
any other tool, delegating a sub-task to the remote agent mid-run. The tool's
name and description come from the remote agent's card; it takes one `"input"`
string parameter (the natural-language task).

A remote task failure becomes a tool-level `ToolResult.Error` the LLM can read
and react to — not a Go error that would abort the run. Only
transport/infrastructure errors return a Go error. Safe for concurrent use.

---

## Protocol types

### `AgentCard`

```go
type AgentCard struct {
    Name                string
    Description         string
    SupportedInterfaces []AgentInterface
    Version             string
    Capabilities        AgentCapabilities
    SecuritySchemes     map[string]SecurityScheme
    Skills              []AgentSkill
    DefaultInputModes   []string
    DefaultOutputModes  []string
}
```

Discovery document published at `WellKnownCardPath`. `Name`,
`SupportedInterfaces`, and `Capabilities` are the load-bearing fields.

---

### `AgentCapabilities`

```go
type AgentCapabilities struct {
    Streaming         bool
    PushNotifications bool
    ExtendedAgentCard bool
}
```

Declares optional protocol features the agent supports. Set automatically by
`NewServer` based on options: `Streaming` is always `true`; `PushNotifications`
is `true` when `WithPushNotifications` is set.

---

### `AgentInterface`

```go
type AgentInterface struct {
    URL             string
    ProtocolBinding string
    Tenant          string
    ProtocolVersion string
}
```

Declares a single endpoint at which the agent is reachable. `ProtocolBinding`
uses the `Binding*` constants or a custom URI string.

```go
const (
    BindingJSONRPC  = "JSONRPC"
    BindingGRPC     = "GRPC"
    BindingHTTPJSON = "HTTP+JSON"
)
```

---

### `AgentSkill`

```go
type AgentSkill struct {
    ID          string
    Name        string
    Description string
    Tags        []string
    InputModes  []string
    OutputModes []string
    Examples    []string
}
```

Describes one capability advertised on the card. `ID` and `Name` are required.

---

### `SecurityScheme`

```go
type SecurityScheme struct {
    APIKey        *APIKeySecurityScheme
    HTTPAuth      *HTTPAuthSecurityScheme
    OAuth2        *OAuth2SecurityScheme
    OpenIDConnect *OpenIDConnectSecurityScheme
    MTLS          *MTLSSecurityScheme
}
```

Discriminated union of the five auth mechanisms defined in A2A v1.0 §4.5.1.
Exactly one pointer field must be set. Example for bearer auth:

```go
a2a.WithSecurityScheme("bearer", a2a.SecurityScheme{
    HTTPAuth: &a2a.HTTPAuthSecurityScheme{Scheme: "Bearer"},
})
```

The five sub-types (each carries an optional `Description`):

#### `APIKeySecurityScheme`

```go
type APIKeySecurityScheme struct {
    Description string
    Location    string // "query", "header", or "cookie"
    Name        string // the header/query/cookie parameter name
}
```

API-key authentication (A2A v1.0 §4.5.2).

#### `HTTPAuthSecurityScheme`

```go
type HTTPAuthSecurityScheme struct {
    Description  string
    Scheme       string // IANA-registered auth scheme, e.g. "Bearer"
    BearerFormat string // optional hint, e.g. "JWT"
}
```

HTTP authentication — Basic, Bearer, etc. (A2A v1.0 §4.5.3).

#### `OAuth2SecurityScheme`

```go
type OAuth2SecurityScheme struct {
    Description       string
    Flows             json.RawMessage // nested OAuthFlows object, passed through verbatim
    OAuth2MetadataURL string          // RFC 8414 authorization-server metadata URL
}
```

OAuth 2.0 authentication (A2A v1.0 §4.5.4). `Flows` stays `json.RawMessage` so
the nested flow configuration passes through without re-encoding.

#### `OpenIDConnectSecurityScheme`

```go
type OpenIDConnectSecurityScheme struct {
    Description      string
    OpenIDConnectURL string // OIDC Discovery URL
}
```

OpenID Connect authentication (A2A v1.0 §4.5.5).

#### `MTLSSecurityScheme`

```go
type MTLSSecurityScheme struct {
    Description string
}
```

Mutual TLS authentication (A2A v1.0 §4.5.6). No fields beyond `Description`;
its presence declares the mTLS requirement.

---

### `Task`

```go
type Task struct {
    ID        string
    ContextID string
    Status    TaskStatus
    Artifacts []Artifact
    History   []Message
    Metadata  json.RawMessage
}
```

A stateful unit of delegated work. `Status` carries the current state;
`Artifacts` holds completed outputs; `History` is the message sequence.

---

### `TaskStatus`

```go
type TaskStatus struct {
    State     TaskState
    Message   *Message
    Timestamp string // RFC 3339, UTC
}
```

Current state of a task plus an optional agent message (e.g. the suspend
question accompanying `TASK_STATE_INPUT_REQUIRED`) and a timestamp.

---

### `TaskState`

```go
type TaskState string

const (
    TaskStateUnspecified    TaskState = "TASK_STATE_UNSPECIFIED"
    TaskStateSubmitted      TaskState = "TASK_STATE_SUBMITTED"
    TaskStateWorking        TaskState = "TASK_STATE_WORKING"
    TaskStateCompleted      TaskState = "TASK_STATE_COMPLETED"
    TaskStateFailed         TaskState = "TASK_STATE_FAILED"
    TaskStateCanceled       TaskState = "TASK_STATE_CANCELED"
    TaskStateInputRequired  TaskState = "TASK_STATE_INPUT_REQUIRED"
    TaskStateRejected       TaskState = "TASK_STATE_REJECTED"
    TaskStateAuthRequired   TaskState = "TASK_STATE_AUTH_REQUIRED"
)
```

Wire values use `SCREAMING_SNAKE_CASE` per the ProtoJSON convention (A2A
ADR-001). `TaskState.Terminal()` reports whether the state is final;
`TASK_STATE_INPUT_REQUIRED` and `TASK_STATE_AUTH_REQUIRED` are interrupted
(not terminal) — the task can still progress.

#### `(TaskState).Terminal`

```go
func (s TaskState) Terminal() bool
```

Reports whether this state is final: `COMPLETED`, `FAILED`, `CANCELED`, and
`REJECTED` are terminal. `INPUT_REQUIRED` and `AUTH_REQUIRED` are not.

---

### `Message`

```go
type Message struct {
    MessageID        string
    ContextID        string
    TaskID           string
    Role             Role
    Parts            []Part
    Metadata         json.RawMessage
    Extensions       []string
    ReferenceTaskIDs []string
}
```

A single communication turn between client and agent. `MessageID` is required.
`ContextID` is the conversation scope (maps to `AgentTask.ThreadID`). `TaskID`
on an inbound message signals "resume this task."

---

### `Role`

```go
type Role string

const (
    RoleUnspecified Role = "ROLE_UNSPECIFIED"
    RoleUser        Role = "ROLE_USER"
    RoleAgent       Role = "ROLE_AGENT"
)
```

---

### `Part`

```go
type Part struct {
    Text      string
    Raw       []byte          // base64 on the wire
    URL       string
    Data      json.RawMessage // zero-copy passthrough
    MediaType string
    Filename  string
    Metadata  json.RawMessage
}
```

One content unit inside a `Message` or `Artifact`. Exactly one of `Text`,
`Raw`, `URL`, or `Data` is set. `Data` stays `json.RawMessage` so large
structured payloads pass through without re-encoding (zero-copy).

#### `TextPart`

```go
func TextPart(s string) Part
```

Convenience constructor for the common plain-text case.

---

### `Artifact`

```go
type Artifact struct {
    ArtifactID  string
    Name        string
    Description string
    Parts       []Part
    Extensions  []string
    Metadata    json.RawMessage
}
```

A tangible output of a task. `ArtifactID` is unique within a task. `Parts`
holds the content (at least one).

---

### `StreamResponse`

```go
type StreamResponse struct {
    Task           *Task
    Message        *Message
    StatusUpdate   *TaskStatusUpdateEvent
    ArtifactUpdate *TaskArtifactUpdateEvent
}
```

Wraps exactly one event payload per SSE frame (A2A v1.0 "StreamResponse"
oneof). Used by `Client.Stream` callbacks and delivered to push-notification
webhooks.

---

### `TaskStatusUpdateEvent`

```go
type TaskStatusUpdateEvent struct {
    TaskID    string
    ContextID string
    Status    TaskStatus
    Final     bool
    Metadata  json.RawMessage
}
```

Streams a task state change. `Final` is set on the last status event of a
stream. `ContextID` is always present (not omitted) per the proto
`field_behavior` requirement.

---

### `TaskArtifactUpdateEvent`

```go
type TaskArtifactUpdateEvent struct {
    TaskID    string
    ContextID string
    Artifact  Artifact
    Append    bool
    LastChunk bool
    Metadata  json.RawMessage
}
```

Streams an artifact chunk. `Append` signals that the content should be
appended to a previously sent artifact with the same ID; `LastChunk` marks
the final chunk.

---

### `SendConfiguration`

```go
type SendConfiguration struct {
    AcceptedOutputModes    []string
    Blocking               bool
    PushNotificationConfig *PushNotificationConfig
    HistoryLength          int
}
```

Per-request send options passed as the `cfg` argument to `Client.SendMessage`.
Pass `nil` for the default blocking behavior.

`AcceptedOutputModes` lists the MIME types the caller is willing to receive;
omit for no restriction. `Blocking` controls server-side scheduling: `true`
(the default when the field is zero) runs the task inline; `false` starts the
task in the background and returns immediately with a working task — a
`PushNotificationConfig` is required for the non-blocking path (the server
rejects non-blocking sends without one). `HistoryLength` limits the number of
history messages in the returned `Task`; zero means unbounded.

---

### `PushNotificationConfig`

```go
type PushNotificationConfig struct {
    ID             string
    URL            string
    Token          string
    Authentication *AuthenticationInfo
}
```

Registers a webhook for asynchronous task updates. The server POSTs
`StreamResponse` payloads to `URL` when the task's state changes. `Token` is
echoed back in the `X-A2A-Notification-Token` header so receivers can validate
the caller.

---

### `AuthenticationInfo`

```go
type AuthenticationInfo struct {
    Scheme      string
    Credentials string
}
```

Describes webhook authentication. `Scheme` is an HTTP auth scheme name (e.g.
`"Bearer"`); `Credentials` is the scheme-specific value.

---

### Constants

```go
const WellKnownCardPath = "/.well-known/agent-card.json"
```

RFC 8615 discovery path for the agent card. `Dial` appends this to `baseURL`
to fetch the card.

---

## Task store

### `TaskRecord`

```go
type TaskRecord struct {
    Task Task
    // unexported, process-bound runtime fields (resume closure, cancel
    // handle, registered webhook config)
}
```

The unit a `TaskStore` persists and serves: one task's serializable protocol
state plus process-bound runtime handles.

**Serialization contract.** `Task` is the serializable protocol state — a
persistent store marshals and unmarshals exactly that field and nothing else.
The unexported runtime fields are live only inside the process that created the
record; they are **zero** in any record a store reconstructs from durable
storage. This is precisely why a suspended run cannot resume after a process
restart: the resume closure does not survive (see `TaskStore` below). A store
recovering a task from durable state constructs the record as
`&TaskRecord{Task: t}` — the runtime fields stay zero, which the server
tolerates (a recovered task is visible and cancelable, just not resumable).

**Same-instance-while-live contract (CRITICAL).** For a **live** (non-terminal)
task the server mutates the **same** `*TaskRecord` instance it passed to `Save`
— advancing `Status`, attaching artifacts, clearing the cancel handle in place.
A store **must** return that same instance from `Get` and `List` while the task
is live; otherwise a poller or resubscriber re-fetching the record reads a stale
copy and never observes progress. The standard pattern for a persistent store is
an in-memory overlay of live records over the durable backend: serve the live
`*TaskRecord` from the overlay, persist `Task` to the backend on each `Save`,
and fall back to a freshly constructed `&TaskRecord{Task: t}` only for
terminal/restart-recovered tasks that are no longer mutated.

---

### `TaskStore`

```go
type TaskStore interface {
    Save(ctx context.Context, rec *TaskRecord) error
    Get(ctx context.Context, id string) (*TaskRecord, error)
    List(ctx context.Context, contextID string) ([]*TaskRecord, error)
}
```

Persists A2A tasks between protocol requests. The in-memory default supports
the full protocol including resume of input-required tasks. Custom persistent
implementations preserve task visibility across restarts, but suspended runs
cannot resume after a process restart — the resume closure is process-bound.

Contract:

- `Save` inserts or replaces the record keyed by `rec.Task.ID`. Persistent
  stores marshal `rec.Task`; the runtime fields are not serializable.
- `Get` must return an error satisfying `errors.Is(err, ErrTaskNotFound)` when
  the task is not found.
- `List` returns records for a context ID, newest first; returns an empty slice
  (not nil) when none exist.
- While a task is non-terminal, `Get` and `List` must return the very
  `*TaskRecord` the server last `Save`d (the same-instance-while-live rule on
  `TaskRecord`), not a copy.
- Implementations must be safe for concurrent use.

The default bounded in-memory store is constructed automatically by `NewServer`.
Replace it with `WithTaskStore(myStore)`.

---

## Errors

```go
var (
    ErrTaskNotFound      = errors.New("a2a: task not found")
    ErrTaskNotCancelable = errors.New("a2a: task not cancelable")
    ErrPushNotSupported  = errors.New("a2a: push notifications not supported")
    ErrUnsupportedOp     = errors.New("a2a: unsupported operation")
    ErrContentType       = errors.New("a2a: content type not supported")
    ErrInvalidAgentResp  = errors.New("a2a: invalid agent response")
)
```

Sentinel errors for A2A protocol failures. All are `errors.Is`-able and
propagate across the network boundary: a server that returns the corresponding
JSON-RPC error code produces a client-side error that matches the sentinel.
Client methods wrap them with the remote agent name and task ID for
observability.

| Sentinel | When |
|---|---|
| `ErrTaskNotFound` | `GetTask` / `CancelTask` with an unknown ID |
| `ErrTaskNotCancelable` | `CancelTask` on a terminal task |
| `ErrPushNotSupported` | push config methods when `WithPushNotifications` was not set |
| `ErrUnsupportedOp` | `ListTasks`, `GetExtendedAgentCard`, or resume on a non-suspended task |
| `ErrContentType` | unsupported content type in request |
| `ErrInvalidAgentResp` | server returned a malformed or unexpected response |

---

## a2atest

Import path: `github.com/nevindra/oasis/a2a/a2atest`

Test doubles for A2A integrations. The package intentionally does not import
`a2a/` so it can be used as a dependency without a circular import. Compose:
`a2a.Dial(ctx, a2atest.Serve(t, a2a.NewServer(ag)).URL)`.

### `EchoAgent`

```go
type EchoAgent struct { /* unexported */ }
func NewEchoAgent(name, desc string) *EchoAgent
```

Echoes its input as `"echo: <input>"`. Supports streaming (emits two text
delta events). Zero dependencies, instant responses — suitable for unit tests
and benchmarks.

### `BlobAgent`

```go
type BlobAgent struct { /* unexported */ }
func NewBlobAgent(name, desc string, blob []byte) *BlobAgent
```

Returns a fixed binary attachment: `Execute` yields the text output
`"blob ready"` plus one file attachment carrying `blob` verbatim
(`application/octet-stream`). The blob is referenced, not copied, so callers can
pre-allocate once and reuse across iterations — it backs the payload-scaling
benchmarks (`BenchmarkA2ARoundTrip_LargeArtifact`). Zero dependencies, instant
responses.

### `FailingAgent`

```go
type FailingAgent struct { /* unexported */ }
func NewFailingAgent(name, desc string) *FailingAgent
```

Always returns a Go error from `Execute`. Used to verify the server maps an
agent failure to a `TASK_STATE_FAILED` task — not a dropped connection and not
an RPC-level error.

### `SuspendingAgent`

```go
type SuspendingAgent struct { /* unexported */ }
func NewSuspendingAgent(name, desc string) *SuspendingAgent
```

Suspends on the first `Execute` with the question `{"question":"which fiscal
year?"}` and completes on `Resume` with `"resumed with: <input>"`. Used to
test the `TASK_STATE_INPUT_REQUIRED` → resume round-trip. Implements the
resumable contract without importing `agent/`.

### `PanicAgent`

```go
type PanicAgent struct { /* unexported */ }
func NewPanicAgent(name, desc string) *PanicAgent
```

Always panics from `Execute`. Used to verify the server recovers from a
panicking agent and settles the task as `TASK_STATE_FAILED` rather than
crashing the process.

### `BlockingAgent`

```go
type BlockingAgent struct { /* unexported */ }
func NewBlockingAgent(name, desc string) *BlockingAgent
```

Blocks in `Execute` until its context is canceled, then returns
`context.Canceled`. Used to test that `Server.Close()` and other cancellation
paths correctly abort streaming and blocking runs.

### `Serve`

```go
func Serve(t testing.TB, handler http.Handler) *httptest.Server
```

Starts an `httptest.Server` serving `handler` and registers `t.Cleanup` to
close it. Takes `testing.TB` so it works in both `*testing.T` and
`*testing.B` contexts. Takes an `http.Handler` — not a `*a2a.Server` — so
it works with any handler (wrapped with auth middleware, custom mux, etc.).
`a2atest` deliberately does not import `a2a` to avoid an import cycle.
Example:

```go
srv := a2atest.Serve(t, a2a.NewServer(a2atest.NewEchoAgent("echo", "echoes")))
remote, err := a2a.Dial(ctx, srv.URL)
```
