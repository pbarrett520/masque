//go:build linux || windows

package sysinfo

import (
	"context"
	"os/exec"
)

// nvidiaVRAM runs the first nvidia-smi found among candidates (bare
// names are resolved on PATH) and sums the reported GPU memory. Any
// failure — no binary, timeout, non-zero exit, nothing parsable —
// yields ok=false.
func nvidiaVRAM(candidates []string) (uint64, bool) {
	for _, c := range candidates {
		path, err := exec.LookPath(c)
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), nvidiaSMITimeout)
		cmd := exec.CommandContext(ctx, path, nvidiaSMIArgs...)
		hideConsole(cmd)
		out, err := cmd.Output()
		cancel()
		if err != nil {
			return 0, false
		}
		return parseNvidiaSMI(string(out))
	}
	return 0, false
}
