//go:build anthropic_integration

// Manual integration tests against the live Anthropic API. Requires a
// real key:
//
//	MASQUE_ANTHROPIC_KEY=sk-ant-... go test -tags anthropic_integration ./internal/provider/anthropic/ -v
package anthropic

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
	key := os.Getenv("MASQUE_ANTHROPIC_KEY")
	if key == "" {
		t.Skip("MASQUE_ANTHROPIC_KEY not set")
	}
	p := New(os.Getenv("MASQUE_ANTHROPIC_URL"), key)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.HealthCheck(ctx); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	return p
}

func TestLiveChatStream(t *testing.T) {
	p := liveProvider(t)
	model := os.Getenv("MASQUE_ANTHROPIC_MODEL")
	if model == "" {
		model = "claude-haiku-4-5"
	}
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
	if !done || sb.Len() == 0 {
		t.Fatalf("done=%v content=%q", done, sb.String())
	}
	t.Logf("completion: %q", sb.String())
}
