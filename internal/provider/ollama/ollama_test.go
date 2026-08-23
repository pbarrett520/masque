package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"masque/internal/provider"
)

func floatPtr(v float64) *float64 { return &v }
func intPtr(v int) *int           { return &v }

func TestBuildChatBodyGolden(t *testing.T) {
	req := provider.ChatRequest{
		Model:  "llama3:8b",
		System: "You are Nyx.",
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: "hi"},
			{Role: provider.RoleAssistant, Content: "hello"},
		},
		Params: provider.SamplerParams{
			Temperature:   floatPtr(0.7),
			TopP:          floatPtr(0.9),
			TopK:          intPtr(40),
			MinP:          floatPtr(0.05),
			RepeatPenalty: floatPtr(1.1),
			MaxTokens:     intPtr(512),
			Stop:          []string{"\nUser:"},
		},
	}
	body, report := buildChatBody(req)
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"model":"llama3:8b",` +
		`"messages":[` +
		`{"role":"system","content":"You are Nyx."},` +
		`{"role":"user","content":"hi"},` +
		`{"role":"assistant","content":"hello"}],` +
		`"stream":true,` +
		`"options":{"min_p":0.05,"num_predict":512,"repeat_penalty":1.1,"stop":["\nUser:"],"temperature":0.7,"top_k":40,"top_p":0.9}}`
	if string(raw) != want {
		t.Errorf("request body mismatch\n got: %s\nwant: %s", raw, want)
	}
	if len(report.Dropped) != 0 {
		t.Errorf("ollama should drop no params, dropped %v", report.Dropped)
	}
	if len(report.Sent) != 7 {
		t.Errorf("expected 7 sent params, got %d: %v", len(report.Sent), report.Sent)
	}
}

func TestBuildChatBodyMinimal(t *testing.T) {
	req := provider.ChatRequest{
		Model:    "m",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	}
	body, report := buildChatBody(req)
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":true}`
	if string(raw) != want {
		t.Errorf("request body mismatch\n got: %s\nwant: %s", raw, want)
	}
	if len(report.Sent) != 0 || len(report.Dropped) != 0 {
		t.Errorf("expected empty report, got %+v", report)
	}
}

// collect drains a stream into deltas and the terminal event.
func collect(t *testing.T, events <-chan provider.StreamEvent) (deltas []string, terminal provider.StreamEvent) {
	t.Helper()
	sawTerminal := false
	for ev := range events {
		if sawTerminal {
			t.Fatalf("event after terminal: %+v", ev)
		}
		if ev.Delta != "" {
			deltas = append(deltas, ev.Delta)
		}
		if ev.Done || ev.Err != nil {
			terminal = ev
			sawTerminal = true
		}
	}
	if !sawTerminal {
		t.Fatal("stream closed without a terminal event")
	}
	return deltas, terminal
}

func TestChatStreamHappyPath(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		lines := []string{
			`{"message":{"role":"assistant","content":"Hel"},"done":false}`,
			`{"message":{"role":"assistant","content":"lo!"},"done":false}`,
			`{"message":{"role":"assistant","content":""},"done":true,"done_reason":"stop","prompt_eval_count":12,"eval_count":34}`,
		}
		for _, l := range lines {
			if _, err := io.WriteString(w, l+"\n"); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	}))
	defer srv.Close()

	p := New(srv.URL)
	events, err := p.ChatStream(context.Background(), provider.ChatRequest{
		Model:    "m",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	deltas, terminal := collect(t, events)

	if gotPath != "/api/chat" {
		t.Errorf("posted to %q, want /api/chat", gotPath)
	}
	if !strings.Contains(gotBody, `"stream":true`) {
		t.Errorf("request body missing stream flag: %s", gotBody)
	}
	if got := strings.Join(deltas, ""); got != "Hello!" {
		t.Errorf("assembled %q, want %q", got, "Hello!")
	}
	if !terminal.Done || terminal.Err != nil {
		t.Errorf("terminal event = %+v, want Done", terminal)
	}
	if terminal.Usage == nil || terminal.Usage.PromptTokens != 12 || terminal.Usage.CompletionTokens != 34 {
		t.Errorf("usage = %+v, want 12/34", terminal.Usage)
	}
}

func TestChatStreamFinalDelta(t *testing.T) {
	// Some backends put trailing text on the done line; it must not be lost.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"message":{"role":"assistant","content":"tail"},"done":true}`+"\n")
	}))
	defer srv.Close()

	events, err := New(srv.URL).ChatStream(context.Background(), provider.ChatRequest{Model: "m"})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	deltas, terminal := collect(t, events)
	if got := strings.Join(deltas, ""); got != "tail" {
		t.Errorf("assembled %q, want %q", got, "tail")
	}
	if !terminal.Done {
		t.Errorf("terminal event = %+v, want Done", terminal)
	}
	if terminal.Usage != nil {
		t.Errorf("usage = %+v, want nil when backend reports none", terminal.Usage)
	}
}

func TestChatStreamMidStreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"message":{"role":"assistant","content":"par"},"done":false}`+"\n")
		_, _ = io.WriteString(w, `{"error":"model crashed"}`+"\n")
	}))
	defer srv.Close()

	events, err := New(srv.URL).ChatStream(context.Background(), provider.ChatRequest{Model: "m"})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	deltas, terminal := collect(t, events)
	if got := strings.Join(deltas, ""); got != "par" {
		t.Errorf("assembled %q, want partial %q", got, "par")
	}
	if terminal.Err == nil || !strings.Contains(terminal.Err.Error(), "model crashed") {
		t.Errorf("terminal err = %v, want ollama error", terminal.Err)
	}
}

func TestChatStreamTruncated(t *testing.T) {
	// Stream ends without a done marker: must surface as an error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"message":{"role":"assistant","content":"par"},"done":false}`+"\n")
	}))
	defer srv.Close()

	events, err := New(srv.URL).ChatStream(context.Background(), provider.ChatRequest{Model: "m"})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	_, terminal := collect(t, events)
	if !errors.Is(terminal.Err, io.ErrUnexpectedEOF) {
		t.Errorf("terminal err = %v, want ErrUnexpectedEOF", terminal.Err)
	}
}

func TestChatStreamHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"model \"nope\" not found"}`)
	}))
	defer srv.Close()

	_, err := New(srv.URL).ChatStream(context.Background(), provider.ChatRequest{Model: "nope"})
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if !strings.Contains(err.Error(), "not found") || !strings.Contains(err.Error(), "404") {
		t.Errorf("err = %v, want status and ollama message", err)
	}
}

func TestChatStreamCancel(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"message":{"role":"assistant","content":"one"},"done":false}`+"\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := New(srv.URL).ChatStream(ctx, provider.ChatRequest{Model: "m"})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	// Read the first delta, then cancel mid-stream and drain.
	first := <-events
	if first.Delta != "one" {
		t.Fatalf("first event = %+v, want delta %q", first, "one")
	}
	cancel()
	var terminal provider.StreamEvent
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				if !errors.Is(terminal.Err, context.Canceled) {
					t.Errorf("terminal err = %v, want context.Canceled", terminal.Err)
				}
				return
			}
			terminal = ev
		case <-deadline:
			t.Fatal("stream did not terminate after cancel")
		}
	}
}

func TestListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("path = %q, want /api/tags", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"models":[
			{"name":"llama3:8b","size":4661224676,"modified_at":"2026-08-01T10:00:00Z",
			 "capabilities":["completion","tools"],
			 "details":{"family":"llama","quantization_level":"Q4_0"}},
			{"name":"nomic-embed-text:latest","size":274302450,"modified_at":"2026-06-07T20:00:00Z",
			 "capabilities":["embedding"],
			 "details":{"family":"nomic-bert","quantization_level":"F16"}},
			{"name":"mistral:7b","size":4113301824,"modified_at":"2026-07-15T09:00:00Z",
			 "details":{"family":"mistral","quantization_level":"Q4_K_M"}}]}`)
	}))
	defer srv.Close()

	models, err := New(srv.URL).ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	// The embedding-only model is filtered; the capability-less one
	// (older Ollama) is kept.
	if len(models) != 2 {
		t.Fatalf("got %d models (%+v), want 2", len(models), models)
	}
	want := provider.ModelInfo{
		ID: "llama3:8b", Size: 4661224676, Family: "llama",
		Quant: "Q4_0", ModifiedAt: "2026-08-01T10:00:00Z",
	}
	if models[0] != want {
		t.Errorf("models[0] = %+v, want %+v", models[0], want)
	}
	if models[1].ID != "mistral:7b" {
		t.Errorf("models[1] = %+v, want mistral:7b", models[1])
	}
}

func TestHealthCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/version" {
			t.Errorf("path = %q, want /api/version", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"version":"0.11.0"}`)
	}))
	if err := New(srv.URL).HealthCheck(context.Background()); err != nil {
		t.Errorf("HealthCheck against live server: %v", err)
	}
	srv.Close()
	if err := New(srv.URL).HealthCheck(context.Background()); err == nil {
		t.Error("HealthCheck against closed server: want error")
	}
}

func TestContextWindow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/show" {
			t.Errorf("path = %q, want /api/show", r.URL.Path)
		}
		var req map[string]string
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req["model"] != "llama3:8b" {
			t.Errorf("show request = %v (%v), want model llama3:8b", req, err)
		}
		_, _ = io.WriteString(w, `{"model_info":{"general.architecture":"llama","llama.context_length":8192,"llama.embedding_length":4096}}`)
	}))
	defer srv.Close()

	n, err := New(srv.URL).ContextWindow(context.Background(), "llama3:8b")
	if err != nil {
		t.Fatalf("ContextWindow: %v", err)
	}
	if n != 8192 {
		t.Errorf("context window = %d, want 8192", n)
	}
}

func TestContextWindowMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"model_info":{}}`)
	}))
	defer srv.Close()

	if _, err := New(srv.URL).ContextWindow(context.Background(), "m"); err == nil {
		t.Error("want error when model_info lacks context_length")
	}
}

func TestNewDefaultsAndTrailingSlash(t *testing.T) {
	if got := New("").baseURL; got != DefaultBaseURL {
		t.Errorf("default base URL = %q, want %q", got, DefaultBaseURL)
	}
	if got := New("http://host:1234/").baseURL; got != "http://host:1234" {
		t.Errorf("base URL = %q, want trailing slash trimmed", got)
	}
}
