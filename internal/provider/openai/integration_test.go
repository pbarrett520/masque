//go:build openai_integration

// Manual integration tests against a live OpenAI-compatible endpoint.
// By default this targets Ollama's compat layer, so they run keyless on
// a machine with local Ollama:
//
//	go test -tags openai_integration ./internal/provider/openai/ -v
//
// Point MASQUE_OPENAI_URL / MASQUE_OPENAI_KEY / MASQUE_OPENAI_MODEL at
// OpenRouter or another server to exercise a real cloud endpoint.
package openai

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"masque/internal/provider"
)

func liveProvider(t *testing.T) *Provider {
	t.Helper()
	url := os.Getenv("MASQUE_OPENAI_URL")
	if url == "" {
		url = "http://localhost:11434/v1"
	}
	p := New(url, os.Getenv("MASQUE_OPENAI_KEY"))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := p.HealthCheck(ctx); err != nil {
		t.Skipf("no live endpoint: %v", err)
	}
	return p
}

func liveModel(t *testing.T, p *Provider) string {
	t.Helper()
	if m := os.Getenv("MASQUE_OPENAI_MODEL"); m != "" {
		return m
	}
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	// The compat /models list has no capability metadata, so skip the
	// obvious embedding models when picking a default.
	for _, m := range models {
		if !strings.Contains(m.ID, "embed") {
			return m.ID
		}
	}
	t.Skip("no usable model at live endpoint")
	return ""
}

func TestLiveChatStream(t *testing.T) {
	p := liveProvider(t)
	model := liveModel(t, p)
	t.Logf("using model %s", model)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	events, err := p.ChatStream(ctx, provider.ChatRequest{
		Model: model,
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: "Reply with the single word: hello"},
		},
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	var sb strings.Builder
	var done bool
	var usage *provider.Usage
	for ev := range events {
		if ev.Err != nil {
			t.Fatalf("stream error: %v", ev.Err)
		}
		sb.WriteString(ev.Delta)
		if ev.Done {
			done = true
			usage = ev.Usage
		}
	}
	if !done {
		t.Fatal("stream closed without done")
	}
	if sb.Len() == 0 {
		t.Fatal("empty completion")
	}
	t.Logf("completion: %q, usage: %+v", sb.String(), usage)
}
