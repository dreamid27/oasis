package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
)

// Client is a low-level A2A JSON-RPC client for one remote agent. It is the
// transport layer beneath RemoteAgent: it speaks the protocol (SendMessage,
// GetTask, CancelTask, streaming) and maps wire errors back to package
// sentinels, but holds no conversation state.
//
// A Client is safe for concurrent use by multiple goroutines: the embedded
// *http.Client is concurrency-safe, the request-ID counter is atomic, and the
// card/headers are immutable after Dial. Construct via Dial, never directly —
// the zero value has no endpoint and will panic on use.
type Client struct {
	endpoint string // JSON-RPC endpoint URL resolved from the agent card
	card     AgentCard
	http     *http.Client
	headers  http.Header  // applied to every request; immutable after Dial
	nextID   atomic.Int64 // monotonic JSON-RPC request id source
}

// dialOptions accumulates DialOption mutations. The zero value is valid: a
// default *http.Client and no extra headers.
type dialOptions struct {
	http    *http.Client
	headers http.Header
}

// DialOption configures Dial. Options are applied in order; later options
// override earlier ones for the same key.
type DialOption func(*dialOptions)

// WithBearerToken sets the Authorization header to "Bearer <tok>" on every
// request the Client makes (card fetch included). Convenience for the common
// HTTP bearer auth scheme; for other schemes use WithHeader.
func WithBearerToken(tok string) DialOption {
	return WithHeader("Authorization", "Bearer "+tok)
}

// WithHeader sets a request header sent on every Client request, including the
// card fetch. Calling it repeatedly with the same key replaces the prior value.
func WithHeader(key, value string) DialOption {
	return func(o *dialOptions) {
		if o.headers == nil {
			o.headers = make(http.Header)
		}
		o.headers.Set(key, value)
	}
}

// WithHTTPClient supplies the *http.Client used for all requests. Use it to set
// timeouts, a custom transport (proxies, mTLS), or connection pooling. A nil
// client is ignored (the default is kept).
func WithHTTPClient(c *http.Client) DialOption {
	return func(o *dialOptions) {
		if c != nil {
			o.http = c
		}
	}
}

// Dial fetches the agent card from baseURL+WellKnownCardPath, resolves the
// JSON-RPC endpoint, and returns a ready RemoteAgent backed by a Client.
//
// The JSON-RPC endpoint is taken from the card's first JSONRPC AgentInterface;
// when the card advertises no interfaces (e.g. a server mounted without WithURL),
// baseURL itself is used as the endpoint. Every error carries baseURL and the
// operation so failures are reconstructable from logs alone.
//
// Dial performs one blocking HTTP GET; it honors ctx for cancellation. The
// returned RemoteAgent is safe for concurrent use.
func Dial(ctx context.Context, baseURL string, opts ...DialOption) (*RemoteAgent, error) {
	var o dialOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	if o.http == nil {
		o.http = http.DefaultClient
	}

	cardURL := strings.TrimRight(baseURL, "/") + WellKnownCardPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cardURL, nil)
	if err != nil {
		return nil, fmt.Errorf("a2a: fetch agent card %s: %w", cardURL, err)
	}
	applyHeaders(req, o.headers)
	req.Header.Set("Accept", "application/json")

	resp, err := o.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("a2a: fetch agent card %s: %w", cardURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Bound the body read: a misbehaving server must not let us buffer
		// unbounded bytes into the error message.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("a2a: fetch agent card %s: status %d: %s",
			cardURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var card AgentCard
	if err := json.NewDecoder(resp.Body).Decode(&card); err != nil {
		return nil, fmt.Errorf("a2a: decode agent card %s: %w", cardURL, err)
	}

	c := &Client{
		endpoint: resolveEndpoint(card, baseURL),
		card:     card,
		http:     o.http,
		headers:  o.headers,
	}
	return &RemoteAgent{client: c, pending: make(map[string]string)}, nil
}

// resolveEndpoint picks the JSON-RPC endpoint from the card, preferring the
// first JSONRPC interface. Why fall back to baseURL: a server mounted without
// WithURL advertises no interfaces, yet the same mount that served the card
// also speaks JSON-RPC — so baseURL is the correct endpoint.
func resolveEndpoint(card AgentCard, baseURL string) string {
	for _, iface := range card.SupportedInterfaces {
		if iface.ProtocolBinding == BindingJSONRPC && iface.URL != "" {
			return iface.URL
		}
	}
	return baseURL
}

// applyHeaders copies h onto req. Nil h is a no-op.
func applyHeaders(req *http.Request, h http.Header) {
	for k, vs := range h {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
}

// Card returns the agent card fetched at Dial time. The returned value is a
// shallow copy of the immutable card; contained slices (SupportedInterfaces,
// Skills, etc.) and maps (SecuritySchemes) are shared with the Client's
// internal copy and must not be mutated.
func (c *Client) Card() AgentCard { return c.card }

// call performs one JSON-RPC POST: it marshals params, assigns an atomically
// incremented numeric id, decodes the response envelope, and either maps an
// envelope error back to a Go error (via rpcErrToGo) or unmarshals the result
// into out. out may be nil to discard the result. ctx governs the request.
func (c *Client) call(ctx context.Context, method string, params, out any) error {
	rawParams, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("a2a: marshal %s params: %w", method, err)
	}
	id, _ := json.Marshal(c.nextID.Add(1))
	body, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  rawParams,
	})
	if err != nil {
		return fmt.Errorf("a2a: marshal %s request: %w", method, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("a2a: %s %s: %w", method, c.endpoint, err)
	}
	applyHeaders(req, c.headers)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("a2a: %s %s: %w", method, c.endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("a2a: %s %s: status %d: %s",
			method, c.endpoint, resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var env rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return fmt.Errorf("a2a: decode %s response: %w", method, err)
	}
	if env.Error != nil {
		return rpcErrToGo(env.Error, c.card.Name)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(env.Result, out); err != nil {
		return fmt.Errorf("a2a: unmarshal %s result: %w", method, err)
	}
	return nil
}

// rpcErrToGo maps a JSON-RPC error envelope back to a package sentinel so
// errors.Is works ACROSS the network boundary: a server that returns
// codeTaskNotCancelable produces an error the caller can match with
// errors.Is(err, ErrTaskNotCancelable). The remote agent name and the wire
// message are wrapped in for observability. Unknown codes (no matching
// sentinel) produce a plain fmt.Errorf carrying agent, code, and message.
func rpcErrToGo(e *rpcError, agentName string) error {
	if sentinel := sentinelForCode(e.Code); sentinel != nil {
		return fmt.Errorf("a2a: agent %q: %w: %s", agentName, sentinel, e.Message)
	}
	return fmt.Errorf("a2a: agent %q: rpc error %d: %s", agentName, e.Code, e.Message)
}

// sentinelForCode is the inverse of codeFor: wire code → sentinel, or nil when
// the code is not an A2A protocol code.
func sentinelForCode(code int) error {
	switch code {
	case codeTaskNotFound:
		return ErrTaskNotFound
	case codeTaskNotCancelable:
		return ErrTaskNotCancelable
	case codePushNotSupported:
		return ErrPushNotSupported
	case codeUnsupportedOp:
		return ErrUnsupportedOp
	case codeContentType:
		return ErrContentType
	case codeInvalidAgentResp:
		return ErrInvalidAgentResp
	}
	return nil
}

// SendMessage sends one message and returns the resulting Task. cfg is optional
// per-request configuration; pass nil for the default blocking behavior.
//
// The server answers with the SendMessage result oneof. When it returns a Task,
// that Task is returned verbatim. When it returns a bare Message (some servers
// answer message-only for a synchronous reply), SendMessage synthesizes a
// completed Task holding the message's parts as a single artifact, so callers
// map every response uniformly. A response carrying neither is a protocol
// violation and yields ErrInvalidAgentResp.
func (c *Client) SendMessage(ctx context.Context, msg Message, cfg *SendConfiguration) (Task, error) {
	var res sendResult
	if err := c.call(ctx, methodSendMessage, sendParams{Message: msg, Configuration: cfg}, &res); err != nil {
		return Task{}, err
	}
	switch {
	case res.Task != nil:
		return *res.Task, nil
	case res.Message != nil:
		return taskFromMessage(*res.Message), nil
	default:
		return Task{}, fmt.Errorf("a2a: agent %q: SendMessage: %w: empty result", c.card.Name, ErrInvalidAgentResp)
	}
}

// taskFromMessage synthesizes a completed Task from a bare reply Message,
// wrapping its parts in one "response" artifact. resultFromTask then maps it
// the same as a real completed Task.
func taskFromMessage(msg Message) Task {
	return Task{
		ID:        msg.TaskID,
		ContextID: msg.ContextID,
		Status:    TaskStatus{State: TaskStateCompleted, Timestamp: nowRFC3339()},
		Artifacts: []Artifact{{Name: "response", Parts: msg.Parts}},
	}
}

// GetTask fetches the current state of a task by id. A missing task yields
// ErrTaskNotFound (mapped across the wire).
func (c *Client) GetTask(ctx context.Context, id string) (Task, error) {
	var t Task
	if err := c.call(ctx, methodGetTask, taskIDParams{ID: id}, &t); err != nil {
		return Task{}, err
	}
	return t, nil
}

// CancelTask requests cancellation of a task by id and returns its post-cancel
// state. Canceling a terminal task yields ErrTaskNotCancelable.
func (c *Client) CancelTask(ctx context.Context, id string) (Task, error) {
	var t Task
	if err := c.call(ctx, methodCancelTask, taskIDParams{ID: id}, &t); err != nil {
		return Task{}, err
	}
	return t, nil
}
