package sysinfo

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func totalRAM() uint64 {
	var info unix.Sysinfo_t
	if err := unix.Sysinfo(&info); err != nil {
		return 0
	}
	// Totalram is in units of Unit bytes (usually 1).
	return uint64(info.Totalram) * uint64(info.Unit) //nolint:unconvert // types differ across architectures
}

// drmRoot is where the kernel exposes GPU devices; amdVRAM takes it as
// a parameter so tests can point it at a fixture tree.
const drmRoot = "/sys/class/drm"

// gpuMemory sums NVIDIA (via nvidia-smi) and AMD (via amdgpu sysfs)
// discrete VRAM. The two never double count: nvidia-smi only knows
// NVIDIA cards and mem_info_vram_total is amdgpu-only.
func gpuMemory() (uint64, bool) {
	nv, nvOK := nvidiaVRAM([]string{"nvidia-smi"})
	amd, amdOK := amdVRAM(drmRoot)
	return nv + amd, nvOK || amdOK
}

// hideConsole is a no-op on Linux; Windows uses it to keep nvidia-smi
// from flashing a console window over the app.
func hideConsole(*exec.Cmd) {}

// cardDir matches top-level DRM card nodes (card0, card1), not their
// connector children (card1-DP-4), whose device link points back at the
// card and would otherwise be walked twice.
var cardDir = regexp.MustCompile(`^card[0-9]+$`)

// amdVRAM sums mem_info_vram_total across amdgpu cards under root,
// skipping integrated parts below the discrete threshold.
func amdVRAM(root string) (uint64, bool) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, false
	}
	var total uint64
	for _, e := range entries {
		if !cardDir.MatchString(e.Name()) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(root, e.Name(), "device", "mem_info_vram_total"))
		if err != nil {
			continue
		}
		bytes, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
		if err != nil {
			continue
		}
		total += countDiscrete(bytes)
	}
	return total, total > 0
}
