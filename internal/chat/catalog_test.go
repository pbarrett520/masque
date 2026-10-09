package chat

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"masque/internal/provider"
)

// openAIStyleList mimics OpenAI's /v1/models ordering: embeddings and
// other non-chat models first, the chat models after.
func openAIStyleList() []provider.ModelInfo {
	var out []provider.ModelInfo
	for _, id := range []string{
		"text-embedding-3-small", "text-embedding-3-large", "whisper-1", "tts-1",
		"dall-e-3", "omni-moderation-latest", "gpt-4o-realtime-preview",
		"gpt-4o-mini", "gpt-4o", "gpt-6-luna", "gpt-6-astra",
	} {
		out = append(out, provider.ModelInfo{ID: id})
	}
	return out
}

func (f *fixture) setString(t *testing.T, key, value string) {
	t.Helper()
	raw, _ := json.Marshal(value)
	if err := f.store.SetSetting(key, string(raw)); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogNeverDefaultsToEmbedding(t *testing.T) {
	f := newFixture(t)
	f.fake.models = openAIStyleList()

	c, err := f.svc.Catalog("openai")
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if !c.Listed || c.KeyRejected {
		t.Fatalf("listed=%v rejected=%v err=%q", c.Listed, c.KeyRejected, c.ListError)
	}
	if c.Default != "gpt-6-luna" {
		t.Errorf("default = %q, want first pinned OpenAI model", c.Default)
	}
	if strings.Contains(c.Default, "embedding") {
		t.Fatalf("defaulted to embedding model %q", c.Default)
	}
	if len(c.Recommended) != 2 || c.Recommended[0].ID != "gpt-6-luna" || c.Recommended[1].ID != "gpt-6-astra" {
		t.Errorf("recommended = %+v", c.Recommended)
	}
	for _, m := range c.All {
		for _, bad := range []string{"embedding", "whisper", "tts", "dall-e", "moderation", "realtime"} {
			if strings.Contains(m.ID, bad) {
				t.Errorf("All lists non-chat model %q", m.ID)
			}
		}
	}
	if len(c.All) != 4 {
		t.Errorf("All = %+v, want the 4 chat models", c.All)
	}
}

func TestCatalogWithoutListingKeepsPinned(t *testing.T) {
	f := newFixture(t)
	f.fake.listErr = &provider.HTTPError{Op: "listing models", Status: http.StatusNotFound}

	c, err := f.svc.Catalog("anthropic")
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if c.Listed || c.KeyRejected || c.ListError == "" {
		t.Errorf("listed=%v rejected=%v err=%q", c.Listed, c.KeyRejected, c.ListError)
	}
	if len(c.Recommended) == 0 || c.Default != c.Recommended[0].ID {
		t.Errorf("recommended=%+v default=%q", c.Recommended, c.Default)
	}

	f.fake.listErr = &provider.HTTPError{Op: "listing models", Status: http.StatusUnauthorized, Message: "invalid x-api-key"}
	c, err = f.svc.Catalog("anthropic")
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if !c.KeyRejected {
		t.Error("401 should flag the key as rejected")
	}
}

func TestCatalogRestoresLastModelPerProvider(t *testing.T) {
	f := newFixture(t)
	f.fake.models = openAIStyleList()
	state := f.startWithModel(t)

	if err := f.svc.SetModel(state.ChatID, "openai", "gpt-4o"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	c, err := f.svc.Catalog("openai")
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if c.Default != "gpt-4o" {
		t.Errorf("default = %q, want the remembered gpt-4o", c.Default)
	}
	// Another provider's memory is separate.
	c, err = f.svc.Catalog("openrouter")
	if err != nil {
		t.Fatalf("Catalog(openrouter): %v", err)
	}
	if c.Default == "gpt-4o" {
		t.Error("openrouter must not inherit openai's last model")
	}
	// A remembered non-chat model (e.g. set by hand) is never restored.
	f.setString(t, lastModelSetting("openai"), "text-embedding-3-small")
	c, _ = f.svc.Catalog("openai")
	if c.Default != "gpt-6-luna" {
		t.Errorf("default = %q after excluded last model", c.Default)
	}
}

func TestCatalogOllamaIsFlat(t *testing.T) {
	f := newFixture(t)
	f.fake.models = []provider.ModelInfo{{ID: "llama3:8b"}, {ID: "mag-mell:12b"}}
	c, err := f.svc.Catalog("ollama")
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if len(c.Recommended) != 0 || len(c.All) != 2 || c.Default != "llama3:8b" {
		t.Errorf("catalog = %+v", c)
	}
	f.setString(t, lastModelSetting("ollama"), "mag-mell:12b")
	if c, _ := f.svc.Catalog("ollama"); c.Default != "mag-mell:12b" {
		t.Errorf("default = %q, want remembered local model", c.Default)
	}
}

func TestProvidersReportConfiguration(t *testing.T) {
	f := newFixture(t)
	find := func(id string) ProviderInfo {
		for _, p := range f.svc.Providers() {
			if p.ID == id {
				return p
			}
		}
		t.Fatalf("provider %q missing", id)
		return ProviderInfo{}
	}
	if p := find("ollama"); !p.Configured || p.NeedsKey {
		t.Errorf("ollama = %+v", p)
	}
	if p := find("openai"); p.Configured || !p.NeedsKey || p.KeyURL == "" || p.BaseURL == "" {
		t.Errorf("openai unconfigured = %+v", p)
	}
	f.setString(t, keySetting("openai"), "sk-test")
	if p := find("openai"); !p.Configured || p.NeedsKey {
		t.Errorf("openai with key = %+v", p)
	}
	if p := find("custom-openai"); p.Configured || !p.Custom {
		t.Errorf("custom without url = %+v", p)
	}
	f.setString(t, urlSetting("custom-openai"), "http://localhost:1234/v1")
	if p := find("custom-openai"); !p.Configured || p.NeedsKey || p.BaseURL != "http://localhost:1234/v1" {
		t.Errorf("custom with url = %+v", p)
	}
	if ids := f.svc.Providers(); ids[0].ID != "ollama" || len(ids) < 10 {
		t.Errorf("provider order/count: %d, first %q", len(ids), ids[0].ID)
	}
}

func TestBuildProviderResolvesPresets(t *testing.T) {
	f := newFixture(t)
	if p, err := f.svc.buildProvider("openrouter"); err != nil || p.ID() != "openai" {
		t.Errorf("openrouter: %v %v", p, err)
	}
	if p, err := f.svc.buildProvider("anthropic"); err != nil || p.ID() != "anthropic" {
		t.Errorf("anthropic: %v %v", p, err)
	}
	if _, err := f.svc.buildProvider("custom-openai"); err == nil {
		t.Error("custom preset without base URL should fail")
	}
	f.setString(t, urlSetting("custom-anthropic"), "https://proxy.example/anthropic")
	if p, err := f.svc.buildProvider("custom-anthropic"); err != nil || p.ID() != "anthropic" {
		t.Errorf("custom-anthropic: %v %v", p, err)
	}
	if _, err := f.svc.buildProvider("hyperbolic"); err == nil {
		t.Error("unknown preset should fail")
	}
}
