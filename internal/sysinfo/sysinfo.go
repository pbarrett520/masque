// Package sysinfo answers two questions: how much RAM does this machine
// have, and how much discrete-GPU memory? The curated model list (dev
// spec §8) uses both to tier starter models by fit. Implementations are
// per-OS via x/sys plus, for VRAM, vendor tools and sysfs — no cgo, per
// project convention.
package sysinfo

// TotalRAM returns total physical memory in bytes, or 0 if it cannot be
// determined. Callers must treat 0 as "unknown" and skip fit filtering
// rather than concluding nothing fits.
func TotalRAM() uint64 {
	return totalRAM()
}

// GPUMemory returns the summed VRAM of the machine's discrete GPUs in
// bytes. ok is false whenever the figure could not be measured — no
// vendor tool, no supported GPU, a timeout — and callers must then
// treat VRAM as unknown ("don't gate"), never as zero.
//
// Integrated GPUs below 2 GB (an AMD APU next to a big NVIDIA card, say)
// are ignored: Ollama won't offload to them in any useful way and
// counting them would inflate the budget.
//
// macOS always reports ok=false: Apple Silicon is unified memory, so the
// caller should budget from RAM instead (Metal can use roughly 75% of
// it).
func GPUMemory() (totalVRAM uint64, ok bool) {
	return gpuMemory()
}
