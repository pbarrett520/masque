// Package ollamamgr is the Ollama management frontend (dev spec §8):
// detect, list installed models, pull with progress, delete, and the
// curated starter roster that drives onboarding. Bound to the frontend
// as ollamamgr.Service. All traffic goes through the ollama provider so
// the base URL and HTTP plumbing stay in one place.
package ollamamgr

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"masque/internal/provider"
	"masque/internal/provider/ollama"
	"masque/internal/store"
	"masque/internal/sysinfo"
)

// settingBaseURL mirrors chat.Service's key: one Ollama endpoint,
// configured in settings, read fresh on every call.
const settingBaseURL = "provider.ollama.base_url"

// probeTimeout bounds the detect/list/delete calls; pulls are unbounded
// (they run for however long the download takes) and cancelable.
const probeTimeout = 5 * time.Second

// PullEventName is the Wails event carrying pull progress.
const PullEventName = "ollama:pull"

//go:embed starter_models.json
var starterManifest []byte

// StarterModel is one curated roster entry, annotated for the machine
// and install at hand.
type StarterModel struct {
	Ref           string `json:"ref"`    // what Pull takes and chats use as the model name
	Name          string `json:"name"`   // display name
	Params        string `json:"params"` // e.g. "12B"
	Description   string `json:"description"`
	DownloadBytes int64  `json:"downloadBytes"`
	MinRAMBytes   int64  `json:"minRamBytes"`
	Recommended   bool   `json:"recommended"`
	Installed     bool   `json:"installed"`
	// Fits is false when the machine's RAM is known to be below
	// MinRAMBytes. Unknown RAM counts as fitting — flag, never hide.
	Fits bool `json:"fits"`
}

// manifest is the parsed embedded roster.
type manifest struct {
	Models []StarterModel `json:"models"`
}

// EmitFunc delivers a Wails event to the frontend.
type EmitFunc func(event string, args ...any)

// Service is bound to the Wails frontend as ollamamgr.Service.
type Service struct {
	store    *store.Store
	emit     EmitFunc
	totalRAM func() uint64 // test seam

	mu         sync.Mutex
	pullCancel context.CancelFunc // non-nil while a pull is running
	pullRef    string
}

// NewService returns a Service backed by st that reports pull progress
// through emit.
func NewService(st *store.Store, emit EmitFunc) *Service {
	return &Service{store: st, emit: emit, totalRAM: sysinfo.TotalRAM}
}

// provider builds an Ollama provider from the current settings.
func (s *Service) provider() *ollama.Provider {
	raw, ok, err := s.store.GetSetting(settingBaseURL)
	if err != nil || !ok {
		return ollama.New("")
	}
	var url string
	if json.Unmarshal([]byte(raw), &url) != nil {
		return ollama.New("")
	}
	return ollama.New(url)
}

// Status is the onboarding/settings detect probe.
type Status struct {
	Reachable bool   `json:"reachable"`
	Version   string `json:"version"`
	BaseURL   string `json:"baseUrl"`
	Error     string `json:"error"` // why unreachable, "" otherwise
}

// Status probes the configured Ollama endpoint.
func (s *Service) Status() Status {
	p := s.provider()
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	version, err := p.Version(ctx)
	st := Status{BaseURL: p.BaseURL()}
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.Reachable = true
	st.Version = version
	return st
}

// Installed lists the chat-capable models on the endpoint.
func (s *Service) Installed() ([]provider.ModelInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	return s.provider().ListModels(ctx)
}

// Recommended returns the curated roster annotated with installed and
// fits flags. Endpoint errors degrade gracefully: the roster still
// renders, with nothing marked installed.
func (s *Service) Recommended() ([]StarterModel, error) {
	var m manifest
	if err := json.Unmarshal(starterManifest, &m); err != nil {
		return nil, fmt.Errorf("parsing starter manifest: %w", err)
	}
	installed := map[string]bool{}
	if models, err := s.Installed(); err == nil {
		for _, mi := range models {
			installed[normalizeRef(mi.ID)] = true
		}
	}
	ram := s.totalRAM()
	out := make([]StarterModel, 0, len(m.Models))
	for _, sm := range m.Models {
		sm.Installed = installed[normalizeRef(sm.Ref)]
		sm.Fits = ram == 0 || uint64(sm.MinRAMBytes) <= ram
		out = append(out, sm)
	}
	return out, nil
}

// normalizeRef makes installed-model names comparable with manifest
// refs: case-insensitive (Ollama lowercases registry names) and with an
// implicit :latest tag.
func normalizeRef(ref string) string {
	ref = strings.ToLower(strings.TrimSpace(ref))
	if !strings.Contains(ref, ":") {
		ref += ":latest"
	}
	return ref
}

// PullProgress is the ollama:pull event payload. Percent is derived
// from the largest layer seen so far (the weights layer dominates a
// pull), so it never runs backwards when small layers follow.
type PullProgress struct {
	Ref       string `json:"ref"`
	Status    string `json:"status"`
	Total     int64  `json:"total"`
	Completed int64  `json:"completed"`
	Done      bool   `json:"done"`
	Error     string `json:"error"` // set on failure; "canceled" on cancel
}

// Pull starts downloading ref in the background, reporting progress via
// ollama:pull events. One pull at a time — model downloads saturate the
// link anyway, and one progress bar is comprehensible.
func (s *Service) Pull(ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return errors.New("model ref is empty")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	if s.pullCancel != nil {
		busy := s.pullRef
		s.mu.Unlock()
		cancel()
		return fmt.Errorf("already downloading %s — wait or cancel it first", busy)
	}
	s.pullCancel = cancel
	s.pullRef = ref
	s.mu.Unlock()

	events, err := s.provider().Pull(ctx, ref)
	if err != nil {
		s.clearPull()
		return err
	}
	go s.forwardPull(ctx, ref, events)
	return nil
}

// CancelPull cancels the in-flight pull, if any. Partial layers stay in
// Ollama's cache, so pulling again resumes.
func (s *Service) CancelPull() {
	s.mu.Lock()
	cancel := s.pullCancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// PullInFlight returns the ref currently downloading, or "" — lets the
// UI re-attach after a screen switch.
func (s *Service) PullInFlight() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pullRef
}

func (s *Service) clearPull() {
	s.mu.Lock()
	if s.pullCancel != nil {
		s.pullCancel()
		s.pullCancel = nil
		s.pullRef = ""
	}
	s.mu.Unlock()
}

// forwardPull drains the provider stream, translating layer progress
// into monotonic percent-style progress events.
func (s *Service) forwardPull(ctx context.Context, ref string, events <-chan ollama.PullEvent) {
	defer s.clearPull()
	var bigTotal, bigCompleted int64
	for ev := range events {
		switch {
		case ev.Done:
			s.emit(PullEventName, PullProgress{Ref: ref, Status: "success", Total: bigTotal, Completed: bigTotal, Done: true})
		case ev.Err != nil:
			msg := ev.Err.Error()
			if errors.Is(ev.Err, context.Canceled) || ctx.Err() != nil {
				msg = "canceled"
			}
			s.emit(PullEventName, PullProgress{Ref: ref, Status: "error", Error: msg})
		default:
			// Track the largest layer; report its progress. Smaller
			// trailing layers keep the bar at the big layer's state
			// rather than bouncing.
			if ev.Total >= bigTotal {
				bigTotal = ev.Total
				bigCompleted = ev.Completed
			}
			s.emit(PullEventName, PullProgress{Ref: ref, Status: ev.Status, Total: bigTotal, Completed: bigCompleted})
		}
	}
}

// Delete removes an installed model, after the frontend's confirm
// dialog (spec §8).
func (s *Service) Delete(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("model name is empty")
	}
	s.mu.Lock()
	pulling := s.pullRef
	s.mu.Unlock()
	if pulling != "" && normalizeRef(pulling) == normalizeRef(name) {
		return errors.New("that model is currently downloading — cancel the download instead")
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	return s.provider().Delete(ctx, name)
}

// Loaded reports models currently in memory (dev-mode status, M1.7;
// bound now for completeness).
func (s *Service) Loaded() ([]ollama.LoadedModel, error) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	return s.provider().PS(ctx)
}
