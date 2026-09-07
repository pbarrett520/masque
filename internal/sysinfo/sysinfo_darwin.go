package sysinfo

import "golang.org/x/sys/unix"

func totalRAM() uint64 {
	v, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}
	return v
}

// gpuMemory reports unknown on macOS. Apple Silicon shares RAM with the
// GPU, so there is no separate VRAM figure; callers budget from
// TotalRAM (Metal can address roughly 75% of it). Intel Macs with
// discrete AMD GPUs are rare enough for local LLM use to leave ungated.
func gpuMemory() (uint64, bool) {
	return 0, false
}
