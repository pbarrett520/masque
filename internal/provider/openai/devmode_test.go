package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"masque/internal/provider"
)

func TestDescribeRequest(t *testing.T) {
	p := New("http://example/v1", "secret-key")
	desc, err := p.DescribeRequest(provider.ChatRequest{
		Model:    "m",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
		Params:   provider.SamplerParams{TopK: intPtr(40)},
	})
	if err != nil {
		t.Fatalf("DescribeRequest: %v", err)
	}
	if desc.URL != "http://example/v1/chat/completions" {
		t.Errorf("URL = %q", desc.URL)
	}
	body := string(desc.Body)
	if !strings.Contains(body, `"stream":true`) || !strings.Contains(body, `"stream_options"`) {
		t.Errorf("streaming body wrong: %s", body)
	}
	if strings.Contains(body, "secret-key") {
		t.Errorf("body must never contain the API key: %s", body)
	}
	if len(desc.Report.Dropped) != 1 || desc.Report.Dropped[0] != "top_k" {
		t.Errorf("report = %+v", desc.Report)
	}
}

func TestDescribeRequestNoStream(t *testing.T) {
	p := New("", "")
	desc, err := p.DescribeRequest(provider.ChatRequest{
		Model:    "m",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
		NoStream: true,
	})
	if err != nil {
		t.Fatalf("DescribeRequest: %v", err)
	}
	body := string(desc.Body)
	if !strings.Contains(body, `"stream":false`) || strings.Contains(body, "stream_options") {
		t.Errorf("non-streaming body wrong: %s", body)
	}
}

func TestChatStreamNoStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") == "text/event-stream" {
			t.Error("non-streaming request must not ask for SSE")
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"whole reply"},"finish_reason":"stop"}],` +
			`"usage":{"prompt_tokens":7,"completion_tokens":3}}`))
	}))
	defer srv.Close()

	events, err := New(srv.URL, "").ChatStream(context.Background(), provider.ChatRequest{
		Model:    "m",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
		NoStream: true,
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	var deltas []string
	var last provider.StreamEvent
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				if strings.Join(deltas, "") != "whole reply" {
					t.Errorf("deltas = %q", deltas)
				}
				if !last.Done || last.Usage == nil || last.Usage.CompletionTokens != 3 {
					t.Errorf("terminal event = %+v", last)
				}
				return
			}
			if ev.Delta != "" {
				deltas = append(deltas, ev.Delta)
			}
			last = ev
		case <-timeout:
			t.Fatal("timed out")
		}
	}
}

func TestChatStreamNoStreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"error":{"message":"model overloaded"}}`))
	}))
	defer srv.Close()

	events, err := New(srv.URL, "").ChatStream(context.Background(), provider.ChatRequest{
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
	if last.Err == nil || !strings.Contains(last.Err.Error(), "overloaded") {
		t.Errorf("terminal = %+v", last)
	}
}
