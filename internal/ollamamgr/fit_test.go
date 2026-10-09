package ollamamgr

import (
	"testing"

	"masque/internal/sysinfo"
)

const (
	gib = 1 << 30
	mib = 1 << 20
)

func TestMemoryNeed(t *testing.T) {
	// 14 GB of weights → 14 + 2.8 GB KV + 1 GiB runtime.
	want := uint64(14e9) + uint64(14e9*kvCacheRatio) + runtimeOverhead
	if got := memoryNeed(14e9, 0); got != want {
		t.Errorf("memoryNeed(14e9, 0) = %d, want %d", got, want)
	}
	// A manifest floor above the computed figure wins…
	if got := memoryNeed(14e9, 40*gib); got != 40*gib {
		t.Errorf("floor not applied: %d", got)
	}
	// …a floor below it is ignored.
	if got := memoryNeed(14e9, 1*gib); got != want {
		t.Errorf("low floor should be ignored: %d", got)
	}
	if got := memoryNeed(-5, 0); got != runtimeOverhead {
		t.Errorf("negative weights should clamp: %d", got)
	}
}

func TestUnifiedBudget(t *testing.T) {
	cases := map[uint64]uint64{
		0:        0,
		16 * gib: 16 * gib * 2 / 3,
		36 * gib: 36 * gib * 2 / 3,
		64 * gib: 64 * gib * 3 / 4,
	}
	for ram, want := range cases {
		if got := unifiedBudget(ram); got != want {
			t.Errorf("unifiedBudget(%d GiB) = %d, want %d", ram/gib, got, want)
		}
	}
}

func TestUsableVRAM(t *testing.T) {
	disc := func(total, free uint64) sysinfo.Pick {
		return sysinfo.Pick{Kind: sysinfo.KindDiscrete, GPU: sysinfo.GPU{TotalBytes: total, FreeBytes: free}}
	}
	cases := []struct {
		name     string
		pick     sysinfo.Pick
		ram      uint64
		resident uint64
		want     uint64
	}{
		{"tester 2070S: free minus margin", disc(8192*mib, 6658*mib), 16 * gib, 0, 6658*mib - safetyMargin},
		{"4090: free minus margin", disc(24564*mib, 23411*mib), 31 * gib, 0, 23411*mib - safetyMargin},
		{"free unknown: total minus display reserve and margin", disc(8*gib, 0), 16 * gib, 0, 8*gib - displayReserve - safetyMargin},
		{"ollama resident counts as free", disc(8192*mib, 1536*mib), 16 * gib, 6 * gib, 1536*mib + 6*gib - safetyMargin},
		{"resident never pushes past total", disc(8*gib, 7*gib), 16 * gib, 4 * gib, 8*gib - safetyMargin},
		{"tiny card: nothing usable", disc(1*gib, 300*mib), 16 * gib, 0, 0},
		{"unified 16 GB", sysinfo.Pick{Kind: sysinfo.KindUnified}, 16 * gib, 0, 16*gib*2/3 - safetyMargin},
		{"unified 64 GB", sysinfo.Pick{Kind: sysinfo.KindUnified}, 64 * gib, 0, 64*gib*3/4 - safetyMargin},
		{"unified RAM unknown", sysinfo.Pick{Kind: sysinfo.KindUnified}, 0, 0, 0},
		{"integrated: budget from RAM", sysinfo.Pick{Kind: sysinfo.KindIntegrated, GPU: sysinfo.GPU{TotalBytes: 2 * gib}}, 16 * gib, 0, 0},
		{"none", sysinfo.Pick{Kind: sysinfo.KindNone}, 16 * gib, 0, 0},
		{"unknown never guesses", sysinfo.Pick{Kind: sysinfo.KindUnknown, GPU: sysinfo.GPU{Name: "NVIDIA GPU"}}, 16 * gib, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := usableVRAM(c.pick, c.ram, c.resident); got != c.want {
				t.Errorf("usableVRAM = %d MiB, want %d MiB", got/mib, c.want/mib)
			}
		})
	}
}

func TestClassifyFit(t *testing.T) {
	need14 := memoryNeed(14e9, 0) // ≈17.8 GB for a 14 GB Q4_K_M 24B
	cases := []struct {
		name    string
		need    uint64
		vram    uint64
		ram     uint64
		unified bool
		want    Fit
	}{
		{"a: 24GB VRAM + 31GB RAM, 14GB model", need14, 24 * gib, 31 * gib, false, FitGPU},
		{"b: 8GB VRAM + 16GB RAM, 14GB model", need14, 8 * gib, 16 * gib, false, FitSplit},
		{"c: no GPU, 8GB RAM, 14GB model", need14, 0, 8 * gib, false, FitTight},
		{"d: VRAM unknown, 32GB RAM gates on RAM only", need14, 0, 32 * gib, false, FitSplit},
		{"d: VRAM unknown, 16GB RAM gates on RAM only", need14, 0, 16 * gib, false, FitTight},
		{"e: both unknown fits everything", need14, 0, 0, false, FitUnknown},
		{"exactly VRAM is gpu", 24 * gib, 24 * gib, 32 * gib, false, FitGPU},
		{"one byte over VRAM is split", 24*gib + 1, 24 * gib, 32 * gib, false, FitSplit},
		{"exactly VRAM + 0.6 RAM is split", 24*gib + 6*gib, 24 * gib, 10 * gib, false, FitSplit},
		{"one byte over the split budget is tight", 24*gib + 6*gib + 1, 24 * gib, 10 * gib, false, FitTight},
		{"RAM unknown, fits VRAM", need14, 24 * gib, 0, false, FitGPU},
		{"RAM unknown, over VRAM is soft-noted", need14, 8 * gib, 0, false, FitSplit},
		{"unified: fits the GPU share", need14, 20 * gib, 32 * gib, true, FitGPU},
		{"unified: over the share is tight (no pool to spill to)", need14, 10 * gib, 16 * gib, true, FitTight},
		{"unified, nothing known", need14, 0, 0, true, FitUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classifyFit(c.need, c.vram, c.ram, c.unified); got != c.want {
				t.Errorf("classifyFit(need=%d, vram=%d, ram=%d, unified=%v) = %q, want %q",
					c.need, c.vram, c.ram, c.unified, got, c.want)
			}
		})
	}
}

// roster mirrors the shipped manifest's sizes so the recommendation
// tests track real numbers.
func roster(fits ...Fit) []StarterModel {
	sizes := []int64{2780000000, 4920000000, 7480000000, 14330000000}
	names := []string{"Impish LLAMA", "Stheno v3.2", "Mag Mell R1", "Cydonia"}
	out := make([]StarterModel, len(sizes))
	for i := range sizes {
		out[i] = StarterModel{Name: names[i], DownloadBytes: sizes[i], Recommended: i == 2, Fit: fits[i]}
	}
	return out
}

func TestChooseRecommended(t *testing.T) {
	cases := []struct {
		name string
		fits []Fit
		want string
	}{
		{"tester: only the 4B fits the GPU", []Fit{FitGPU, FitSplit, FitSplit, FitTight}, "Impish LLAMA"},
		{"8 GB card with room: largest gpu fit", []Fit{FitGPU, FitGPU, FitSplit, FitSplit}, "Stheno v3.2"},
		{"24 GB card: everything fits, biggest wins", []Fit{FitGPU, FitGPU, FitGPU, FitGPU}, "Cydonia"},
		{"nothing fits the GPU: smallest", []Fit{FitSplit, FitSplit, FitTight, FitTight}, "Impish LLAMA"},
		{"everything tight: still the smallest", []Fit{FitTight, FitTight, FitTight, FitTight}, "Impish LLAMA"},
		{"unknown hardware: manifest default", []Fit{FitUnknown, FitUnknown, FitUnknown, FitUnknown}, "Mag Mell R1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			models := roster(c.fits...)
			i := chooseRecommended(models)
			if i < 0 || models[i].Name != c.want {
				t.Errorf("chose %d, want %s", i, c.want)
			}
		})
	}
	if chooseRecommended(nil) != -1 {
		t.Error("empty roster")
	}
}

// TestTesterConfigurationEndToEnd is the reported bug as a pure
// computation: 2070 Super (8192 MiB, 6658 free) beside a 2 GiB APU,
// 16 GB RAM. The budget must come from the 2070 alone, and the
// recommended model must fit it.
func TestTesterConfigurationEndToEnd(t *testing.T) {
	report := sysinfo.Report{GPUs: []sysinfo.GPU{
		{Vendor: sysinfo.VendorNVIDIA, Name: "NVIDIA GeForce RTX 2070 SUPER", TotalBytes: 8192 * mib, FreeBytes: 6658 * mib, Source: "nvidia-smi"},
		{Vendor: sysinfo.VendorAMD, Name: "AMD integrated graphics", TotalBytes: 2 * gib, FreeBytes: 1900 * mib, Integrated: true, Source: "amdgpu sysfs card1"},
	}}
	pick := report.Best()
	if pick.GPU.TotalBytes != 8192*mib {
		t.Fatalf("picked %d MiB, want the 2070's 8192 (the bug summed to 10240)", pick.GPU.TotalBytes/mib)
	}
	usable := usableVRAM(pick, 16*gib, 0)
	if usable >= 8192*mib {
		t.Fatalf("usable %d MiB must be below the card's total", usable/mib)
	}
	models := roster(FitUnknown, FitUnknown, FitUnknown, FitUnknown)
	for i := range models {
		need := memoryNeed(models[i].DownloadBytes, 0)
		models[i].NeedBytes = int64(need)
		models[i].Fit = classifyFit(need, usable, 16*gib, false)
	}
	rec := models[chooseRecommended(models)]
	if rec.Fit != FitGPU || uint64(rec.NeedBytes) > usable {
		t.Errorf("recommended %s: fit=%s need=%d MiB usable=%d MiB", rec.Name, rec.Fit, rec.NeedBytes/mib, usable/mib)
	}
	if rec.Name == "Mag Mell R1" {
		t.Error("12B recommended on an 8 GB card")
	}
	t.Logf("tester box: usable %d MiB → recommended %s (need %d MiB)", usable/mib, rec.Name, rec.NeedBytes/mib)
}
