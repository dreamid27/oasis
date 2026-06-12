package a2a

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// streamBufInit is the initial scanner buffer (64 KiB) and streamBufMax its hard
// cap (16 MiB). A single SSE frame carries one StreamResponse; the cap bounds a
// pathological server frame so a slow/hostile peer cannot drive unbounded
// allocation, while 16 MiB comfortably holds large data-part artifacts.
const (
	streamBufInit = 64 * 1024
	streamBufMax  = 16 * 1024 * 1024
)

// Stream sends a streaming message and invokes fn for each decoded
// StreamResponse frame as it arrives. fn returns false to stop early (Stream
// then returns nil after closing the connection).
//
// Transport: POST SendStreamingMessage with Accept: text/event-stream. A
// non-200 status is returned as an error with a bounded snippet of the body.
// Each SSE "data:" frame is unwrapped from its JSON-RPC envelope; frames that
// fail to unmarshal are tolerated and skipped (SSE keep-alives / comments are
// not JSON-RPC). An envelope error stops the stream and is mapped to a Go error
// via rpcErrToGo. At EOF, Stream returns the scanner's error (nil on a clean
// close).
//
// Each event is parsed from a single "data:" line; the SSE multi-line "data:"
// concatenation rule is not supported — Oasis servers emit single-line frames.
//
// Stream does not retry and holds the HTTP connection for its full duration;
// ctx cancellation aborts it. Safe to call concurrently on one Client.
func (c *Client) Stream(ctx context.Context, msg Message, fn func(StreamResponse) bool) error {
	body, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		ID:      mustID(c.nextID.Add(1)),
		Method:  methodSendStreamingMessage,
		Params:  mustMarshal(sendParams{Message: msg}),
	})
	if err != nil {
		return fmt.Errorf("a2a: marshal stream request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("a2a: stream %s: %w", c.endpoint, err)
	}
	applyHeaders(req, c.headers)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("a2a: stream %s: %w", c.endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("a2a: stream %s: status %d: %s",
			c.endpoint, resp.StatusCode, strings.TrimSpace(string(b)))
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, streamBufInit), streamBufMax)
	for sc.Scan() {
		line := sc.Bytes()
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue // blank lines, SSE comments, other field names
		}
		payload := bytes.TrimSpace(line[len("data:"):])
		if len(payload) == 0 {
			continue
		}

		var env rpcResponse
		if err := json.Unmarshal(payload, &env); err != nil {
			continue // tolerate non-envelope frames (keep-alives)
		}
		if env.Error != nil {
			return rpcErrToGo(env.Error, c.card.Name)
		}
		if len(env.Result) == 0 {
			continue
		}
		var sr StreamResponse
		if err := json.Unmarshal(env.Result, &sr); err != nil {
			continue // unparseable result payload — skip the frame
		}
		if !fn(sr) {
			return nil // consumer asked to stop
		}
	}
	return sc.Err()
}

// mustID marshals a JSON-RPC numeric id; the input is always a plain int64 so
// json.Marshal cannot fail.
func mustID(n int64) json.RawMessage {
	b, _ := json.Marshal(n)
	return b
}

// mustMarshal marshals sendParams whose only field that can fail is raw JSON
// already validated upstream; errors are impossible for the shapes we pass.
func mustMarshal(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
