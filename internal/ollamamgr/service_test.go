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
	"masque/internal/sysinfo"
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
		if sm.DownloadBytes <= 0 || sm.MinRAMBytes < 0 {
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

// loadedFixture is one entry of a fake /api/ps response.
type loadedFixture struct {
	name     string
	sizeVRAM int64
}

// fakeOllama serves tags, a scripted pull stream, and an empty ps.
func fakeOllama(t *testing.T, installed []string, pullLines []string) *httptest.Server {
	t.Helper()
	return fakeOllamaWith(t, installed, pullLines, nil)
}

// fakeOllamaWith additionally reports loaded models on /api/ps.
func fakeOllamaWith(t *testing.T, installed []string, pullLines []string, loaded []loadedFixture) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/ps":
			models := make([]map[string]any, 0, len(loaded))
			for _, l := range loaded {
				models = append(models, map[string]any{"name": l.name, "model": l.name, "size": l.sizeVRAM, "size_vram": l.sizeVRAM})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"models": models})
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
	svc.totalRAM = func() uint64 { return 16 << 30 }                           // 16GB machine…
	svc.detect = discrete("NVIDIA GeForce RTX 2070 SUPER", 8192<<20, 6658<<20) // …with the tester's 8GB GPU

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
	// Usable budget: 6658 MiB free minus the 512 MiB margin ≈ 6.4 GB.
	// The 4B (≈4.4 GB) fits; the 8B (≈7.0 GB) and 12B (≈10 GB) spill
	// into the 16 GB of RAM; the 24B (≈18.3 GB) exceeds even that.
	if byName["Impish LLAMA"].Fit != FitGPU {
		t.Errorf("4B should fit the 2070's free VRAM: %+v", byName["Impish LLAMA"])
	}
	if byName["Stheno v3.2"].Fit != FitSplit || byName["Mag Mell R1"].Fit != FitSplit {
		t.Errorf("8B/12B should split across 6.4GB usable VRAM + 16GB RAM: %q %q",
			byName["Stheno v3.2"].Fit, byName["Mag Mell R1"].Fit)
	}
	if byName["Cydonia"].Fit != FitTight || byName["Cydonia"].Fits {
		t.Errorf("24B on 8GB VRAM + 16GB RAM should be tight, got %+v", byName["Cydonia"])
	}
	for _, m := range models {
		if m.Machine.VRAMTotalBytes != 8192<<20 || m.Machine.VRAMFreeBytes != 6658<<20 || m.RAMBytes != 16<<30 ||
			m.VRAMBytes != (6658<<20)-safetyMargin || m.NeedBytes <= m.DownloadBytes || m.Machine.GPUName == "" {
			t.Errorf("measurements not surfaced on %s: %+v", m.Name, m.Machine)
		}
	}
}

func TestRecommendedBigGPUFitsCydonia(t *testing.T) {
	// The bug this guards: 31GB RAM + RTX 4090 used to flag the 24B as
	// too big because only RAM was consulted.
	srv := fakeOllama(t, nil, nil)
	defer srv.Close()
	svc, _ := newTestService(t, srv.URL)
	svc.totalRAM = func() uint64 { return 31 << 30 }
	svc.detect = discrete("NVIDIA GeForce RTX 4090", 24564<<20, 23411<<20)

	models, err := svc.Recommended()
	if err != nil {
		t.Fatalf("Recommended: %v", err)
	}
	for _, m := range models {
		if m.Fit != FitGPU || !m.Fits {
			t.Errorf("%s should fit entirely on a 24GB GPU, got %q", m.Name, m.Fit)
		}
	}
}

func TestRecommendedRAMOnlyMachine(t *testing.T) {
	srv := fakeOllama(t, nil, nil)
	defer srv.Close()
	svc, _ := newTestService(t, srv.URL)
	svc.totalRAM = func() uint64 { return 8 << 30 }
	svc.detect = func() sysinfo.Report { return sysinfo.Report{} } // no GPU

	models, err := svc.Recommended()
	if err != nil {
		t.Fatalf("Recommended: %v", err)
	}
	byName := map[string]StarterModel{}
	for _, m := range models {
		byName[m.Name] = m
		if m.VRAMBytes != 0 {
			t.Errorf("unknown VRAM must surface as 0: %+v", m)
		}
	}
	if byName["Impish LLAMA"].Fit != FitSplit {
		t.Errorf("4B on 8GB RAM-only should be split (CPU), got %q", byName["Impish LLAMA"].Fit)
	}
	if byName["Cydonia"].Fit != FitTight || byName["Cydonia"].Fits {
		t.Errorf("24B on 8GB RAM-only should be tight, got %+v", byName["Cydonia"])
	}
}

func TestRecommendedUnknownHardwareFitsEverything(t *testing.T) {
	srv := fakeOllama(t, nil, nil)
	defer srv.Close()
	svc, _ := newTestService(t, srv.URL)
	svc.totalRAM = func() uint64 { return 0 }
	svc.detect = func() sysinfo.Report { return sysinfo.Report{Notes: []string{"probe failed"}} }

	models, err := svc.Recommended()
	if err != nil {
		t.Fatalf("Recommended: %v", err)
	}
	for _, m := range models {
		if !m.Fits || m.Fit != FitUnknown {
			t.Errorf("unknown hardware must not exclude %s: %+v", m.Name, m)
		}
	}
}

// discrete is a detect seam returning one discrete card.
func discrete(name string, total, free uint64) func() sysinfo.Report {
	return func() sysinfo.Report {
		return sysinfo.Report{GPUs: []sysinfo.GPU{{Vendor: sysinfo.VendorNVIDIA, Name: name, TotalBytes: total, FreeBytes: free, Source: "test"}}}
	}
}

func TestMachineBudgetsFromTheBestGPUOnly(t *testing.T) {
	svc, _ := newTestService(t, "http://127.0.0.1:1") // Ollama down: nothing resident
	svc.totalRAM = func() uint64 { return 31 << 30 }
	// This dev box: 4090 next to a 512 MiB APU. The APU must not add.
	svc.detect = func() sysinfo.Report {
		return sysinfo.Report{GPUs: []sysinfo.GPU{
			{Vendor: sysinfo.VendorNVIDIA, Name: "NVIDIA GeForce RTX 4090", TotalBytes: 24564 << 20, FreeBytes: 23411 << 20, Source: "nvidia-smi"},
			{Vendor: sysinfo.VendorAMD, Name: "AMD integrated graphics", TotalBytes: 512 << 20, FreeBytes: 492 << 20, Integrated: true, Source: "amdgpu sysfs card1"},
		}}
	}
	m := svc.Machine()
	if m.GPUKind != "discrete" || m.GPUName != "NVIDIA GeForce RTX 4090" {
		t.Errorf("pick = %s %q", m.GPUKind, m.GPUName)
	}
	if m.VRAMTotalBytes != 24564<<20 || m.VRAMFreeBytes != 23411<<20 {
		t.Errorf("total/free = %d/%d MiB, want 24564/23411", m.VRAMTotalBytes>>20, m.VRAMFreeBytes>>20)
	}
	if want := int64(23411<<20) - safetyMargin; m.VRAMBytes != want {
		t.Errorf("usable = %d MiB, want free minus margin %d MiB", m.VRAMBytes>>20, want>>20)
	}
	for _, want := range []string{"24564 MiB total", "23411 MiB free", "512 MiB total", "never summed"} {
		if !strings.Contains(m.Detail, want) {
			t.Errorf("Detail lacks %q: %s", want, m.Detail)
		}
	}

	// Detection failure: unknown kind, no budget, RAM still known.
	svc.detect = func() sysinfo.Report { return sysinfo.Report{Notes: []string{"nvidia-smi failed: exit status 1"}} }
	if m := svc.Machine(); m.GPUKind != "unknown" || m.VRAMBytes != 0 || m.RAMBytes != 31<<30 || !strings.Contains(m.Detail, "nvidia-smi failed") {
		t.Errorf("failed detection: %+v", m)
	}
}

func TestMachineCountsOllamaResidentModelsAsFree(t *testing.T) {
	// Ollama holds 6 GiB of a 8 GiB card: free reads 1.5 GiB, but
	// loading another model evicts the first, so the budget is ~7.5.
	srv := fakeOllamaWith(t, nil, nil, []loadedFixture{{name: "big:latest", sizeVRAM: 6 << 30}})
	defer srv.Close()
	svc, _ := newTestService(t, srv.URL)
	svc.totalRAM = func() uint64 { return 16 << 30 }
	svc.detect = discrete("NVIDIA GeForce RTX 2070 SUPER", 8192<<20, 1536<<20)
	m := svc.Machine()
	if m.VRAMFreeBytes != (1536<<20)+(6<<30) {
		t.Errorf("free should include Ollama's share: %d MiB", m.VRAMFreeBytes>>20)
	}
	if m.VRAMBytes != m.VRAMFreeBytes-safetyMargin {
		t.Errorf("usable = %d MiB", m.VRAMBytes>>20)
	}
}

func TestRecommendedBadgeFollowsFit(t *testing.T) {
	srv := fakeOllama(t, nil, nil)
	defer srv.Close()
	svc, _ := newTestService(t, srv.URL)

	// The tester's box: 2070 Super with 6.5 GB free, 16 GB RAM. The
	// old static badge pointed at the 12B; it must now be a model that
	// fits in the usable 6 GB.
	svc.totalRAM = func() uint64 { return 16 << 30 }
	svc.detect = discrete("NVIDIA GeForce RTX 2070 SUPER", 8192<<20, 6658<<20)
	models, err := svc.Recommended()
	if err != nil {
		t.Fatal(err)
	}
	var rec *StarterModel
	for i := range models {
		if models[i].Recommended {
			if rec != nil {
				t.Fatal("more than one recommended model")
			}
			rec = &models[i]
		}
	}
	if rec == nil {
		t.Fatal("no recommended model")
	}
	if rec.Fit != FitGPU {
		t.Errorf("recommended %s has fit %q, must fit on the GPU", rec.Name, rec.Fit)
	}
	if uint64(rec.NeedBytes) > uint64(rec.VRAMBytes) {
		t.Errorf("recommended %s needs %d MiB but budget is %d MiB", rec.Name, rec.NeedBytes>>20, rec.VRAMBytes>>20)
	}
	if rec.Name == "Mag Mell R1" {
		t.Error("the 12B must not be recommended on an 8 GB card")
	}

	// 4090: the biggest model that fits on the GPU is recommended.
	svc.totalRAM = func() uint64 { return 31 << 30 }
	svc.detect = discrete("NVIDIA GeForce RTX 4090", 24564<<20, 23411<<20)
	models, _ = svc.Recommended()
	for _, m := range models {
		if m.Recommended && m.Name != "Cydonia" {
			t.Errorf("24 GB card should recommend the 24B, got %s", m.Name)
		}
	}

	// Nothing fits on the GPU (integrated only, 8 GB RAM): the smallest.
	svc.totalRAM = func() uint64 { return 8 << 30 }
	svc.detect = func() sysinfo.Report {
		return sysinfo.Report{GPUs: []sysinfo.GPU{{Vendor: sysinfo.VendorAMD, Name: "AMD integrated graphics", TotalBytes: 2 << 30, Integrated: true}}}
	}
	models, _ = svc.Recommended()
	for _, m := range models {
		if m.Recommended && m.Name != "Impish LLAMA" {
			t.Errorf("iGPU-only box should recommend the smallest, got %s", m.Name)
		}
		if m.Machine.GPUKind != "integrated" || m.VRAMBytes != 0 {
			t.Errorf("iGPU-only must budget from RAM: %+v", m.Machine)
		}
	}

	// Unmeasurable: the manifest default keeps the badge.
	svc.totalRAM = func() uint64 { return 0 }
	svc.detect = func() sysinfo.Report { return sysinfo.Report{Notes: []string{"probe failed"}} }
	models, _ = svc.Recommended()
	for _, m := range models {
		if m.Recommended != (m.Name == "Mag Mell R1") {
			t.Errorf("unknown hardware: badge on %s = %v", m.Name, m.Recommended)
		}
	}
}

func TestRecommendedAppleSilicon(t *testing.T) {
	srv := fakeOllama(t, nil, nil)
	defer srv.Close()
	svc, _ := newTestService(t, srv.URL)
	svc.detect = func() sysinfo.Report {
		return sysinfo.Report{GPUs: []sysinfo.GPU{{Vendor: sysinfo.VendorApple, Name: "Apple M3", Integrated: true, Source: "sysctl"}}}
	}
	fits := func(ram uint64) map[string]Fit {
		svc.totalRAM = func() uint64 { return ram }
		models, err := svc.Recommended()
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]Fit{}
		for _, m := range models {
			out[m.Name] = m.Fit
			if !m.Machine.Unified || m.Machine.GPUKind != "unified" {
				t.Errorf("apple must be unified: %+v", m.Machine)
			}
		}
		return out
	}
	// 16 GB: GPU share is 2/3 ≈ 10.7 GB minus margin ≈ 10.2 GB. The
	// 12B needs ≈ 10.0 GB → fits; the 24B does not and cannot spill.
	f16 := fits(16 << 30)
	if f16["Impish LLAMA"] != FitGPU || f16["Stheno v3.2"] != FitGPU {
		t.Errorf("16 GB: small models should fit: %v", f16)
	}
	if f16["Cydonia"] != FitTight {
		t.Errorf("16 GB: 24B must be tight, got %q", f16["Cydonia"])
	}
	// 64 GB: 3/4 = 48 GB; everything fits.
	for name, fit := range fits(64 << 30) {
		if fit != FitGPU {
			t.Errorf("64 GB: %s = %q", name, fit)
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
