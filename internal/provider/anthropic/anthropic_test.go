package anthropic

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

func fullParams() provider.SamplerParams {
	return provider.SamplerParams{
		Temperature:   floatPtr(0.7),
		TopP:          floatPtr(0.9),
		TopK:          intPtr(40),
		MinP:          floatPtr(0.05),
		RepeatPenalty: floatPtr(1.1),
		MaxTokens:     intPtr(512),
		Stop:          []string{"\nUser:"},
	}
}

func TestBuildChatBodyGolden(t *testing.T) {
	req := provider.ChatRequest{
		Model:  "claude-sonnet-4-6",
		System: "You are Nyx.",
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: "hi"},
		},
		Params: fullParams(),
	}
	body, report := buildChatBody(req)
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"model":"claude-sonnet-4-6",` +
		`"max_tokens":512,` +
		`"system":"You are Nyx.",` +
		`"messages":[{"role":"user","content":"hi"}],` +
		`"stream":true,` +
		`"temperature":0.7,` +
		`"top_p":0.9,` +
		`"top_k":40,` +
		`"stop_sequences":["\nUser:"]}`
	if string(raw) != want {
		t.Errorf("request body mismatch\n got: %s\nwant: %s", raw, want)
	}
	wantDropped := []string{"min_p", "repeat_penalty"}
	if len(report.Dropped) != 2 || report.Dropped[0] != wantDropped[0] || report.Dropped[1] != wantDropped[1] {
		t.Errorf("dropped = %v, want %v", report.Dropped, wantDropped)
	}
}

func TestBuildChatBodySamplingRemovedModels(t *testing.T) {
	req := provider.ChatRequest{
		Model:    "claude-opus-5",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
		Params:   fullParams(),
	}
	body, report := buildChatBody(req)
	if body.Temperature != nil || body.TopP != nil || body.TopK != nil {
		t.Errorf("sampling params must be dropped on claude-opus-5: %+v", body)
	}
	if body.MaxTokens != 512 || len(body.StopSequences) != 1 {
		t.Errorf("max_tokens/stop should still be sent: %+v", body)
	}
	for _, name := range []string{"temperature", "top_p", "top_k", "min_p", "repeat_penalty"} {
		found := false
		for _, d := range report.Dropped {
			if d == name {
				found = true
			}
		}
		if !found {
			t.Errorf("%s missing from dropped list %v", name, report.Dropped)
		}
	}
}

func TestBuildChatBodyDefaultMaxTokens(t *testing.T) {
	body, _ := buildChatBody(provider.ChatRequest{
		Model:    "m",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	})
	if body.MaxTokens != defaultMaxTokens {
		t.Errorf("max_tokens = %d, want required-field default %d", body.MaxTokens, defaultMaxTokens)
	}
}

func TestNormalizeMessages(t *testing.T) {
	// RP history: greeting first (assistant), then alternation.
	in := []provider.Message{
		{Role: provider.RoleAssistant, Content: "greeting"},
		{Role: provider.RoleUser, Content: "hi"},
	}
	out := normalizeMessages(in)
	if len(out) != 3 || out[0].Role != provider.RoleUser {
		t.Fatalf("greeting-first not fixed: %+v", out)
	}
	if out[1].Content != "greeting" || out[2].Content != "hi" {
		t.Errorf("history reordered: %+v", out)
	}

	// Consecutive same-role messages are merged.
	in = []provider.Message{
		{Role: provider.RoleUser, Content: "one"},
		{Role: provider.RoleUser, Content: "two"},
		{Role: provider.RoleAssistant, Content: "reply"},
	}
	out = normalizeMessages(in)
	if len(out) != 2 || out[0].Content != "one\n\ntwo" {
		t.Errorf("consecutive roles not merged: %+v", out)
	}

	if got := normalizeMessages(nil); len(got) != 0 {
		t.Errorf("empty history mishandled: %+v", got)
	}
}

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

// sseServer streams raw SSE lines (already framed with event:/data:).
func sseServer(t *testing.T, check func(r *http.Request), lines ...string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if check != nil {
			check(r)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, l := range lines {
			if _, err := io.WriteString(w, l+"\n"); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	}))
}

func happyPathLines() []string {
	return []string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":12}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`event: ping`,
		`data: {"type":"ping"}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo!"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":34}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
	}
}

func TestChatStreamHappyPath(t *testing.T) {
	var gotPath, gotKey, gotVersion string
	srv := sseServer(t, func(r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
	}, happyPathLines()...)
	defer srv.Close()

	events, err := New(srv.URL, "sk-ant-test").ChatStream(context.Background(), provider.ChatRequest{
		Model:    "claude-sonnet-4-6",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	deltas, terminal := collect(t, events)

	if gotPath != "/v1/messages" {
		t.Errorf("posted to %q, want /v1/messages", gotPath)
	}
	if gotKey != "sk-ant-test" || gotVersion != apiVersion {
		t.Errorf("headers: key=%q version=%q", gotKey, gotVersion)
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

func TestChatStreamIgnoresThinkingDeltas(t *testing.T) {
	srv := sseServer(t, nil,
		`data: {"type":"message_start","message":{"usage":{"input_tokens":1}}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hmm"}}`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Answer."}}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
		`data: {"type":"message_stop"}`,
	)
	defer srv.Close()

	events, err := New(srv.URL, "k").ChatStream(context.Background(), provider.ChatRequest{Model: "m"})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	deltas, terminal := collect(t, events)
	if got := strings.Join(deltas, ""); got != "Answer." {
		t.Errorf("assembled %q, want thinking excluded", got)
	}
	if !terminal.Done {
		t.Errorf("terminal = %+v", terminal)
	}
}

func TestChatStreamRefusal(t *testing.T) {
	srv := sseServer(t, nil,
		`data: {"type":"message_start","message":{"usage":{"input_tokens":1}}}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"refusal"},"usage":{"output_tokens":0}}`,
		`data: {"type":"message_stop"}`,
	)
	defer srv.Close()

	events, err := New(srv.URL, "k").ChatStream(context.Background(), provider.ChatRequest{Model: "m"})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	_, terminal := collect(t, events)
	if terminal.Err == nil || !strings.Contains(terminal.Err.Error(), "refusal") {
		t.Errorf("terminal = %+v, want refusal error", terminal)
	}
}

func TestChatStreamMidStreamError(t *testing.T) {
	srv := sseServer(t, nil,
		`data: {"type":"message_start","message":{"usage":{"input_tokens":1}}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"par"}}`,
		`event: error`,
		`data: {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`,
	)
	defer srv.Close()

	events, err := New(srv.URL, "k").ChatStream(context.Background(), provider.ChatRequest{Model: "m"})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	deltas, terminal := collect(t, events)
	if got := strings.Join(deltas, ""); got != "par" {
		t.Errorf("assembled %q, want partial", got)
	}
	if terminal.Err == nil || !strings.Contains(terminal.Err.Error(), "Overloaded") {
		t.Errorf("terminal err = %v", terminal.Err)
	}
}

func TestChatStreamTruncated(t *testing.T) {
	srv := sseServer(t, nil,
		`data: {"type":"message_start","message":{"usage":{"input_tokens":1}}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"par"}}`,
	)
	defer srv.Close()

	events, err := New(srv.URL, "k").ChatStream(context.Background(), provider.ChatRequest{Model: "m"})
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
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"},"request_id":"req_1"}`)
	}))
	defer srv.Close()

	_, err := New(srv.URL, "bad").ChatStream(context.Background(), provider.ChatRequest{Model: "m"})
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if !strings.Contains(err.Error(), "invalid x-api-key") || !strings.Contains(err.Error(), "401") {
		t.Errorf("err = %v", err)
	}
}

func TestChatStreamCancel(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"one"}}`+"\n")
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
	events, err := New(srv.URL, "k").ChatStream(ctx, provider.ChatRequest{Model: "m"})
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
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %q, want /v1/models", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "k" || r.Header.Get("anthropic-version") != apiVersion {
			t.Errorf("missing auth headers")
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"claude-opus-5","display_name":"Claude Opus 5"},{"id":"claude-sonnet-4-6","display_name":"Claude Sonnet 4.6"}],"has_more":false}`)
	}))
	defer srv.Close()

	models, err := New(srv.URL, "k").ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 || models[0].ID != "claude-opus-5" {
		t.Errorf("models = %+v", models)
	}
}

func TestHealthCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"missing key"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":[],"has_more":false}`)
	}))
	defer srv.Close()

	if err := New(srv.URL, "k").HealthCheck(context.Background()); err != nil {
		t.Errorf("HealthCheck with key: %v", err)
	}
	err := New(srv.URL, "").HealthCheck(context.Background())
	if err == nil || !strings.Contains(err.Error(), "missing key") {
		t.Errorf("HealthCheck without key = %v", err)
	}
}
