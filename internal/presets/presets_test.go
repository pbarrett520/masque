package presets

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"masque/internal/provider"
)

func mustLoad(t *testing.T) *Registry {
	t.Helper()
	r, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return r
}

// openAIFixture is a realistic GET /v1/models response from OpenAI,
// embedding models first (the ordering that bit the first tester).
func openAIFixture(t *testing.T) []provider.ModelInfo {
	t.Helper()
	raw, err := os.ReadFile("testdata/openai_models.json")
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	models := make([]provider.ModelInfo, 0, len(body.Data))
	for _, m := range body.Data {
		models = append(models, provider.ModelInfo{ID: m.ID})
	}
	if !strings.Contains(models[0].ID, "embedding") {
		t.Fatalf("fixture must start with an embedding model, got %q", models[0].ID)
	}
	return models
}

func ids(models []Model) []string {
	out := make([]string, len(models))
	for i, m := range models {
		out[i] = m.ID
	}
	return out
}

func TestRegistryLoadsAndValidates(t *testing.T) {
	r := mustLoad(t)
	if len(r.Presets) < 3 {
		t.Fatalf("registry has only %d presets", len(r.Presets))
	}
	for _, p := range r.Presets {
		if p.Format != FormatOpenAI && p.Format != FormatAnthropic {
			t.Errorf("%s: bad format %q", p.ID, p.Format)
		}
		if p.Custom {
			continue
		}
		if !strings.HasPrefix(p.BaseURL, "https://") || strings.HasSuffix(p.BaseURL, "/") {
			t.Errorf("%s: baseUrl %q must be https and have no trailing slash", p.ID, p.BaseURL)
		}
		if !strings.HasPrefix(p.KeyURL, "https://") {
			t.Errorf("%s: keyUrl %q", p.ID, p.KeyURL)
		}
		if n := len(p.Pinned); n < 1 || n > 4 {
			t.Errorf("%s: %d pinned models, want 1..4", p.ID, n)
		}
		if _, err := time.Parse("2006-01-02", p.LastVerified); err != nil {
			t.Errorf("%s: lastVerified %q: %v", p.ID, p.LastVerified, err)
		}
		for _, m := range p.Pinned {
			if r.excludedByID(m.ID) {
				t.Errorf("%s: pinned %q is excluded by the chat filter", p.ID, m.ID)
			}
		}
	}
	for _, id := range []string{"openai", "anthropic", "custom-openai", "custom-anthropic"} {
		if _, ok := r.Find(id); !ok {
			t.Errorf("required preset %q missing", id)
		}
	}
}

func TestParseRejectsIncompletePresets(t *testing.T) {
	cases := map[string]string{
		"no base url": `{"exclude_patterns":["embed"],"presets":[{"id":"x","label":"X","format":"openai","keyUrl":"https://k","pinned":[{"id":"m","label":"M"}],"lastVerified":"2026-01-01"}]}`,
		"no pinned":   `{"exclude_patterns":["embed"],"presets":[{"id":"x","label":"X","format":"openai","baseUrl":"https://b","keyUrl":"https://k","pinned":[],"lastVerified":"2026-01-01"}]}`,
		"bad format":  `{"exclude_patterns":["embed"],"presets":[{"id":"x","label":"X","format":"grpc","baseUrl":"https://b","keyUrl":"https://k","pinned":[{"id":"m","label":"M"}],"lastVerified":"2026-01-01"}]}`,
		"no patterns": `{"exclude_patterns":[],"presets":[]}`,
		"dup id":      `{"exclude_patterns":["embed"],"presets":[{"id":"x","label":"X","format":"openai","custom":true},{"id":"x","label":"Y","format":"openai","custom":true}]}`,
	}
	for name, src := range cases {
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("%s: Parse accepted invalid registry", name)
		}
	}
}

func TestOpenAIListNeverDefaultsToEmbedding(t *testing.T) {
	r := mustLoad(t)
	listed := openAIFixture(t)

	// The shipped OpenAI preset against a realistic list.
	openai, _ := r.Find("openai")
	c := r.BuildCatalog(openai, listed, nil, "")
	if c.Default == "" || !r.IsChatModel(provider.ModelInfo{ID: c.Default}) {
		t.Fatalf("default %q is not a chat model", c.Default)
	}
	if strings.Contains(c.Default, "embedding") {
		t.Fatalf("defaulted to embedding model %q", c.Default)
	}
	if c.Default != openai.Pinned[0].ID {
		t.Errorf("default = %q, want first pinned %q", c.Default, openai.Pinned[0].ID)
	}
	for _, m := range c.All {
		if !r.IsChatModel(provider.ModelInfo{ID: m.ID}) {
			t.Errorf("All contains non-chat model %q", m.ID)
		}
	}
	for _, bad := range []string{"text-embedding-3-small", "whisper-1", "tts-1", "dall-e-3", "gpt-4o-realtime-preview", "omni-moderation-latest", "sora-2", "babbage-002", "gpt-3.5-turbo-instruct"} {
		for _, m := range c.All {
			if m.ID == bad {
				t.Errorf("All still lists %q", bad)
			}
		}
	}
	for _, good := range []string{"gpt-4o", "gpt-4.1-mini", "gpt-5", "chatgpt-4o-latest"} {
		found := false
		for _, m := range c.All {
			found = found || m.ID == good
		}
		if !found {
			t.Errorf("All dropped chat model %q", good)
		}
	}

	// Same list with a preset whose pinned models aren't offered: fall
	// through to the first chat model, still never the embedding.
	stale := Preset{ID: "stale", Pinned: []Pinned{{ID: "gpt-99", Label: "GPT-99"}}}
	c = r.BuildCatalog(stale, listed, nil, "")
	if len(c.Recommended) != 0 {
		t.Errorf("stale pins should not be recommended, got %v", ids(c.Recommended))
	}
	if c.Default != "gpt-4o" {
		t.Errorf("fallback default = %q, want first chat model gpt-4o", c.Default)
	}
}

func TestDefaultSelection(t *testing.T) {
	r := mustLoad(t)
	preset := Preset{ID: "p", Pinned: []Pinned{
		{ID: "pin-a", Label: "A", Note: "fast"},
		{ID: "pin-b", Label: "B", Note: "strong"},
	}}
	list := func(ids ...string) []provider.ModelInfo {
		out := make([]provider.ModelInfo, len(ids))
		for i, id := range ids {
			out[i] = provider.ModelInfo{ID: id}
		}
		return out
	}

	t.Run("pinned available", func(t *testing.T) {
		c := r.BuildCatalog(preset, list("text-embedding-1", "other-chat", "pin-b", "pin-a"), nil, "")
		if got := ids(c.Recommended); strings.Join(got, ",") != "pin-a,pin-b" {
			t.Errorf("recommended = %v (registry order, not list order)", got)
		}
		if c.Default != "pin-a" {
			t.Errorf("default = %q", c.Default)
		}
		if !c.Listed || c.ListError != "" {
			t.Errorf("listed=%v err=%q", c.Listed, c.ListError)
		}
	})
	t.Run("only second pin available", func(t *testing.T) {
		c := r.BuildCatalog(preset, list("pin-b", "other-chat"), nil, "")
		if c.Default != "pin-b" {
			t.Errorf("default = %q", c.Default)
		}
	})
	t.Run("pinned missing", func(t *testing.T) {
		c := r.BuildCatalog(preset, list("text-embedding-1", "chat-x", "chat-y"), nil, "")
		if len(c.Recommended) != 0 || c.Default != "chat-x" {
			t.Errorf("recommended=%v default=%q", ids(c.Recommended), c.Default)
		}
	})
	t.Run("nothing usable", func(t *testing.T) {
		c := r.BuildCatalog(preset, list("text-embedding-1", "whisper-1"), nil, "")
		if c.Default != "" {
			t.Errorf("default = %q, want none", c.Default)
		}
	})
	t.Run("list endpoint failing", func(t *testing.T) {
		c := r.BuildCatalog(preset, nil, errors.New("boom"), "")
		if c.Listed || c.ListError == "" || c.KeyRejected {
			t.Errorf("listed=%v err=%q rejected=%v", c.Listed, c.ListError, c.KeyRejected)
		}
		if got := ids(c.Recommended); strings.Join(got, ",") != "pin-a,pin-b" {
			t.Errorf("recommended = %v, want every pin", got)
		}
		if c.Default != "pin-a" || len(c.All) != 0 {
			t.Errorf("default=%q all=%v", c.Default, ids(c.All))
		}
	})
	t.Run("key rejected", func(t *testing.T) {
		err := &provider.HTTPError{Op: "listing models", Status: http.StatusUnauthorized, Message: "bad key"}
		c := r.BuildCatalog(preset, nil, err, "")
		if !c.KeyRejected {
			t.Error("401 should set KeyRejected")
		}
		if c404 := r.BuildCatalog(preset, nil, &provider.HTTPError{Status: 404}, ""); c404.KeyRejected {
			t.Error("404 must not count as a rejected key")
		}
	})
}

func TestLastSelectionRestored(t *testing.T) {
	r := mustLoad(t)
	preset := Preset{ID: "p", Pinned: []Pinned{{ID: "pin-a", Label: "A"}}}
	list := []provider.ModelInfo{{ID: "pin-a"}, {ID: "chat-z"}, {ID: "text-embedding-1"}}

	if c := r.BuildCatalog(preset, list, nil, "chat-z"); c.Default != "chat-z" {
		t.Errorf("last listed: default = %q", c.Default)
	}
	if c := r.BuildCatalog(preset, list, nil, "gone"); c.Default != "pin-a" {
		t.Errorf("last vanished: default = %q", c.Default)
	}
	// A remembered non-chat model is never restored, even unlisted.
	if c := r.BuildCatalog(preset, list, nil, "text-embedding-1"); c.Default != "pin-a" {
		t.Errorf("last excluded: default = %q", c.Default)
	}
	if c := r.BuildCatalog(preset, nil, errors.New("down"), "text-embedding-1"); c.Default != "pin-a" {
		t.Errorf("last excluded, no list: default = %q", c.Default)
	}
	// Without a listing the last choice is trusted.
	if c := r.BuildCatalog(preset, nil, errors.New("down"), "chat-z"); c.Default != "chat-z" {
		t.Errorf("last, no list: default = %q", c.Default)
	}
}

func TestMetadataBeatsIDPatterns(t *testing.T) {
	r := mustLoad(t)
	yes, no := true, false
	if r.IsChatModel(provider.ModelInfo{ID: "vendor/chatty-model", Chat: &no}) {
		t.Error("metadata chat=false must exclude a chat-looking id")
	}
	if !r.IsChatModel(provider.ModelInfo{ID: "vendor/embedding-but-chat", Chat: &yes}) {
		t.Error("metadata chat=true must win over the id pattern")
	}
	if r.IsChatModel(provider.ModelInfo{ID: "Text-Embedding-3"}) {
		t.Error("pattern match must be case-insensitive")
	}
}

func TestPatternsKeepCommonChatModels(t *testing.T) {
	r := mustLoad(t)
	for _, id := range []string{
		"Qwen/Qwen2.5-72B-Instruct", "meta-llama/Llama-3.3-70B-Instruct",
		"anthropic/claude-sonnet-4.5", "Sao10K/L3-8B-Stheno-v3.2",
		"TheDrummer/Cydonia-24B-v4", "mistralai/Mistral-Nemo-Instruct-2407",
		"nvidia/llama-3.1-nemotron-70b-instruct", "gpt-4o-mini", "o4-mini",
		"deepseek-chat", "deepseek-reasoner", "models/gemini-2.5-flash",
		"command-a-03-2025", "grok-4", "gemma-3-27b-it", "llama-3.3-70b-versatile",
		"kimi-k2-0905-preview", "glm-4.6", "mistral-large-latest",
	} {
		if !r.IsChatModel(provider.ModelInfo{ID: id}) {
			t.Errorf("chat model %q wrongly excluded", id)
		}
	}
	for _, id := range []string{
		"BAAI/bge-m3", "intfloat/e5-large-v2", "meta-llama/Llama-Guard-4-12B",
		"black-forest-labs/FLUX.1-schnell", "stabilityai/stable-diffusion-xl",
		"openai/whisper-large-v3", "mistral-ocr-latest", "mistral-embed",
		"playai-tts", "distil-whisper-large-v3-en", "rerank-v3.5", "embed-v4.0",
		"voyage-3", "gpt-realtime", "cohere/rerank-english-v3.0",
	} {
		if r.IsChatModel(provider.ModelInfo{ID: id}) {
			t.Errorf("non-chat model %q wrongly kept", id)
		}
	}
}

func TestGeminiPrefixNormalized(t *testing.T) {
	r := mustLoad(t)
	preset := Preset{ID: "gemini", Pinned: []Pinned{{ID: "gemini-2.5-flash", Label: "Flash"}}}
	c := r.BuildCatalog(preset, []provider.ModelInfo{{ID: "models/gemini-2.5-pro"}, {ID: "models/gemini-2.5-flash"}}, nil, "")
	if c.Default != "gemini-2.5-flash" || len(c.Recommended) != 1 {
		t.Errorf("default=%q recommended=%v", c.Default, ids(c.Recommended))
	}
	if c := r.BuildCatalog(preset, []provider.ModelInfo{{ID: "models/gemini-2.5-flash"}}, nil, "models/gemini-2.5-flash"); c.Default != "models/gemini-2.5-flash" {
		t.Errorf("prefixed last choice: default=%q", c.Default)
	}
}

func TestPartialListKeepsPinned(t *testing.T) {
	r := mustLoad(t)
	preset := Preset{ID: "p", PartialList: true, Pinned: []Pinned{{ID: "Org/Pinned-RP", Label: "P"}}}
	listed := []provider.ModelInfo{{ID: "Org/Other"}, {ID: "Org/bge-embed"}}
	c := r.BuildCatalog(preset, listed, nil, "")
	if got := ids(c.Recommended); strings.Join(got, ",") != "Org/Pinned-RP" {
		t.Errorf("recommended = %v", got)
	}
	if c.Default != "Org/Pinned-RP" || len(c.All) != 1 {
		t.Errorf("default=%q all=%v", c.Default, ids(c.All))
	}
	// A remembered model absent from a partial page is still restored.
	if c := r.BuildCatalog(preset, listed, nil, "Org/Deep-Page"); c.Default != "Org/Deep-Page" {
		t.Errorf("last on partial list: default=%q", c.Default)
	}
	// Even on a partial list, a pinned id matching the filter is not shown.
	bad := Preset{ID: "p", PartialList: true, Pinned: []Pinned{{ID: "Org/text-embedding", Label: "E"}}}
	if c := r.BuildCatalog(bad, listed, nil, ""); len(c.Recommended) != 0 || c.Default != "Org/Other" {
		t.Errorf("excluded pin leaked: recommended=%v default=%q", ids(c.Recommended), c.Default)
	}
}
