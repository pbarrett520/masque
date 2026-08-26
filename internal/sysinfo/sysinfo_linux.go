package sysinfo

import "golang.org/x/sys/unix"

func totalRAM() uint64 {
	var info unix.Sysinfo_t
	if err := unix.Sysinfo(&info); err != nil {
		return 0
	}
	// Totalram is in units of Unit bytes (usually 1).
	return uint64(info.Totalram) * uint64(info.Unit) //nolint:unconvert // types differ across architectures
}
