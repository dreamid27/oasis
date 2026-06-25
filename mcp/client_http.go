package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// HTTPClient is an MCP client that communicates over HTTP (stateless JSON-RPC).
// Each method call is an independent POST request. It is safe for concurrent use.
type HTTPClient struct {
	url          string
	headers      map[string]string
	auth         Auth
	httpClient   *http.Client
	nextID       atomic.Int64
	disconnectFn atomic.Value // stores func(error)
}

// NewHTTPClient constructs an HTTP-transport MCP client.
// extraHeaders are added to every request before auth (auth may override them).
// timeout is applied per-request; 0 means no timeout.
func NewHTTPClient(url string, extraHeaders map[string]string, auth Auth, timeout time.Duration) *HTTPClient {
	return &HTTPClient{
		url:        url,
		headers:    extraHeaders,
		auth:       auth,
		httpClient: &http.Client{Timeout: timeout},
	}
}

func (c *HTTPClient) call(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	req := rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	body, err := json.Marshal(&req)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	// Streamable-HTTP servers (e.g. GitHub) reject requests that don't accept both.
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range c.headers {
		httpReq.Header.Set(k, v)
	}
	if c.auth != nil {
		if err := c.auth.Apply(httpReq); err != nil {
			return nil, fmt.Errorf("auth: %w", err)
		}
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		c.notifyDisconnect(err)
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(raw))
	}

	// Streamable-HTTP servers may answer a single request with an SSE stream
	// instead of a JSON body; pull the JSON-RPC payload out of the data events.
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		raw, err = sseJSONPayload(raw)
		if err != nil {
			return nil, err
		}
	}

	var rpcResp rpcResponse
	if err := json.Unmarshal(raw, &rpcResp); err != nil {
		return nil, fmt.Errorf("decode rpc response: %w (body: %s)", err, raw)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	return rpcResp.Result, nil
}

func (c *HTTPClient) notifyDisconnect(err error) {
	if fn, ok := c.disconnectFn.Load().(func(error)); ok && fn != nil {
		fn(err)
	}
}

// OnDisconnect registers a callback fired when an HTTP request fails (transport
// error). For HTTP clients this is a best-effort signal — a single callback is
// stored; a second registration replaces the first.
func (c *HTTPClient) OnDisconnect(fn func(error)) {
	c.disconnectFn.Store(fn)
}

// Initialize performs the MCP initialize handshake and returns the server's
// declared info and capabilities.
func (c *HTTPClient) Initialize(ctx context.Context) (*InitializeResult, error) {
	params := json.RawMessage(fmt.Sprintf(`{
		"protocolVersion":%q,
		"capabilities":{"tools":{},"resources":{}},
		"clientInfo":{"name":"oasis","version":"0.x.0"}
	}`, protocolVersion))
	raw, err := c.call(ctx, "initialize", params)
	if err != nil {
		return nil, err
	}
	var res InitializeResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// ListTools fetches the server's tool catalog.
func (c *HTTPClient) ListTools(ctx context.Context) (*ListToolsResult, error) {
	raw, err := c.call(ctx, "tools/list", json.RawMessage(`{}`))
	if err != nil {
		return nil, err
	}
	var res ListToolsResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// CallTool invokes a tool by name. args may be nil or empty (treated as `{}`).
func (c *HTTPClient) CallTool(ctx context.Context, name string, args json.RawMessage) (*CallToolResult, error) {
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	params, err := json.Marshal(map[string]interface{}{"name": name, "arguments": json.RawMessage(args)})
	if err != nil {
		return nil, fmt.Errorf("marshal call params: %w", err)
	}
	raw, err := c.call(ctx, "tools/call", params)
	if err != nil {
		return nil, err
	}
	var res CallToolResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (c *HTTPClient) listResources(ctx context.Context) ([]ResourceInfo, error) {
	raw, err := c.call(ctx, "resources/list", json.RawMessage(`{}`))
	if err != nil {
		return nil, err
	}
	return decodeResourceList(raw)
}

func (c *HTTPClient) readResource(ctx context.Context, uri string) ([]ResourceContent, error) {
	params, _ := json.Marshal(map[string]string{"uri": uri})
	raw, err := c.call(ctx, "resources/read", params)
	if err != nil {
		return nil, err
	}
	return decodeResourceRead(raw)
}

func (c *HTTPClient) listPrompts(ctx context.Context) ([]Prompt, error) {
	raw, err := c.call(ctx, "prompts/list", json.RawMessage(`{}`))
	if err != nil {
		return nil, err
	}
	return decodePromptsList(raw)
}

func (c *HTTPClient) getPrompt(ctx context.Context, name string, args map[string]string) (*PromptResult, error) {
	params, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
	raw, err := c.call(ctx, "prompts/get", params)
	if err != nil {
		return nil, err
	}
	return decodePromptGet(raw)
}

func (c *HTTPClient) setLogLevel(ctx context.Context, level LogLevel) error {
	params, _ := json.Marshal(map[string]string{"level": string(level)})
	_, err := c.call(ctx, "logging/setLevel", params)
	return err
}

// sseJSONPayload extracts the JSON-RPC response from an SSE body. A server may
// interleave notifications/requests (also jsonrpc:"2.0") with the response on the
// same stream, so we keep only a data event that carries a result or error — the
// actual response to our request — not merely the last jsonrpc-shaped event.
func sseJSONPayload(raw []byte) ([]byte, error) {
	var data []string
	var found []byte
	flush := func() {
		if len(data) == 0 {
			return
		}
		joined := strings.Join(data, "\n")
		data = nil
		var probe rpcResponse
		if json.Unmarshal([]byte(joined), &probe) == nil && probe.JSONRPC != "" &&
			(len(probe.Result) > 0 || probe.Error != nil) {
			found = []byte(joined)
		}
	}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			flush()
			continue
		}
		if d, ok := strings.CutPrefix(line, "data:"); ok {
			data = append(data, strings.TrimPrefix(d, " "))
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read SSE response (line too large or read error): %w", err)
	}
	flush()
	if found == nil {
		return nil, fmt.Errorf("no JSON-RPC data event in SSE response (body: %s)", raw)
	}
	return found, nil
}

// Close releases idle connections. HTTP is stateless so no active connections
// to close; this is a best-effort cleanup.
func (c *HTTPClient) Close(_ context.Context) error {
	c.httpClient.CloseIdleConnections()
	return nil
}
