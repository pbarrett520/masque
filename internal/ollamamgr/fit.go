package ollamamgr

import "masque/internal/sysinfo"

// Fit is how well a starter model fits the machine it is being offered
// on. The roster never hides a model on account of fit — it annotates.
type Fit string

const (
	// FitGPU: the whole model plus working room fits in usable GPU
	// memory (or, on unified memory, in the GPU's share of RAM).
	FitGPU Fit = "gpu"
	// FitSplit: it fits VRAM plus a usable slice of system RAM, so
	// Ollama will offload part of it to the CPU. Runs, but slower.
	FitSplit Fit = "split"
	// FitTight: even VRAM plus RAM headroom is short. Likely to swap,
	// crash on load, or crawl — the red warning.
	FitTight Fit = "tight"
	// FitUnknown: neither RAM nor VRAM could be measured, so nothing
	// was gated. Rendered like FitGPU.
	FitUnknown Fit = "unknown"
)

// Memory model. A model needs its weights plus working memory, and
// the GPU has less room than its spec-sheet size. Every figure here
// errs toward "recommend the smaller model": a slow or failed first
// load is the worst first impression.
const (
	// kvCacheRatio is the KV cache and compute buffers as a share of
	// the weights, sized for ~8k context on a Q4_K_M model. It scales
	// with weights because hidden size and layer count do.
	kvCacheRatio = 0.20
	// runtimeOverhead is the fixed cost of a CUDA/ROCm/Metal context
	// plus the llama.cpp compute graph, independent of model size.
	runtimeOverhead = 1 << 30
	// safetyMargin absorbs what the probes can't see: allocator
	// fragmentation, the gap between what nvidia-smi reports free and
	// what a single contiguous allocation can get, and VRAM the desktop
	// grabs between probe and load. 512 MiB is about one browser tab
	// with hardware acceleration on.
	safetyMargin = 512 << 20
	// displayReserve stands in for "free" when a source only reports
	// total: a compositor, browser, and a few windows typically pin
	// about a GiB of VRAM on a desktop session.
	displayReserve = 1 << 30
	// ramUsableRatio is the share of system RAM we let a model spill
	// into: the rest is the OS, the browser, and the app itself.
	ramUsableRatio = 0.6
)

// memoryNeed is the memory a model needs to run: weights plus KV/compute
// room plus the runtime, floored by the manifest's optional minRamBytes
// override. weights is the Q4_K_M download size.
func memoryNeed(weights, floor int64) uint64 {
	if weights < 0 {
		weights = 0
	}
	need := uint64(weights) + uint64(float64(weights)*kvCacheRatio) + runtimeOverhead
	if floor > 0 && uint64(floor) > need {
		need = uint64(floor)
	}
	return need
}

// unifiedBudget is how much of Apple Silicon's unified memory the GPU
// may take by default: macOS caps GPU wired memory (iogpu.wired_limit)
// at roughly two thirds of RAM on machines up to 36 GB and three
// quarters above that. This is the same rule Ollama applies, so the
// budget matches what will actually load. Users can raise the sysctl
// cap, but the default is what a first run gets.
func unifiedBudget(ram uint64) uint64 {
	switch {
	case ram == 0:
		return 0
	case ram <= 36<<30:
		return ram * 2 / 3
	default:
		return ram * 3 / 4
	}
}

// usableVRAM is the GPU budget the fit check compares against, from the
// one device Best picked. ollamaResident is VRAM Ollama's own loaded
// models hold right now: it counts as free, because Ollama evicts them
// to load the next model, and a user who opens Settings mid-chat must
// not see every model turn red.
//
//   - discrete: free memory (plus Ollama's share) when the source
//     reports it, else total minus displayReserve; then minus the
//     safety margin, never above total
//   - unified: the macOS default GPU share of RAM, minus the margin
//   - integrated, none, unknown: 0 — budget from RAM alone
func usableVRAM(pick sysinfo.Pick, ram, ollamaResident uint64) uint64 {
	var base uint64
	switch pick.Kind {
	case sysinfo.KindDiscrete:
		total := pick.GPU.TotalBytes
		if free := pick.GPU.FreeBytes; free > 0 {
			base = free + ollamaResident
		} else if total > displayReserve {
			base = total - displayReserve
		}
		if base > total {
			base = total
		}
	case sysinfo.KindUnified:
		base = unifiedBudget(ram)
	default:
		return 0
	}
	if base <= safetyMargin {
		return 0
	}
	return base - safetyMargin
}

// classifyFit tiers need against the budget. vram is the usable GPU
// figure from usableVRAM and ram is total RAM; 0 means unknown, and
// an unknown figure is never treated as zero capacity:
//
//   - both unknown → FitUnknown (nothing gated)
//   - unified (Apple Silicon) → need ≤ vram is GPU; there is no
//     separate pool to spill into, so anything else is tight
//   - need ≤ vram → FitGPU
//   - need ≤ vram + ramUsableRatio × RAM → FitSplit (vram counts as 0
//     when there is no GPU budget, which is the RAM-only path)
//   - vram known but RAM unknown and need > vram → FitSplit: we know it
//     spills, not whether RAM covers it, so the soft note is honest
//   - otherwise → FitTight
func classifyFit(need, vram, ram uint64, unified bool) Fit {
	vramKnown, ramKnown := vram > 0, ram > 0
	switch {
	case !vramKnown && !ramKnown:
		return FitUnknown
	case unified:
		if vramKnown && need <= vram {
			return FitGPU
		}
		return FitTight
	case vramKnown && need <= vram:
		return FitGPU
	case !ramKnown:
		return FitSplit
	case float64(need) <= float64(vram)+float64(ram)*ramUsableRatio:
		return FitSplit
	default:
		return FitTight
	}
}

// chooseRecommended picks which roster entry gets the Recommended
// badge for this machine: the largest model that fits entirely on the
// GPU; failing that, the smallest model (it will at least run); and
// when nothing could be measured, the manifest's own default. Returns
// an index into models, or -1 for an empty roster.
func chooseRecommended(models []StarterModel) int {
	if len(models) == 0 {
		return -1
	}
	best, smallest, manifest := -1, 0, -1
	for i, m := range models {
		if m.Fit == FitUnknown && manifest == -1 && m.Recommended {
			manifest = i
		}
		if m.Fit == FitGPU && (best == -1 || m.DownloadBytes > models[best].DownloadBytes) {
			best = i
		}
		if m.DownloadBytes < models[smallest].DownloadBytes {
			smallest = i
		}
	}
	switch {
	case manifest != -1:
		return manifest
	case best != -1:
		return best
	default:
		return smallest
	}
}
