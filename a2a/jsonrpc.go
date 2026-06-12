package a2a

import (
	"encoding/json"
	"io"
	"net/http"
)

// rpcRequest is a JSON-RPC 2.0 request. ID stays raw: the spec allows
// string or number and we must echo it back byte-identical.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Standard JSON-RPC 2.0 codes.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// decodeRPCRequest reads one JSON-RPC request from r. Returns a ready-to-send
// *rpcError (never a Go error) so handlers map failures to wire codes
// uniformly.
func decodeRPCRequest(r io.Reader) (rpcRequest, *rpcError) {
	var req rpcRequest
	dec := json.NewDecoder(r)
	if err := dec.Decode(&req); err != nil {
		return req, &rpcError{Code: codeParseError, Message: "parse error: " + err.Error()}
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		return req, &rpcError{Code: codeInvalidRequest, Message: "invalid request: jsonrpc must be \"2.0\" with a method"}
	}
	return req, nil
}

// marshalResult wraps a handler result into a response envelope.
// Marshal failures become internal errors — never a dropped connection.
// Used by test helpers that need an rpcResponse value; the production
// server response path uses writeRPCResult to avoid a second marshal pass.
func marshalResult(id json.RawMessage, v any) rpcResponse {
	raw, err := json.Marshal(v)
	if err != nil {
		return rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: codeInternalError, Message: "marshal result: " + err.Error()}}
	}
	return rpcResponse{JSONRPC: "2.0", ID: id, Result: raw}
}

// writeRPCResult streams {"jsonrpc":"2.0","id":<id>,"result":<raw>} directly
// to w without re-encoding the pre-marshaled result — large artifact payloads
// pass through exactly once (zero-copy passthrough requirement).
//
// The envelope is assembled with raw w.Write calls that mirror the hand-rolled
// technique in sseWriter.send: marshal v once, then write the prefix, id or
// null, "result": literal, raw bytes, closing brace, and newline. The
// json.Encoder used in writeJSON would re-process (compact/copy) the
// RawMessage a second time, which doubles memory for large payloads.
//
// Why: for a 1 MB artifact the base64 JSON is ~1.33 MB. writeJSON's
// json.NewEncoder(w).Encode(rpcResponse{Result: raw}) causes encoding/json to
// compact-copy that RawMessage into the output stream — a second ~1.33 MB
// allocation. writeRPCResult eliminates that second pass by writing the static
// envelope fragments around the already-marshaled bytes.
//
// Marshal failure of v falls back to an internal-error envelope via writeJSON
// (same observable behaviour as marshalResult's error path).
func writeRPCResult(w http.ResponseWriter, id json.RawMessage, v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		writeJSON(w, rpcResponse{JSONRPC: "2.0", ID: id,
			Error: &rpcError{Code: codeInternalError, Message: "marshal result: " + err.Error()}})
		return
	}
	// Hand-assemble the envelope so raw is written verbatim — no second marshal pass.
	w.Write([]byte(`{"jsonrpc":"2.0","id":`)) //nolint:errcheck // mid-body: nothing actionable on failure
	if len(id) > 0 {
		w.Write(id) //nolint:errcheck
	} else {
		w.Write([]byte("null")) //nolint:errcheck
	}
	w.Write([]byte(`,"result":`)) //nolint:errcheck
	w.Write(raw)                  //nolint:errcheck
	w.Write([]byte("}\n"))        //nolint:errcheck
}

// sendParams is the params object of SendMessage / SendStreamingMessage.
type sendParams struct {
	Message       Message            `json:"message"`
	Configuration *SendConfiguration `json:"configuration,omitempty"`
	Metadata      json.RawMessage    `json:"metadata,omitempty"`
}

// sendResult is the SendMessage response oneof: exactly one of Task or
// Message is set, discriminated by key presence on the wire.
type sendResult struct {
	Task    *Task    `json:"task,omitempty"`
	Message *Message `json:"message,omitempty"`
}

// taskIDParams is the params shape shared by GetTask, CancelTask, and
// SubscribeToTask. HistoryLength limits the returned history (GetTask only;
// zero means unbounded).
type taskIDParams struct {
	ID            string `json:"id"`
	HistoryLength int    `json:"historyLength,omitempty"`
}
