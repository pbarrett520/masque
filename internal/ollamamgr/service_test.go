package ollamamgr

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"masque/internal/store"
)

// collectEmits records emitted events, safe for concurrent emitters.
type collectEmits struct {
	mu     sync.Mutex
	events []PullProgress
}

func (c *collectEmits) emit(event string, args ...any) {
	if event != PullEventName || len(args) != 1 {
		return
	}
	p, ok := args[0].(PullProgress)
	if !ok {
		return
	}
	c.mu.Lock()
	c.events = append(c.events, p)
	c.mu.Unlock()
}

func (c *collectEmits) snapshot() []PullProgress {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]PullProgress(nil), c.events...)
}

func newTestService(t *testing.T, baseURL string) (*Service, *collectEmits) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "masque.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if baseURL != "" {
		raw, _ := json.Marshal(baseURL)
		if err := st.SetSetting(settingBaseURL, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	c := &collectEmits{}
	return NewService(st, c.emit), c
}

func TestManifestParsesAndIsSane(t *testing.T) {
	var m manifest
	if err := json.Unmarshal(starterManifest, &m); err != nil {
		t.Fatalf("embedded manifest is invalid JSON: %v", err)
	}
	if len(m.Models) < 3 {
		t.Fatalf("manifest has %d models, want at least 3 (spec §8: 3–5)", len(m.Models))
	}
	recommended := 0
	for _, sm := range m.Models {
		if sm.Ref == "" || sm.Name == "" || sm.Description == "" || sm.Params == "" {
			t.Errorf("manifest entry missing fields: %+v", sm)
		}
		if sm.DownloadBytes <= 0 || sm.MinRAMBytes <= 0 {
			t.Errorf("manifest entry %s has bad sizes: %+v", sm.Ref, sm)
		}
		if sm.Recommended {
			recommended++
		}
	}
	if recommended != 1 {
		t.Errorf("manifest should mark exactly one model recommended, got %d", recommended)
	}
}

func TestStatusReachableAndNot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"version":"0.30.6"}`))
	}))
	defer srv.Close()

	svc, _ := newTestService(t, srv.URL)
	st := svc.Status()
	if !st.Reachable || st.Version != "0.30.6" || st.BaseURL != srv.URL {
		t.Errorf("Status = %+v", st)
	}

	down, _ := newTestService(t, "http://127.0.0.1:1")
	st = down.Status()
	if st.Reachable || st.Error == "" {
		t.Errorf("Status against dead endpoint = %+v", st)
	}
}

// fakeOllama serves tags and a scripted pull stream.
func fakeOllama(t *testing.T, installed []string, pullLines []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			models := make([]map[string]any, 0, len(installed))
			for _, name := range installed {
				models = append(models, map[string]any{"name": name, "size": 1, "capabilities": []string{"completion"}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"models": models})
		case "/api/pull":
			for _, l := range pullLines {
				_, _ = w.Write([]byte(l + "\n"))
			}
		case "/api/delete":
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
}

func TestRecommendedAnnotatesInstalledAndFits(t *testing.T) {
	srv := fakeOllama(t, []string{
		// Case differs from the manifest ref: Ollama lowercases names.
		"hammerai/mn-mag-mell-r1:12b-q4_K_M",
	}, nil)
	defer srv.Close()

	svc, _ := newTestService(t, srv.URL)
	svc.totalRAM = func() uint64 { return 16 << 30 } // 16GB machine

	models, err := svc.Recommended()
	if err != nil {
		t.Fatalf("Recommended: %v", err)
	}
	byName := map[string]StarterModel{}
	for _, m := range models {
		byName[m.Name] = m
	}
	if !byName["Mag Mell R1"].Installed {
		t.Errorf("Mag Mell should be installed: %+v", byName["Mag Mell R1"])
	}
	if byName["Stheno v3.2"].Installed {
		t.Errorf("Stheno should not be installed")
	}
	if !byName["Impish LLAMA"].Fits || !byName["Stheno v3.2"].Fits || !byName["Mag Mell R1"].Fits {
		t.Errorf("small/mid models should fit 16GB: %+v", models)
	}
	if byName["Cydonia"].Fits {
		t.Errorf("24B should not fit a 16GB machine")
	}
}

func TestRecommendedUnknownRAMFitsEverything(t *testing.T) {
	srv := fakeOllama(t, nil, nil)
	defer srv.Close()
	svc, _ := newTestService(t, srv.URL)
	svc.totalRAM = func() uint64 { return 0 }

	models, err := svc.Recommended()
	if err != nil {
		t.Fatalf("Recommended: %v", err)
	}
	for _, m := range models {
		if !m.Fits {
			t.Errorf("unknown RAM must not exclude %s", m.Name)
		}
	}
}

func TestRecommendedSurvivesDeadEndpoint(t *testing.T) {
	svc, _ := newTestService(t, "http://127.0.0.1:1")
	models, err := svc.Recommended()
	if err != nil {
		t.Fatalf("Recommended should degrade gracefully, got %v", err)
	}
	for _, m := range models {
		if m.Installed {
			t.Errorf("nothing can be installed on a dead endpoint: %+v", m)
		}
	}
}

func TestPullEmitsProgressAndSuccess(t *testing.T) {
	srv := fakeOllama(t, nil, []string{
		`{"status":"pulling manifest"}`,
		`{"status":"pulling ab1c","digest":"sha256:ab1c","total":1000,"completed":400}`,
		`{"status":"pulling small","digest":"sha256:tiny","total":10,"completed":10}`,
		`{"status":"success"}`,
	})
	defer srv.Close()

	svc, emits := newTestService(t, srv.URL)
	if err := svc.Pull("test/model:q4"); err != nil {
		t.Fatalf("Pull: %v", err)
	}
	waitFor(t, func() bool {
		evs := emits.snapshot()
		return len(evs) > 0 && evs[len(evs)-1].Done
	})
	evs := emits.snapshot()
	last := evs[len(evs)-1]
	if last.Error != "" || !last.Done {
		t.Errorf("terminal event: %+v", last)
	}
	// The tiny trailing layer must not shrink reported progress: its
	// event still reports the big layer's totals.
	for _, ev := range evs {
		if ev.Status == "pulling small" && ev.Total != 1000 {
			t.Errorf("progress ran backwards on small layer: %+v", ev)
		}
	}
	if svc.PullInFlight() != "" {
		t.Errorf("pull slot not cleared")
	}
}

func TestPullRejectsConcurrent(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/pull" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"status":"pulling manifest"}` + "\n"))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
		_, _ = w.Write([]byte(`{"status":"success"}` + "\n"))
	}))
	defer srv.Close()
	defer close(release)

	svc, emits := newTestService(t, srv.URL)
	if err := svc.Pull("first"); err != nil {
		t.Fatalf("first Pull: %v", err)
	}
	err := svc.Pull("second")
	if err == nil || !strings.Contains(err.Error(), "first") {
		t.Fatalf("second Pull = %v, want busy error naming first", err)
	}
	if got := svc.PullInFlight(); got != "first" {
		t.Errorf("PullInFlight = %q", got)
	}
	svc.CancelPull()
	waitFor(t, func() bool {
		evs := emits.snapshot()
		return len(evs) > 0 && evs[len(evs)-1].Error == "canceled"
	})
}

func TestDeleteRefusesInFlightPull(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"pulling manifest"}` + "\n"))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	svc, _ := newTestService(t, srv.URL)
	if err := svc.Pull("Some/Model:tag"); err != nil {
		t.Fatalf("Pull: %v", err)
	}
	defer svc.CancelPull()
	if err := svc.Delete("some/model:tag"); err == nil || !strings.Contains(err.Error(), "downloading") {
		t.Errorf("Delete during pull = %v, want downloading error", err)
	}
}

func TestNormalizeRef(t *testing.T) {
	cases := []struct{ in, want string }{
		{"HammerAI/mn-mag-mell-r1:12b-q4_K_M", "hammerai/mn-mag-mell-r1:12b-q4_k_m"},
		{"llama3", "llama3:latest"},
		{" m:latest ", "m:latest"},
	}
	for _, c := range cases {
		if got := normalizeRef(c.in); got != c.want {
			t.Errorf("normalizeRef(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// waitFor polls cond until true or a timeout fails the test.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
