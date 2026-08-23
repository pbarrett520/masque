//go:build ollama_integration

// Manual integration tests against a live local Ollama:
//
//	go test -tags ollama_integration ./internal/provider/ollama/ -v
//
// Requires Ollama running at localhost:11434 (or MASQUE_OLLAMA_URL) with
// at least one model pulled.
package ollama

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
	p := New(os.Getenv("MASQUE_OLLAMA_URL"))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := p.HealthCheck(ctx); err != nil {
		t.Skipf("no live ollama: %v", err)
	}
	return p
}

func firstModel(t *testing.T, p *Provider) string {
	t.Helper()
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) == 0 {
		t.Skip("live ollama has no models pulled")
	}
	return models[0].ID
}

func TestLiveChatStream(t *testing.T) {
	p := liveProvider(t)
	model := firstModel(t, p)
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
	for ev := range events {
		if ev.Err != nil {
			t.Fatalf("stream error: %v", ev.Err)
		}
		sb.WriteString(ev.Delta)
		done = done || ev.Done
	}
	if !done {
		t.Fatal("stream closed without done")
	}
	if sb.Len() == 0 {
		t.Fatal("empty completion")
	}
	t.Logf("completion: %q", sb.String())
}

func TestLiveContextWindow(t *testing.T) {
	p := liveProvider(t)
	model := firstModel(t, p)
	n, err := p.ContextWindow(context.Background(), model)
	if err != nil {
		t.Fatalf("ContextWindow(%s): %v", model, err)
	}
	if n < 512 {
		t.Errorf("context window %d looks wrong", n)
	}
	t.Logf("%s context window: %d", model, n)
}
