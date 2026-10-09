//go:build linux || windows

package sysinfo

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// errNoNvidiaSMI means no candidate binary was found on this machine:
// either there is no NVIDIA driver, or nvidia-smi isn't on PATH.
var errNoNvidiaSMI = errors.New("nvidia-smi not found")

// nvidiaGPUs runs the first nvidia-smi found among candidates (bare
// names are resolved on PATH) and returns one GPU per card. A missing
// binary is errNoNvidiaSMI; a timeout, non-zero exit, or unparsable
// output is another error. Callers treat every error as "NVIDIA memory
// unknown", never as zero.
func nvidiaGPUs(candidates []string) ([]GPU, error) {
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
			return nil, fmt.Errorf("nvidia-smi failed: %w", err)
		}
		gpus := parseNvidiaSMI(string(out))
		if len(gpus) == 0 {
			return nil, fmt.Errorf("nvidia-smi reported no usable GPU: %q", truncate(string(out), 80))
		}
		return gpus, nil
	}
	return nil, errNoNvidiaSMI
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
