package sysinfo

import (
	"runtime"

	"golang.org/x/sys/unix"
)

func totalRAM() uint64 {
	v, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}
	return v
}

// detectGPUs on Apple Silicon reports the one unified-memory GPU: the
// chip name from sysctl, no separate VRAM figure (the budget is a
// share of RAM, decided by the caller — see ollamamgr.unifiedBudget).
// Intel Macs are left unknown: their discrete AMD parts would need
// IOKit (cgo) to read, and they are rare enough for local LLM use.
func detectGPUs() Report {
	if runtime.GOARCH != "arm64" {
		return Report{Notes: []string{"Intel Mac: GPU memory not readable without IOKit; budgeting from RAM"}}
	}
	name := "Apple Silicon"
	if brand, err := unix.Sysctl("machdep.cpu.brand_string"); err == nil && brand != "" {
		name = brand
	}
	return Report{GPUs: []GPU{{
		Vendor:     VendorApple,
		Name:       name,
		Integrated: true,
		Source:     "sysctl",
	}}}
}
