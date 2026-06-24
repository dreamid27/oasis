package dashscope

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	oasis "github.com/nevindra/oasis/core"
)

// fakeImageBytes is a non-zero payload returned by the mock image server.
var fakeImageBytes = make([]byte, 2048)

func init() {
	for i := range fakeImageBytes {
		fakeImageBytes[i] = 0xFF
	}
}

// imageURL returns the full URL for a path on the test server, derived from the
// request's Host header (avoids the closure-ordering problem where srv.URL is
// not yet assigned when the handler is constructed).
func imageURL(r *http.Request, path string) string {
	return "http://" + r.Host + path
}

// --- Option tests ---

func TestNew_DefaultName(t *testing.T) {
	p := New("key", "model", "http://localhost")
	if p.Name() != "dashscope" {
		t.Errorf("expected default name 'dashscope', got %q", p.Name())
	}
}

func TestNew_WithName(t *testing.T) {
	p := New("key", "model", "http://localhost", WithName("dashscope-intl"))
	if p.Name() != "dashscope-intl" {
		t.Errorf("expected name 'dashscope-intl', got %q", p.Name())
	}
}

func TestNew_WithHTTPClient(t *testing.T) {
	custom := &http.Client{}
	p := New("key", "model", "http://localhost", WithHTTPClient(custom))
	if p.client != custom {
		t.Error("expected custom HTTP client to be set")
	}
}

func TestNew_BaseURLTrailingSlash(t *testing.T) {
	p := New("key", "model", "http://localhost/api/v1/")
	if p.baseURL != "http://localhost/api/v1" {
		t.Errorf("expected trailing slash stripped, got %q", p.baseURL)
	}
}

// --- generateSync tests ---

func TestGenerateSync_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/services/aigc/multimodal-generation/generation":
			if r.Header.Get("Authorization") != "Bearer test-key" {
				t.Errorf("unexpected auth header: %s", r.Header.Get("Authorization"))
			}
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body["model"] != "qwen-image-2.0" {
				t.Errorf("expected model qwen-image-2.0, got %v", body["model"])
			}
			resp := map[string]any{
				"output": map[string]any{
					"choices": []any{
						map[string]any{
							"message": map[string]any{
								"content": []any{
									map[string]any{"image": imageURL(r, "/fake-image.png")},
								},
							},
						},
					},
				},
				"usage": map[string]any{"image_count": 1},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)

		case "/fake-image.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write(fakeImageBytes)

		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := New("test-key", "qwen-image-2.0", srv.URL, WithHTTPClient(srv.Client()))

	attachments, err := p.generateSync(context.Background(), "a red fox")
	if err != nil {
		t.Fatalf("generateSync: %v", err)
	}
	if len(attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(attachments))
	}
	if attachments[0].MimeType != "image/png" {
		t.Errorf("expected mime image/png, got %q", attachments[0].MimeType)
	}
	if len(attachments[0].Data) != len(fakeImageBytes) {
		t.Errorf("expected %d bytes, got %d", len(fakeImageBytes), len(attachments[0].Data))
	}
}

func TestGenerateSync_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"code":    "InvalidApiKey",
			"message": "bad key",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := New("test-key", "qwen-image-2.0", srv.URL, WithHTTPClient(srv.Client()))

	_, err := p.generateSync(context.Background(), "a red fox")
	if err == nil {
		t.Fatal("expected error for API error response")
	}
	llmErr, ok := err.(*oasis.ErrLLM)
	if !ok {
		t.Fatalf("expected *oasis.ErrLLM, got %T: %v", err, err)
	}
	if llmErr.Provider != "dashscope" {
		t.Errorf("expected provider 'dashscope', got %q", llmErr.Provider)
	}
}

func TestGenerateSync_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer srv.Close()

	p := New("test-key", "qwen-image-2.0", srv.URL, WithHTTPClient(srv.Client()))

	_, err := p.generateSync(context.Background(), "a red fox")
	if err == nil {
		t.Fatal("expected error for 429 response")
	}
	httpErr, ok := err.(*oasis.ErrHTTP)
	if !ok {
		t.Fatalf("expected *oasis.ErrHTTP, got %T", err)
	}
	if httpErr.Status != http.StatusTooManyRequests {
		t.Errorf("expected status 429, got %d", httpErr.Status)
	}
}

func TestGenerateSync_EmptyPrompt(t *testing.T) {
	// ChatStream should reject an empty prompt before hitting the network.
	p := New("key", "qwen-image-2.0", "http://localhost")
	_, err := p.ChatStream(context.Background(), oasis.ChatRequest{
		Messages: []oasis.ChatMessage{{Role: "user", Content: ""}},
	}, nil)
	if err == nil {
		t.Fatal("expected error for empty prompt")
	}
}

// --- generateInterleaved tests ---

func TestGenerateInterleaved_Success(t *testing.T) {
	taskID := "task-abc-123"
	pollCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/services/aigc/image-generation/generation" && r.Method == http.MethodPost:
			if r.Header.Get("X-DashScope-Async") != "enable" {
				t.Errorf("expected X-DashScope-Async: enable, got %q", r.Header.Get("X-DashScope-Async"))
			}
			resp := map[string]any{
				"output": map[string]any{
					"task_id":     taskID,
					"task_status": "PENDING",
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)

		case r.URL.Path == "/tasks/"+taskID && r.Method == http.MethodGet:
			pollCount++
			if pollCount < 2 {
				// First poll: still running.
				resp := map[string]any{
					"output": map[string]any{"task_status": "RUNNING"},
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(resp)
			} else {
				// Second+ poll: succeeded.
				resp := map[string]any{
					"output": map[string]any{
						"task_status": "SUCCEEDED",
						"choices": []any{
							map[string]any{
								"message": map[string]any{
									"content": []any{
										map[string]any{"image": imageURL(r, "/fake-image.png")},
									},
								},
							},
						},
					},
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(resp)
			}

		case r.URL.Path == "/fake-image.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write(fakeImageBytes)

		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := New("test-key", "wan2.7-image", srv.URL, WithHTTPClient(srv.Client()))

	attachments, err := p.generateInterleaved(context.Background(), "a robot")
	if err != nil {
		t.Fatalf("generateInterleaved: %v", err)
	}
	if len(attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(attachments))
	}
	if len(attachments[0].Data) != len(fakeImageBytes) {
		t.Errorf("expected %d bytes, got %d", len(fakeImageBytes), len(attachments[0].Data))
	}
	if pollCount < 2 {
		t.Errorf("expected at least 2 polls, got %d", pollCount)
	}
}

func TestGenerateInterleaved_CreateError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"server error"}`))
	}))
	defer srv.Close()

	p := New("test-key", "wan2.7-image", srv.URL, WithHTTPClient(srv.Client()))

	_, err := p.generateInterleaved(context.Background(), "a robot")
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestGenerateInterleaved_APIErrorInBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"code":    "QuotaExceeded",
			"message": "daily quota exceeded",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := New("test-key", "wan2.7-image", srv.URL, WithHTTPClient(srv.Client()))

	_, err := p.generateInterleaved(context.Background(), "a robot")
	if err == nil {
		t.Fatal("expected error for API error code in body")
	}
	llmErr, ok := err.(*oasis.ErrLLM)
	if !ok {
		t.Fatalf("expected *oasis.ErrLLM, got %T: %v", err, err)
	}
	if llmErr.Provider != "dashscope" {
		t.Errorf("expected provider 'dashscope', got %q", llmErr.Provider)
	}
}

func TestGenerateInterleaved_NoTaskID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"output": map[string]any{
				"task_status": "PENDING",
				// deliberately omit task_id
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := New("test-key", "wan2.7-image", srv.URL, WithHTTPClient(srv.Client()))

	_, err := p.generateInterleaved(context.Background(), "a robot")
	if err == nil {
		t.Fatal("expected error when no task_id returned")
	}
}

// --- pollTask tests ---

func TestPollTask_TaskFailed(t *testing.T) {
	taskID := "task-fail-456"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"output": map[string]any{
				"task_status": "FAILED",
				"message":     "quota exceeded",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := New("test-key", "qwen-image-2.0", srv.URL, WithHTTPClient(srv.Client()))

	_, err := p.pollTask(context.Background(), taskID)
	if err == nil {
		t.Fatal("expected error for FAILED task")
	}
	llmErr, ok := err.(*oasis.ErrLLM)
	if !ok {
		t.Fatalf("expected *oasis.ErrLLM, got %T: %v", err, err)
	}
	if llmErr.Message != "quota exceeded" {
		t.Errorf("expected message 'quota exceeded', got %q", llmErr.Message)
	}
}

func TestPollTask_TaskCanceled(t *testing.T) {
	taskID := "task-cancel-789"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"output": map[string]any{"task_status": "CANCELED"},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := New("test-key", "qwen-image-2.0", srv.URL, WithHTTPClient(srv.Client()))

	_, err := p.pollTask(context.Background(), taskID)
	if err == nil {
		t.Fatal("expected error for CANCELED task")
	}
}

func TestPollTask_ContextCanceled(t *testing.T) {
	taskID := "task-ctx-999"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Would keep returning PENDING forever if context weren't canceled.
		resp := map[string]any{
			"output": map[string]any{"task_status": "PENDING"},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := New("test-key", "qwen-image-2.0", srv.URL, WithHTTPClient(srv.Client()))

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately so pollTask hits the ctx.Done() select branch on first iteration.
	cancel()

	_, err := p.pollTask(ctx, taskID)
	if err == nil {
		t.Fatal("expected error when context is canceled")
	}
}

func TestPollTask_HTTPError(t *testing.T) {
	taskID := "task-http-err"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":"service unavailable"}`))
	}))
	defer srv.Close()

	p := New("test-key", "qwen-image-2.0", srv.URL, WithHTTPClient(srv.Client()))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := p.pollTask(ctx, taskID)
	if err == nil {
		t.Fatal("expected error for 503 response")
	}
	httpErr, ok := err.(*oasis.ErrHTTP)
	if !ok {
		t.Fatalf("expected *oasis.ErrHTTP, got %T: %v", err, err)
	}
	if httpErr.Status != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", httpErr.Status)
	}
}

func TestPollTask_Success(t *testing.T) {
	taskID := "task-success-001"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tasks/" + taskID:
			resp := map[string]any{
				"output": map[string]any{
					"task_status": "SUCCEEDED",
					"choices": []any{
						map[string]any{
							"message": map[string]any{
								"content": []any{
									map[string]any{"image": imageURL(r, "/img.png")},
								},
							},
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		case "/img.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write(fakeImageBytes)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := New("test-key", "qwen-image-2.0", srv.URL, WithHTTPClient(srv.Client()))

	attachments, err := p.pollTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("pollTask: %v", err)
	}
	if len(attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(attachments))
	}
	if attachments[0].MimeType != "image/png" {
		t.Errorf("expected mime image/png, got %q", attachments[0].MimeType)
	}
}

// --- ChatStream integration tests ---

func TestChatStream_RoutesQwenToSync(t *testing.T) {
	hitGeneration := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/services/aigc/multimodal-generation/generation":
			hitGeneration = true
			resp := map[string]any{
				"output": map[string]any{
					"choices": []any{
						map[string]any{"message": map[string]any{
							"content": []any{map[string]any{"image": imageURL(r, "/img.png")}},
						}},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		case "/img.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write(fakeImageBytes)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := New("test-key", "qwen-image-2.0", srv.URL, WithHTTPClient(srv.Client()))

	resp, err := p.ChatStream(context.Background(), oasis.ChatRequest{
		Messages: []oasis.ChatMessage{oasis.UserMessage("a red fox")},
	}, nil)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if !hitGeneration {
		t.Error("expected synchronous generation endpoint to be hit")
	}
	if len(resp.Attachments) != 1 {
		t.Errorf("expected 1 attachment, got %d", len(resp.Attachments))
	}
	if resp.FinishReason != oasis.FinishStop {
		t.Errorf("expected FinishStop, got %q", resp.FinishReason)
	}
}

// --- video generation tests ---

const videoSynthPath = "/services/aigc/video-generation/video-synthesis"

// fakeVideoBytes is a non-zero payload returned by the mock video server.
var fakeVideoBytes = make([]byte, 4096)

func init() {
	for i := range fakeVideoBytes {
		fakeVideoBytes[i] = 0xAB
	}
}

func TestGenerateVideo_T2V(t *testing.T) {
	taskID := "vid-t2v-1"
	var createBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == videoSynthPath && r.Method == http.MethodPost:
			if r.Header.Get("X-DashScope-Async") != "enable" {
				t.Errorf("expected X-DashScope-Async: enable, got %q", r.Header.Get("X-DashScope-Async"))
			}
			if err := json.NewDecoder(r.Body).Decode(&createBody); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			resp := map[string]any{"output": map[string]any{"task_id": taskID, "task_status": "PENDING"}}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		case r.URL.Path == "/tasks/"+taskID && r.Method == http.MethodGet:
			resp := map[string]any{"output": map[string]any{
				"task_status": "SUCCEEDED",
				"video_url":   imageURL(r, "/fake-video.mp4"),
			}}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := New("test-key", "wan2.7-t2v", srv.URL, WithHTTPClient(srv.Client()))

	atts, err := p.generateVideo(context.Background(), "a galloping horse", nil)
	if err != nil {
		t.Fatalf("generateVideo: %v", err)
	}
	if len(atts) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(atts))
	}
	if atts[0].MimeType != "video/mp4" {
		t.Errorf("expected video/mp4, got %q", atts[0].MimeType)
	}
	if atts[0].URL == "" {
		t.Error("expected non-empty URL")
	}
	if len(atts[0].Data) != 0 {
		t.Errorf("expected no inline Data (URL default), got %d bytes", len(atts[0].Data))
	}

	input, _ := createBody["input"].(map[string]any)
	if input == nil {
		t.Fatalf("missing input in create body: %v", createBody)
	}
	if input["prompt"] != "a galloping horse" {
		t.Errorf("expected input.prompt set, got %v", input["prompt"])
	}
	if _, has := input["media"]; has {
		t.Errorf("t2v must not send media, got %v", input["media"])
	}

	params, _ := createBody["parameters"].(map[string]any)
	if params == nil {
		t.Fatalf("missing parameters in create body: %v", createBody)
	}
	if params["watermark"] != false {
		t.Errorf("expected parameters.watermark == false, got %v", params["watermark"])
	}
	if params["prompt_extend"] != true {
		t.Errorf("expected parameters.prompt_extend == true, got %v", params["prompt_extend"])
	}
}

func TestGenerateVideo_I2V_BuildsMedia(t *testing.T) {
	taskID := "vid-i2v-1"
	var createBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == videoSynthPath && r.Method == http.MethodPost:
			json.NewDecoder(r.Body).Decode(&createBody)
			resp := map[string]any{"output": map[string]any{"task_id": taskID, "task_status": "PENDING"}}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		case r.URL.Path == "/tasks/"+taskID:
			resp := map[string]any{"output": map[string]any{
				"task_status": "SUCCEEDED",
				"video_url":   imageURL(r, "/v.mp4"),
			}}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := New("test-key", "wan2.7-i2v", srv.URL, WithHTTPClient(srv.Client()))

	imgURL := "https://example.com/frame.png"
	_, err := p.generateVideo(context.Background(), "animate", []oasis.Attachment{
		{MimeType: "image/png", URL: imgURL},
	})
	if err != nil {
		t.Fatalf("generateVideo: %v", err)
	}

	media := mediaArray(t, createBody)
	if len(media) != 1 {
		t.Fatalf("expected 1 media entry, got %d: %v", len(media), media)
	}
	m0 := media[0].(map[string]any)
	if m0["type"] != "first_frame" {
		t.Errorf("expected type first_frame, got %v", m0["type"])
	}
	if m0["url"] != imgURL {
		t.Errorf("expected url %q, got %v", imgURL, m0["url"])
	}

	params, _ := createBody["parameters"].(map[string]any)
	if params == nil {
		t.Fatalf("missing parameters in create body: %v", createBody)
	}
	if params["watermark"] != false {
		t.Errorf("expected parameters.watermark == false, got %v", params["watermark"])
	}
	if params["prompt_extend"] != true {
		t.Errorf("expected parameters.prompt_extend == true, got %v", params["prompt_extend"])
	}
}

func TestGenerateVideo_I2V_NoImageErrors(t *testing.T) {
	p := New("test-key", "wan2.7-i2v", "http://localhost")
	_, err := p.generateVideo(context.Background(), "animate", nil)
	if err == nil {
		t.Fatal("expected error when i2v has no image attachment")
	}
	if _, ok := err.(*oasis.ErrLLM); !ok {
		t.Fatalf("expected *oasis.ErrLLM, got %T", err)
	}
}

func TestGenerateVideo_VideoEdit_BuildsMedia(t *testing.T) {
	taskID := "vid-edit-1"
	var createBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == videoSynthPath && r.Method == http.MethodPost:
			json.NewDecoder(r.Body).Decode(&createBody)
			resp := map[string]any{"output": map[string]any{"task_id": taskID, "task_status": "PENDING"}}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		case r.URL.Path == "/tasks/"+taskID:
			resp := map[string]any{"output": map[string]any{
				"task_status": "SUCCEEDED",
				"video_url":   imageURL(r, "/v.mp4"),
			}}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := New("test-key", "wan2.7-videoedit", srv.URL, WithHTTPClient(srv.Client()))

	vidURL := "https://example.com/in.mp4"
	imgURL := "https://example.com/ref.png"
	_, err := p.generateVideo(context.Background(), "edit it", []oasis.Attachment{
		{MimeType: "video/mp4", URL: vidURL},
		{MimeType: "image/png", URL: imgURL},
	})
	if err != nil {
		t.Fatalf("generateVideo: %v", err)
	}

	media := mediaArray(t, createBody)
	if len(media) != 2 {
		t.Fatalf("expected 2 media entries, got %d: %v", len(media), media)
	}
	m0 := media[0].(map[string]any)
	if m0["type"] != "video" || m0["url"] != vidURL {
		t.Errorf("expected video entry %q, got %v", vidURL, m0)
	}
	m1 := media[1].(map[string]any)
	if m1["type"] != "reference_image" || m1["url"] != imgURL {
		t.Errorf("expected reference_image entry %q, got %v", imgURL, m1)
	}

	params, _ := createBody["parameters"].(map[string]any)
	if params == nil {
		t.Fatalf("missing parameters in create body: %v", createBody)
	}
	if params["watermark"] != false {
		t.Errorf("expected parameters.watermark == false, got %v", params["watermark"])
	}
	if params["prompt_extend"] != true {
		t.Errorf("expected parameters.prompt_extend == true, got %v", params["prompt_extend"])
	}
}

func TestGenerateVideo_VideoEdit_NoVideoErrors(t *testing.T) {
	p := New("test-key", "wan2.7-videoedit", "http://localhost")
	_, err := p.generateVideo(context.Background(), "edit", []oasis.Attachment{
		{MimeType: "image/png", URL: "https://example.com/x.png"},
	})
	if err == nil {
		t.Fatal("expected error when videoedit has no video attachment")
	}
	if _, ok := err.(*oasis.ErrLLM); !ok {
		t.Fatalf("expected *oasis.ErrLLM, got %T", err)
	}
}

func TestAttachmentRef_DataURI(t *testing.T) {
	att := oasis.Attachment{MimeType: "image/png", Data: []byte{0x01, 0x02, 0x03}}
	ref := attachmentRef(att)
	const want = "data:image/png;base64,AQID"
	if ref != want {
		t.Errorf("expected %q, got %q", want, ref)
	}

	att2 := oasis.Attachment{MimeType: "image/png", URL: "https://example.com/a.png", Data: []byte{0x01}}
	if got := attachmentRef(att2); got != "https://example.com/a.png" {
		t.Errorf("expected URL preferred, got %q", got)
	}
}

func TestGenerateVideo_WithDownloadVideo(t *testing.T) {
	taskID := "vid-dl-1"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == videoSynthPath && r.Method == http.MethodPost:
			resp := map[string]any{"output": map[string]any{"task_id": taskID, "task_status": "PENDING"}}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		case r.URL.Path == "/tasks/"+taskID:
			resp := map[string]any{"output": map[string]any{
				"task_status": "SUCCEEDED",
				"video_url":   imageURL(r, "/out.mp4"),
			}}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		case r.URL.Path == "/out.mp4":
			w.Header().Set("Content-Type", "video/mp4")
			w.Write(fakeVideoBytes)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := New("test-key", "wan2.7-t2v", srv.URL, WithHTTPClient(srv.Client()), WithDownloadVideo())

	atts, err := p.generateVideo(context.Background(), "a horse", nil)
	if err != nil {
		t.Fatalf("generateVideo: %v", err)
	}
	if len(atts) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(atts))
	}
	if atts[0].MimeType != "video/mp4" {
		t.Errorf("expected video/mp4, got %q", atts[0].MimeType)
	}
	if len(atts[0].Data) != len(fakeVideoBytes) {
		t.Errorf("expected %d bytes inline, got %d", len(fakeVideoBytes), len(atts[0].Data))
	}
	if atts[0].URL != "" {
		t.Errorf("expected empty URL when downloaded, got %q", atts[0].URL)
	}
}

func TestChatStream_RoutesVideoModel(t *testing.T) {
	taskID := "route-vid"
	hitVideo := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == videoSynthPath:
			hitVideo = true
			resp := map[string]any{"output": map[string]any{"task_id": taskID, "task_status": "PENDING"}}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		case r.URL.Path == "/tasks/"+taskID:
			resp := map[string]any{"output": map[string]any{
				"task_status": "SUCCEEDED",
				"video_url":   imageURL(r, "/v.mp4"),
			}}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := New("test-key", "wan2.7-t2v", srv.URL, WithHTTPClient(srv.Client()))

	resp, err := p.ChatStream(context.Background(), oasis.ChatRequest{
		Messages: []oasis.ChatMessage{oasis.UserMessage("a galloping horse")},
	}, nil)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if !hitVideo {
		t.Error("expected video-synthesis endpoint to be hit")
	}
	if len(resp.Attachments) != 1 || resp.Attachments[0].MimeType != "video/mp4" {
		t.Errorf("expected one video attachment, got %v", resp.Attachments)
	}
}

func TestIsVideoModel(t *testing.T) {
	cases := map[string]bool{
		"wan2.7-t2v":       true,
		"wan2.7-i2v":       true,
		"wan2.7-videoedit": true,
		"WAN2.7-T2V":       true,
		"wan2.7-image":     false,
		"qwen-image-2.0":   false,
	}
	for model, want := range cases {
		if got := isVideoModel(model); got != want {
			t.Errorf("isVideoModel(%q) = %v, want %v", model, got, want)
		}
	}
}

// mediaArray extracts input.media from a decoded create body.
func mediaArray(t *testing.T, body map[string]any) []any {
	t.Helper()
	input, _ := body["input"].(map[string]any)
	if input == nil {
		t.Fatalf("missing input in create body: %v", body)
	}
	media, _ := input["media"].([]any)
	return media
}

func TestChatStream_ChannelClosedOnSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/services/aigc/multimodal-generation/generation":
			resp := map[string]any{
				"output": map[string]any{
					"choices": []any{
						map[string]any{"message": map[string]any{
							"content": []any{map[string]any{"image": imageURL(r, "/img.png")}},
						}},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		case "/img.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write(fakeImageBytes)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := New("test-key", "qwen-image-2.0", srv.URL, WithHTTPClient(srv.Client()))

	ch := make(chan oasis.StreamEvent, 10)
	_, err := p.ChatStream(context.Background(), oasis.ChatRequest{
		Messages: []oasis.ChatMessage{oasis.UserMessage("test")},
	}, ch)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	// Drain; channel must be closed (range must terminate, not block).
	for range ch {
	}
}
