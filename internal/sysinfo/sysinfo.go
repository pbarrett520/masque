// Package sysinfo answers one question: how much RAM does this machine
// have? The curated model list (dev spec §8) uses it to flag starter
// models that plausibly fit. Implementations are per-OS via x/sys — no
// cgo, per project convention.
package sysinfo

// TotalRAM returns total physical memory in bytes, or 0 if it cannot be
// determined. Callers must treat 0 as "unknown" and skip fit filtering
// rather than concluding nothing fits.
func TotalRAM() uint64 {
	return totalRAM()
}
