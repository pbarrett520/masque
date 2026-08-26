package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"masque/internal/provider"
)

func TestDescribeRequest(t *testing.T) {
	p := New("http://localhost:11434")
	desc, err := p.DescribeRequest(provider.ChatRequest{
		Model:    "m",
		System:   "sys",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
		Params:   provider.SamplerParams{Temperature: floatPtr(0.8)},
	})
	if err != nil {
		t.Fatalf("DescribeRequest: %v", err)
	}
	if desc.URL != "http://localhost:11434/api/chat" {
		t.Errorf("URL = %q", desc.URL)
	}
	body := string(desc.Body)
	if !strings.Contains(body, `"stream":true`) || !strings.Contains(body, `"temperature":0.8`) {
		t.Errorf("body = %s", body)
	}
	if len(desc.Report.Sent) != 1 {
		t.Errorf("report = %+v", desc.Report)
	}
}

func TestChatStreamNoStream(t *testing.T) {
	// stream:false makes /api/chat answer with a single JSON object; the
	// NDJSON reader handles it as a one-line stream.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got struct {
			Stream bool `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		if got.Stream {
			t.Error("expected stream:false in request")
		}
		_, _ = w.Write([]byte(`{"message":{"content":"whole reply"},"done":true,"prompt_eval_count":4,"eval_count":2}`))
	}))
	defer srv.Close()

	events, err := New(srv.URL).ChatStream(context.Background(), provider.ChatRequest{
		Model:    "m",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
		NoStream: true,
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	var last provider.StreamEvent
	var text strings.Builder
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				if text.String() != "whole reply" {
					t.Errorf("text = %q", text.String())
				}
				if !last.Done || last.Usage == nil || last.Usage.PromptTokens != 4 {
					t.Errorf("terminal = %+v", last)
				}
				return
			}
			text.WriteString(ev.Delta)
			last = ev
		case <-timeout:
			t.Fatal("timed out")
		}
	}
}

func TestListAllModelsIncludesNonChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"models":[` +
			`{"name":"chatty","capabilities":["completion"]},` +
			`{"name":"embedder","capabilities":["embedding"]}]}`))
	}))
	defer srv.Close()

	p := New(srv.URL)
	chat, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(chat) != 1 || chat[0].ID != "chatty" {
		t.Errorf("ListModels should filter: %+v", chat)
	}
	all, err := p.ListAllModels(context.Background())
	if err != nil {
		t.Fatalf("ListAllModels: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("ListAllModels should include everything: %+v", all)
	}
}
