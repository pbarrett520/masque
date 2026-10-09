// Package sysinfo answers two questions: how much RAM does this machine
// have, and what GPU memory can a model actually use? The curated
// model list (dev spec §8) tiers starter models by fit from the
// answers. Implementations are per-OS via x/sys plus, for GPUs, vendor
// tools, sysfs, and the registry — no cgo, per project convention.
//
// GPU memory is reported per device and never summed: two cards can't
// pool VRAM for a single model, and an integrated GPU's carve-out next
// to a discrete card is not usable at all. Callers pick one GPU with
// Report.Best.
package sysinfo

import (
	"fmt"
	"strings"
)

// TotalRAM returns total physical memory in bytes, or 0 if it cannot be
// determined. Callers must treat 0 as "unknown" and skip fit filtering
// rather than concluding nothing fits.
func TotalRAM() uint64 {
	return totalRAM()
}

// GPU vendors as reported in GPU.Vendor.
const (
	VendorNVIDIA  = "nvidia"
	VendorAMD     = "amd"
	VendorIntel   = "intel"
	VendorApple   = "apple"
	VendorUnknown = "unknown"
)

// GPU is one graphics device as detected. Memory figures are exact
// bytes from the source; 0 means unknown, never "none".
type GPU struct {
	Vendor string `json:"vendor"`
	Name   string `json:"name"`
	// TotalBytes is the device's dedicated memory (an integrated
	// part's carve-out, a discrete card's VRAM). 0 = unknown.
	TotalBytes uint64 `json:"totalBytes"`
	// FreeBytes is what was unallocated at probe time, when the source
	// exposes it (nvidia-smi, amdgpu sysfs). 0 = unknown.
	FreeBytes uint64 `json:"freeBytes"`
	// Integrated marks parts that share system memory (iGPUs, Apple
	// Silicon). Their TotalBytes is not a budget for inference.
	Integrated bool `json:"integrated"`
	// Source names the probe that produced the entry, for dev mode.
	Source string `json:"source"`
}

// String renders the exact figures for logs and dev mode.
func (g GPU) String() string {
	kind := "discrete"
	if g.Integrated {
		kind = "integrated"
	}
	s := fmt.Sprintf("%s (%s, %s", g.Name, g.Vendor, kind)
	if g.TotalBytes > 0 {
		s += fmt.Sprintf(", %d MiB total", g.TotalBytes>>20)
	} else {
		s += ", memory unknown"
	}
	if g.FreeBytes > 0 {
		s += fmt.Sprintf(", %d MiB free", g.FreeBytes>>20)
	}
	return s + ", via " + g.Source + ")"
}

// Report is everything the probes found, including what went wrong.
type Report struct {
	GPUs []GPU `json:"gpus"`
	// Notes record probe failures and skipped devices ("nvidia-smi not
	// found", "intel GPU memory not readable") so dev mode and logs can
	// show why a figure is unknown.
	Notes []string `json:"notes"`
}

// String renders the whole report on one line, exact units.
func (r Report) String() string {
	parts := make([]string, 0, len(r.GPUs)+1)
	for _, g := range r.GPUs {
		parts = append(parts, g.String())
	}
	if len(parts) == 0 {
		parts = append(parts, "no GPUs detected")
	}
	if len(r.Notes) > 0 {
		parts = append(parts, "notes: "+strings.Join(r.Notes, "; "))
	}
	return strings.Join(parts, "; ")
}

// DetectGPUs probes the machine. It never fails: problems land in
// Report.Notes and the GPU list is whatever could be read.
func DetectGPUs() Report {
	return detectGPUs()
}

// Kind classifies what Best settled on.
type Kind string

const (
	// KindDiscrete: a dedicated card with known memory; GPU is it.
	KindDiscrete Kind = "discrete"
	// KindUnified: Apple Silicon; the GPU budget is a share of RAM.
	KindUnified Kind = "unified"
	// KindIntegrated: only an iGPU exists; treat as CPU/shared-memory
	// inference and budget from RAM.
	KindIntegrated Kind = "integrated"
	// KindNone: probes worked and found no GPU at all.
	KindNone Kind = "none"
	// KindUnknown: a GPU may exist but its memory could not be read
	// (tool missing, unsupported vendor). Never guess high: callers
	// budget from RAM only.
	KindUnknown Kind = "unknown"
)

// Pick is the one device the fit check budgets against.
type Pick struct {
	Kind Kind `json:"kind"`
	GPU  GPU  `json:"gpu"` // zero value for KindNone
	// Why explains the choice in one line (dev mode).
	Why string `json:"why"`
}

// Best chooses the single GPU to budget against: the discrete card with
// the most memory, ignoring integrated parts whenever a discrete one
// exists. Memory is never summed across devices.
func (r Report) Best() Pick {
	var best *GPU
	unknownDiscrete := ""
	for i := range r.GPUs {
		g := &r.GPUs[i]
		if g.Integrated {
			continue
		}
		if g.TotalBytes == 0 {
			if unknownDiscrete == "" {
				unknownDiscrete = g.Name
			}
			continue
		}
		if best == nil || g.TotalBytes > best.TotalBytes {
			best = g
		}
	}
	switch {
	case best != nil:
		why := "largest discrete GPU"
		if n := len(r.GPUs); n > 1 {
			why += fmt.Sprintf(" of %d devices; others ignored, never summed", n)
		}
		return Pick{Kind: KindDiscrete, GPU: *best, Why: why}
	case unknownDiscrete != "":
		return Pick{Kind: KindUnknown, GPU: r.firstDiscrete(), Why: "discrete GPU found but its memory could not be read"}
	}
	for _, g := range r.GPUs {
		if g.Vendor == VendorApple {
			return Pick{Kind: KindUnified, GPU: g, Why: "unified memory: GPU budget is a share of RAM"}
		}
	}
	for _, g := range r.GPUs {
		if g.Integrated {
			return Pick{Kind: KindIntegrated, GPU: g, Why: "only an integrated GPU: models run from system RAM"}
		}
	}
	if len(r.Notes) > 0 {
		return Pick{Kind: KindUnknown, Why: "detection failed: " + strings.Join(r.Notes, "; ")}
	}
	return Pick{Kind: KindNone, Why: "no GPU detected"}
}

func (r Report) firstDiscrete() GPU {
	for _, g := range r.GPUs {
		if !g.Integrated {
			return g
		}
	}
	return GPU{}
}

// looksIntegrated is the name-based fallback for sources that don't
// say whether a device shares system memory (the Windows registry,
// nvidia-smi on Tegra). Vendor-specific: Intel parts are integrated
// unless they are Arc cards; AMD APU graphics carry generic
// "Radeon Graphics" / "Radeon 780M" style names rather than RX
// model numbers; NVIDIA desktop and laptop parts are all discrete.
func looksIntegrated(vendor, name string) bool {
	n := strings.ToLower(name)
	switch vendor {
	case VendorApple:
		return true
	case VendorIntel:
		return !strings.Contains(n, "arc")
	case VendorAMD:
		// APU names: "Radeon(TM) Graphics", "Radeon Vega 8 Graphics",
		// "Radeon RX Vega 11 Graphics", "Radeon 780M". Cards carry an
		// RX/Pro/Instinct model number without a trailing "Graphics".
		if strings.Contains(n, "vega") && strings.HasSuffix(n, "graphics") {
			return true
		}
		if strings.Contains(n, " rx ") || strings.HasSuffix(n, " rx") || strings.Contains(n, "radeon pro") || strings.Contains(n, "instinct") {
			return false
		}
		return strings.Contains(n, "radeon(tm) graphics") ||
			strings.HasSuffix(n, "radeon graphics") ||
			igpuModel.MatchString(n)
	}
	return false
}
