package a2a

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeRPCRequest(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":7,"method":"SendMessage","params":{"message":{"messageId":"m1","role":"ROLE_USER","parts":[{"text":"hi"}]}}}`
	req, rpcErr := decodeRPCRequest(strings.NewReader(body))
	if rpcErr != nil {
		t.Fatalf("unexpected error: %+v", rpcErr)
	}
	if req.Method != methodSendMessage {
		t.Errorf("method = %q", req.Method)
	}
	var p sendParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		t.Fatal(err)
	}
	if p.Message.Parts[0].Text != "hi" {
		t.Errorf("params round-trip failed: %+v", p)
	}
}

func TestDecodeRPCRequestMalformed(t *testing.T) {
	_, rpcErr := decodeRPCRequest(strings.NewReader(`{not json`))
	if rpcErr == nil || rpcErr.Code != codeParseError {
		t.Fatalf("want parse error -32700, got %+v", rpcErr)
	}
}

func TestRPCResponseMarshal(t *testing.T) {
	out, err := json.Marshal(rpcResponse{JSONRPC: "2.0", ID: json.RawMessage(`7`), Result: json.RawMessage(`{"ok":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"result":{"ok":true}`) {
		t.Errorf("marshal: %s", out)
	}
}

// TestWriteRPCResult verifies that writeRPCResult produces a valid JSON-RPC 2.0
// envelope: well-formed JSON, id echoed byte-identical, result intact, and
// the envelope is structurally equivalent to what marshalResult + json.Marshal
// would produce.
func TestWriteRPCResult(t *testing.T) {
	tests := []struct {
		name   string
		id     json.RawMessage
		result any
	}{
		{
			name:   "numeric id",
			id:     json.RawMessage(`7`),
			result: map[string]any{"ok": true},
		},
		{
			name:   "string id",
			id:     json.RawMessage(`"req-1"`),
			result: map[string]any{"task": map[string]any{"id": "t1"}},
		},
		{
			name:   "null id",
			id:     nil,
			result: map[string]any{"ok": true},
		},
		{
			name:   "large payload passthrough",
			id:     json.RawMessage(`42`),
			result: map[string]any{"data": strings.Repeat("x", 10*1024)},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeRPCResult(rec, tc.id, tc.result)

			body := rec.Body.Bytes()

			// Must be valid JSON.
			var envelope map[string]json.RawMessage
			if err := json.Unmarshal(body, &envelope); err != nil {
				t.Fatalf("invalid JSON output: %v\nbody: %s", err, body)
			}

			// jsonrpc field must be "2.0".
			if string(envelope["jsonrpc"]) != `"2.0"` {
				t.Errorf("jsonrpc field = %s, want \"2.0\"", envelope["jsonrpc"])
			}

			// id must be echoed byte-identical (or "null" when not set).
			wantID := string(tc.id)
			if wantID == "" {
				wantID = "null"
			}
			if string(envelope["id"]) != wantID {
				t.Errorf("id field = %s, want %s", envelope["id"], wantID)
			}

			// result must be present and decode to the same value as tc.result.
			if _, ok := envelope["result"]; !ok {
				t.Fatal("result field missing")
			}
			wantRaw, err := json.Marshal(tc.result)
			if err != nil {
				t.Fatal(err)
			}
			if string(envelope["result"]) != string(wantRaw) {
				t.Errorf("result field = %s, want %s", envelope["result"], wantRaw)
			}

			// error field must be absent on success.
			if _, hasErr := envelope["error"]; hasErr {
				t.Errorf("unexpected error field in success response")
			}
		})
	}
}

// TestWriteRPCResult_MarshalError verifies that writeRPCResult falls back to an
// internal-error envelope when the result cannot be marshaled.
func TestWriteRPCResult_MarshalError(t *testing.T) {
	rec := httptest.NewRecorder()
	// Channels cannot be marshaled to JSON.
	writeRPCResult(rec, json.RawMessage(`1`), make(chan struct{}))

	body := rec.Body.Bytes()
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("invalid JSON on error fallback: %v\nbody: %s", err, body)
	}
	if _, ok := envelope["error"]; !ok {
		t.Fatal("expected error field on marshal failure")
	}
	if _, ok := envelope["result"]; ok {
		t.Fatal("result field must be absent on marshal failure")
	}
}

func TestSendResultOneof(t *testing.T) {
	// SendMessage result is a oneof: {"task":{...}} or {"message":{...}}.
	var r sendResult
	if err := json.Unmarshal([]byte(`{"task":{"id":"t1","status":{"state":"TASK_STATE_WORKING"}}}`), &r); err != nil {
		t.Fatal(err)
	}
	if r.Task == nil || r.Task.ID != "t1" || r.Message != nil {
		t.Errorf("oneof decode: %+v", r)
	}
}
