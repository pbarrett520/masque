package ollamamgr

// Fit is how well a starter model fits the machine it is being offered
// on. The roster never hides a model on account of fit — it annotates.
type Fit string

const (
	// FitGPU: the whole model plus working room fits in discrete VRAM
	// (or, on unified memory, in the GPU's share of RAM). No warning.
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

const (
	// overheadFixed is the flat KV-cache/runtime margin added to the
	// weights; overheadRatio is the proportional part. Together they
	// approximate a Q4_K_M model at an 8k-ish context.
	overheadFixed = 1536 << 20 // 1.5 GiB
	overheadRatio = 0.20
	// ramUsableRatio is the share of system RAM we let a model spill
	// into: the rest is the OS, the browser, and the app itself.
	ramUsableRatio = 0.6
	// unifiedGPURatio is the share of RAM Metal can address on Apple
	// Silicon.
	unifiedGPURatio = 0.75
)

// memoryNeed is the memory a model needs to run: weights plus the
// KV/overhead margin, floored by the manifest's optional minRamBytes
// override. weights is the Q4_K_M download size.
func memoryNeed(weights, floor int64) uint64 {
	if weights < 0 {
		weights = 0
	}
	need := uint64(weights) + uint64(float64(weights)*overheadRatio) + overheadFixed
	if floor > 0 && uint64(floor) > need {
		need = uint64(floor)
	}
	return need
}

// classifyFit tiers need against the measured memory. vram and ram are
// 0 when unknown; an unknown figure is never treated as zero capacity:
//
//   - both unknown → FitUnknown (nothing gated)
//   - unified (Apple Silicon) → GPU budget is unifiedGPURatio × RAM;
//     there is no separate pool to spill into, so the rest is tight
//   - need ≤ VRAM → FitGPU
//   - need ≤ VRAM + ramUsableRatio × RAM → FitSplit (VRAM counts as 0
//     when unknown, which is the RAM-only path)
//   - VRAM known but RAM unknown and need > VRAM → FitSplit: we know it
//     spills, not whether RAM covers it, so the soft note is honest
//   - otherwise → FitTight
func classifyFit(need, vram, ram uint64, unified bool) Fit {
	vramKnown, ramKnown := vram > 0, ram > 0
	switch {
	case !vramKnown && !ramKnown:
		return FitUnknown
	case unified && ramKnown:
		if float64(need) <= float64(ram)*unifiedGPURatio {
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
