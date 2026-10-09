// Package presets is the registry of cloud provider presets: which
// hosted APIs Masque knows how to talk to, where they live, where a
// user gets a key, and which chat models to recommend first. The data
// lives in the embedded presets.json so the roster can be revised per
// release without code changes; this package loads, validates, and
// applies it (chat-model filtering and default-model selection).
//
// Ollama is deliberately not a preset: the local path has its own
// manager (internal/ollamamgr) and never goes through here.
package presets

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"masque/internal/provider"
)

// API formats a preset can use. They name the provider implementation
// in internal/provider that speaks to the endpoint.
const (
	FormatOpenAI    = "openai"
	FormatAnthropic = "anthropic"
)

//go:embed presets.json
var raw []byte

// Pinned is one recommended chat model on a preset.
type Pinned struct {
	ID    string `json:"id"`    // exact id for the request's model field
	Label string `json:"label"` // short display name
	Note  string `json:"note"`  // one line: "fast and cheap", "strongest", ...
}

// Preset is one hosted provider.
type Preset struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Format  string `json:"format"`  // FormatOpenAI or FormatAnthropic
	BaseURL string `json:"baseUrl"` // "" only when Custom
	KeyURL  string `json:"keyUrl"`  // where to create an API key
	// Custom marks the "bring your own endpoint" presets: the user
	// supplies the base URL, and a key is optional (local servers
	// often run without one).
	Custom bool `json:"custom"`
	// PartialList marks hosts whose model listing is paginated,
	// filtered by plan, or undocumented, so absence from it proves
	// nothing: pinned models stay recommended regardless.
	PartialList  bool     `json:"partialList"`
	Pinned       []Pinned `json:"pinned"`
	LastVerified string   `json:"lastVerified"` // YYYY-MM-DD the URLs and ids were checked
}

// Registry is the parsed, validated presets file.
type Registry struct {
	Presets []Preset
	// excludePatterns are compiled case-insensitively from the file's
	// exclude_patterns; a model id matching any is not a chat model.
	excludePatterns []*regexp.Regexp
	patternSources  []string
}

type file struct {
	ExcludePatterns []string `json:"exclude_patterns"`
	Presets         []Preset `json:"presets"`
}

// Load parses and validates the embedded registry.
func Load() (*Registry, error) {
	return Parse(raw)
}

// Parse builds a Registry from presets.json bytes. Every preset must
// have an id, label, and format; non-custom presets must also have a
// base URL and at least one pinned model.
func Parse(data []byte) (*Registry, error) {
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parsing presets: %w", err)
	}
	r := &Registry{Presets: f.Presets, patternSources: f.ExcludePatterns}
	seen := map[string]bool{}
	for i, p := range f.Presets {
		switch {
		case p.ID == "":
			return nil, fmt.Errorf("preset %d: missing id", i)
		case seen[p.ID]:
			return nil, fmt.Errorf("preset %q: duplicate id", p.ID)
		case p.Label == "":
			return nil, fmt.Errorf("preset %q: missing label", p.ID)
		case p.Format != FormatOpenAI && p.Format != FormatAnthropic:
			return nil, fmt.Errorf("preset %q: unknown format %q", p.ID, p.Format)
		case !p.Custom && p.BaseURL == "":
			return nil, fmt.Errorf("preset %q: missing baseUrl", p.ID)
		case !p.Custom && len(p.Pinned) == 0:
			return nil, fmt.Errorf("preset %q: no pinned models", p.ID)
		case !p.Custom && p.KeyURL == "":
			return nil, fmt.Errorf("preset %q: missing keyUrl", p.ID)
		case !p.Custom && p.LastVerified == "":
			return nil, fmt.Errorf("preset %q: missing lastVerified", p.ID)
		}
		seen[p.ID] = true
		for j, m := range p.Pinned {
			if m.ID == "" || m.Label == "" {
				return nil, fmt.Errorf("preset %q: pinned model %d needs id and label", p.ID, j)
			}
		}
	}
	if len(f.ExcludePatterns) == 0 {
		return nil, fmt.Errorf("presets: no exclude_patterns")
	}
	for _, src := range f.ExcludePatterns {
		re, err := regexp.Compile("(?i)" + src)
		if err != nil {
			return nil, fmt.Errorf("exclude pattern %q: %w", src, err)
		}
		r.excludePatterns = append(r.excludePatterns, re)
	}
	return r, nil
}

// Find returns the preset with id.
func (r *Registry) Find(id string) (Preset, bool) {
	for _, p := range r.Presets {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}

// ExcludePatterns returns the raw pattern sources (for display/tests).
func (r *Registry) ExcludePatterns() []string {
	return append([]string(nil), r.patternSources...)
}

// IsChatModel reports whether m can be used for chat. Provider
// metadata wins when the endpoint supplied it (m.Chat); otherwise the
// id is matched against the registry's exclude patterns.
func (r *Registry) IsChatModel(m provider.ModelInfo) bool {
	if m.Chat != nil {
		return *m.Chat
	}
	return !r.excludedByID(m.ID)
}

func (r *Registry) excludedByID(id string) bool {
	for _, re := range r.excludePatterns {
		if re.MatchString(id) {
			return true
		}
	}
	return false
}

// FilterChat returns the chat-capable subset of models, in order.
func (r *Registry) FilterChat(models []provider.ModelInfo) []provider.ModelInfo {
	out := make([]provider.ModelInfo, 0, len(models))
	for _, m := range models {
		if r.IsChatModel(m) {
			out = append(out, m)
		}
	}
	return out
}

// Model is one picker entry.
type Model struct {
	ID    string `json:"id"`
	Label string `json:"label"` // display name; the id for unpinned models
	Note  string `json:"note"`  // "" for unpinned models
}

// Catalog is what the model picker renders for one provider.
type Catalog struct {
	ProviderID string `json:"providerId"`
	// Recommended holds the pinned models — only those the endpoint
	// actually lists when a listing was obtained, all of them when it
	// wasn't (the user can still pick one and try).
	Recommended []Model `json:"recommended"`
	// All is every chat-capable model the endpoint listed; empty when
	// the listing failed.
	All []Model `json:"all"`
	// Default is the model to preselect: the user's last choice on this
	// provider if it is still usable, else the first recommended model,
	// else the first listed chat model. Never a non-chat model. "" when
	// nothing is known.
	Default string `json:"default"`
	// Listed is true when the endpoint's model list was obtained;
	// ListError carries the failure otherwise (the key check failed,
	// endpoint unreachable, listing unsupported).
	Listed    bool   `json:"listed"`
	ListError string `json:"listError"`
	// KeyRejected is set when the listing failed with 401/403: the key
	// is wrong, not merely the endpoint lacking a list. Other failures
	// (404 on hosts without /models, network) leave the pinned models
	// usable.
	KeyRejected bool `json:"keyRejected"`
}

// normalizeID makes pinned ids and listed ids comparable: Gemini's
// OpenAI layer lists "models/gemini-…" while requests take either
// form, and some hosts differ in case.
func normalizeID(id string) string {
	return strings.ToLower(strings.TrimPrefix(id, "models/"))
}

// BuildCatalog assembles the picker catalog for preset from the
// endpoint's listing (listed, listErr) and the user's last choice on
// this provider. listErr != nil means no listing is available.
func (r *Registry) BuildCatalog(preset Preset, listed []provider.ModelInfo, listErr error, last string) Catalog {
	c := Catalog{ProviderID: preset.ID, Recommended: []Model{}, All: []Model{}}
	if listErr != nil {
		c.ListError = listErr.Error()
		var httpErr *provider.HTTPError
		if errors.As(listErr, &httpErr) && httpErr.Unauthorized() {
			c.KeyRejected = true
		}
		for _, p := range preset.Pinned {
			// Pinned ids are curated as chat models, but never let a
			// registry mistake auto-select something the filter rejects.
			if !r.excludedByID(p.ID) {
				c.Recommended = append(c.Recommended, Model(p))
			}
		}
		c.Default = r.pickDefault(c, last, nil)
		return c
	}

	c.Listed = true
	chat := r.FilterChat(listed)
	available := make(map[string]bool, len(chat))
	for _, m := range chat {
		available[normalizeID(m.ID)] = true
		c.All = append(c.All, Model{ID: m.ID, Label: m.ID})
	}
	for _, p := range preset.Pinned {
		if available[normalizeID(p.ID)] || (preset.PartialList && !r.excludedByID(p.ID)) {
			c.Recommended = append(c.Recommended, Model(p))
		}
	}
	if preset.PartialList {
		// The listing can't vouch for anything it lacks, so the user's
		// remembered choice is trusted the same way as with no listing.
		available = nil
	}
	c.Default = r.pickDefault(c, last, available)
	return c
}

// pickDefault applies the selection order. available is nil when no
// listing exists, in which case last is trusted if it passes the
// filter.
func (r *Registry) pickDefault(c Catalog, last string, available map[string]bool) string {
	if last != "" && !r.excludedByID(last) {
		if available == nil || available[normalizeID(last)] {
			return last
		}
	}
	if len(c.Recommended) > 0 {
		return c.Recommended[0].ID
	}
	if len(c.All) > 0 {
		return c.All[0].ID
	}
	return ""
}

// MustLoad returns the embedded registry, panicking if it fails to
// validate. The data file ships inside the binary and is covered by
// tests, so a failure here is a build defect, not a runtime condition.
func MustLoad() *Registry {
	r, err := Load()
	if err != nil {
		panic(err)
	}
	return r
}
