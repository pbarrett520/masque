package openai

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
		Model:  "gpt-x",
		System: "You are Nyx.",
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: "hi"},
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
	want := `{"model":"gpt-x",` +
		`"messages":[` +
		`{"role":"system","content":"You are Nyx."},` +
		`{"role":"user","content":"hi"}],` +
		`"stream":true,` +
		`"stream_options":{"include_usage":true},` +
		`"temperature":0.7,` +
		`"top_p":0.9,` +
		`"max_tokens":512,` +
		`"stop":["\nUser:"]}`
	if string(raw) != want {
		t.Errorf("request body mismatch\n got: %s\nwant: %s", raw, want)
	}
	wantDropped := []string{"top_k", "min_p", "repeat_penalty"}
	if len(report.Dropped) != len(wantDropped) {
		t.Fatalf("dropped = %v, want %v", report.Dropped, wantDropped)
	}
	for i, d := range wantDropped {
		if report.Dropped[i] != d {
			t.Errorf("dropped[%d] = %q, want %q", i, report.Dropped[i], d)
		}
	}
	if len(report.Sent) != 4 {
		t.Errorf("sent = %v, want temperature/top_p/max_tokens/stop", report.Sent)
	}
}

func TestBuildChatBodyMinimal(t *testing.T) {
	body, report := buildChatBody(provider.ChatRequest{
		Model:    "m",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	})
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"model":"m","messages":[{"role":"user","content":"hi"}],` +
		`"stream":true,"stream_options":{"include_usage":true}}`
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

// sseServer streams the given lines (already "data: ..." framed).
func sseServer(t *testing.T, check func(r *http.Request), lines ...string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if check != nil {
			check(r)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, l := range lines {
			if _, err := io.WriteString(w, l+"\n\n"); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	}))
}

func TestChatStreamHappyPath(t *testing.T) {
	var gotPath, gotAuth, gotBody string
	srv := sseServer(t, func(r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
	},
		`data: {"choices":[{"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{"content":"Hel"},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{"content":"lo!"},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: {"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":34}}`,
		`data: [DONE]`,
	)
	defer srv.Close()

	p := New(srv.URL, "sk-test")
	events, err := p.ChatStream(context.Background(), provider.ChatRequest{
		Model:    "m",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	deltas, terminal := collect(t, events)

	if gotPath != "/chat/completions" {
		t.Errorf("posted to %q, want /chat/completions", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("auth header = %q", gotAuth)
	}
	if !strings.Contains(gotBody, `"stream":true`) {
		t.Errorf("request body missing stream flag: %s", gotBody)
	}
	if got := strings.Join(deltas, ""); got != "Hello!" {
		t.Errorf("assembled %q, want %q", got, "Hello!")
	}
	if !terminal.Done || terminal.Err != nil {
		t.Errorf("terminal = %+v, want Done", terminal)
	}
	if terminal.Usage == nil || terminal.Usage.PromptTokens != 12 || terminal.Usage.CompletionTokens != 34 {
		t.Errorf("usage = %+v, want 12/34", terminal.Usage)
	}
}

func TestChatStreamIgnoresReasoningDeltas(t *testing.T) {
	// Ollama's /v1 layer streams thinking as reasoning deltas with empty
	// content; they must not surface as chat text.
	srv := sseServer(t, nil,
		`data: {"choices":[{"delta":{"role":"assistant","content":"","reasoning":"Let me think"},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{"content":"Answer."},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	)
	defer srv.Close()

	events, err := New(srv.URL, "").ChatStream(context.Background(), provider.ChatRequest{Model: "m"})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	deltas, terminal := collect(t, events)
	if got := strings.Join(deltas, ""); got != "Answer." {
		t.Errorf("assembled %q, want reasoning excluded", got)
	}
	if !terminal.Done {
		t.Errorf("terminal = %+v, want Done", terminal)
	}
}

func TestChatStreamEOFAfterFinishIsDone(t *testing.T) {
	// Some servers close without a [DONE] sentinel.
	srv := sseServer(t, nil,
		`data: {"choices":[{"delta":{"content":"hi"},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2}}`,
	)
	defer srv.Close()

	events, err := New(srv.URL, "").ChatStream(context.Background(), provider.ChatRequest{Model: "m"})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	_, terminal := collect(t, events)
	if !terminal.Done || terminal.Err != nil {
		t.Errorf("terminal = %+v, want Done on EOF after finish_reason", terminal)
	}
	if terminal.Usage == nil || terminal.Usage.CompletionTokens != 2 {
		t.Errorf("usage = %+v", terminal.Usage)
	}
}

func TestChatStreamTruncated(t *testing.T) {
	srv := sseServer(t, nil,
		`data: {"choices":[{"delta":{"content":"par"},"finish_reason":null}]}`,
	)
	defer srv.Close()

	events, err := New(srv.URL, "").ChatStream(context.Background(), provider.ChatRequest{Model: "m"})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	_, terminal := collect(t, events)
	if !errors.Is(terminal.Err, io.ErrUnexpectedEOF) {
		t.Errorf("terminal err = %v, want ErrUnexpectedEOF", terminal.Err)
	}
}

func TestChatStreamMidStreamError(t *testing.T) {
	srv := sseServer(t, nil,
		`data: {"choices":[{"delta":{"content":"par"},"finish_reason":null}]}`,
		`data: {"error":{"message":"rate limited","code":429}}`,
	)
	defer srv.Close()

	events, err := New(srv.URL, "").ChatStream(context.Background(), provider.ChatRequest{Model: "m"})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	deltas, terminal := collect(t, events)
	if got := strings.Join(deltas, ""); got != "par" {
		t.Errorf("assembled %q, want partial", got)
	}
	if terminal.Err == nil || !strings.Contains(terminal.Err.Error(), "rate limited") {
		t.Errorf("terminal err = %v", terminal.Err)
	}
}

func TestChatStreamHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"Incorrect API key provided","type":"invalid_request_error"}}`)
	}))
	defer srv.Close()

	_, err := New(srv.URL, "bad").ChatStream(context.Background(), provider.ChatRequest{Model: "m"})
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if !strings.Contains(err.Error(), "Incorrect API key") || !strings.Contains(err.Error(), "401") {
		t.Errorf("err = %v, want status and message", err)
	}
}

func TestChatStreamCancel(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"content":"one"},"finish_reason":null}]}`+"\n\n")
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
	events, err := New(srv.URL, "").ChatStream(ctx, provider.ChatRequest{Model: "m"})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	first := <-events
	if first.Delta != "one" {
		t.Fatalf("first event = %+v", first)
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
		if r.URL.Path != "/models" {
			t.Errorf("path = %q, want /models", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("auth header sent without key: %q", got)
		}
		_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"model-a"},{"id":"model-b"}]}`)
	}))
	defer srv.Close()

	models, err := New(srv.URL, "").ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 || models[0].ID != "model-a" || models[1].ID != "model-b" {
		t.Errorf("models = %+v", models)
	}
}

func TestHealthCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":{"message":"no key"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"object":"list","data":[]}`)
	}))
	defer srv.Close()

	if err := New(srv.URL, "k").HealthCheck(context.Background()); err != nil {
		t.Errorf("HealthCheck with key: %v", err)
	}
	err := New(srv.URL, "").HealthCheck(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no key") {
		t.Errorf("HealthCheck without key = %v, want auth error", err)
	}
}

func TestNewDefaultsAndTrailingSlash(t *testing.T) {
	if got := New("", "").baseURL; got != DefaultBaseURL {
		t.Errorf("default base URL = %q", got)
	}
	if got := New("http://host:1234/v1/", "").baseURL; got != "http://host:1234/v1" {
		t.Errorf("base URL = %q, want trailing slash trimmed", got)
	}
}
