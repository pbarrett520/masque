package anthropic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"masque/internal/provider"
)

func TestDescribeRequest(t *testing.T) {
	p := New("", "sk-ant-secret")
	desc, err := p.DescribeRequest(provider.ChatRequest{
		Model:    "claude-fable-5",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
		Params:   provider.SamplerParams{Temperature: floatPtr(0.7)},
	})
	if err != nil {
		t.Fatalf("DescribeRequest: %v", err)
	}
	if desc.URL != DefaultBaseURL+"/v1/messages" {
		t.Errorf("URL = %q", desc.URL)
	}
	body := string(desc.Body)
	if strings.Contains(body, "sk-ant-secret") {
		t.Errorf("body must never contain the API key: %s", body)
	}
	// Claude 5 drops manual sampling; the report records it.
	if !strings.Contains(strings.Join(desc.Report.Dropped, ","), "temperature") {
		t.Errorf("temperature should be dropped for claude-fable-5: %+v", desc.Report)
	}
}

func TestChatStreamNoStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") == "text/event-stream" {
			t.Error("non-streaming request must not ask for SSE")
		}
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"whole "},{"type":"text","text":"reply"}],` +
			`"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":2}}`))
	}))
	defer srv.Close()

	events, err := New(srv.URL, "k").ChatStream(context.Background(), provider.ChatRequest{
		Model:    "claude-fable-5",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
		NoStream: true,
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	var text strings.Builder
	var last provider.StreamEvent
	for ev := range events {
		text.WriteString(ev.Delta)
		last = ev
	}
	if text.String() != "whole reply" {
		t.Errorf("text = %q", text.String())
	}
	if !last.Done || last.Usage == nil || last.Usage.PromptTokens != 5 {
		t.Errorf("terminal = %+v", last)
	}
}

func TestChatStreamNoStreamRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"content":[],"stop_reason":"refusal","usage":{"input_tokens":5,"output_tokens":0}}`))
	}))
	defer srv.Close()

	events, err := New(srv.URL, "k").ChatStream(context.Background(), provider.ChatRequest{
		Model: "m", NoStream: true,
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	var last provider.StreamEvent
	for ev := range events {
		last = ev
	}
	if last.Err == nil || !strings.Contains(last.Err.Error(), "refusal") {
		t.Errorf("terminal = %+v", last)
	}
}
