package ollamamgr

import "testing"

const gib = 1 << 30

func TestMemoryNeed(t *testing.T) {
	// 14 GB of weights → 14 + 2.8 + 1.5 GiB.
	want := uint64(14e9) + uint64(14e9*0.2) + 1536<<20
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
	if got := memoryNeed(-5, 0); got != 1536<<20 {
		t.Errorf("negative weights should clamp: %d", got)
	}
}

func TestClassifyFit(t *testing.T) {
	need14 := memoryNeed(14e9, 0) // ≈18.4 GB for a 14 GB Q4_K_M 24B
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
		{"unified 32GB: 14GB model fits Metal share", need14, 0, 32 * gib, true, FitGPU},
		{"unified 16GB: 14GB model is tight (no pool to spill to)", need14, 0, 16 * gib, true, FitTight},
		{"unified, RAM unknown", need14, 0, 0, true, FitUnknown},
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
