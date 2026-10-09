package sysinfo

import (
	"errors"
	"fmt"
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

// drmRoot is where the kernel exposes GPU devices; detectLinux takes it
// as a parameter so tests can point it at a fixture tree.
const drmRoot = "/sys/class/drm"

func detectGPUs() Report {
	return detectLinux(drmRoot, func() ([]GPU, error) { return nvidiaGPUs([]string{"nvidia-smi"}) })
}

// hideConsole is a no-op on Linux; Windows uses it to keep nvidia-smi
// from flashing a console window over the app.
func hideConsole(*exec.Cmd) {}

// cardDir matches top-level DRM card nodes (card0, card1), not their
// connector children (card1-DP-4), whose device link points back at the
// card and would otherwise be walked twice.
var cardDir = regexp.MustCompile(`^card[0-9]+$`)

// detectLinux walks the DRM cards under root and asks nvidia-smi about
// NVIDIA parts. Per device:
//
//   - NVIDIA (vendor 10de): memory comes from nvidia-smi, which is the
//     only no-root source for the proprietary driver. If nvidia-smi is
//     missing or fails, the card is listed with unknown memory and a
//     note, so the caller budgets from RAM instead of guessing.
//   - AMD (amdgpu): mem_info_vram_total/used from sysfs. Integrated
//     parts are told apart by the absence of mem_info_vram_vendor —
//     amdgpu only publishes it when the firmware names a VRAM vendor
//     (GDDR/HBM on cards; an APU's carve-out is plain system DDR) —
//     with the carve-out-sized total as a second signal.
//   - Intel (8086): listed by name only. i915/xe expose no VRAM figure
//     in sysfs without root, so a discrete Arc card is "memory unknown"
//     and an iGPU is integrated.
func detectLinux(root string, nvidia func() ([]GPU, error)) Report {
	var r Report
	entries, err := os.ReadDir(root)
	if err != nil {
		r.Notes = append(r.Notes, fmt.Sprintf("cannot read %s: %v", root, err))
	}
	var nvidiaCards int
	for _, e := range entries {
		if !cardDir.MatchString(e.Name()) {
			continue
		}
		dev := filepath.Join(root, e.Name(), "device")
		vendor := pciVendor(readTrim(dev, "vendor"))
		switch vendor {
		case VendorNVIDIA:
			nvidiaCards++ // filled in from nvidia-smi below
		case VendorAMD:
			r.GPUs = append(r.GPUs, amdCard(dev, e.Name()))
		case VendorIntel:
			name := "Intel GPU"
			if label := readTrim(dev, "label"); label != "" {
				name = label
			}
			g := GPU{Vendor: VendorIntel, Name: name, Integrated: true, Source: "sysfs " + e.Name()}
			if isIntelDiscrete(dev) {
				g.Integrated = false
				r.Notes = append(r.Notes, e.Name()+": Intel discrete GPU memory is not readable without root; treated as unknown")
			}
			r.GPUs = append(r.GPUs, g)
		case VendorUnknown:
			if readTrim(dev, "vendor") == "" {
				continue // no PCI device behind this node (virtual/simple framebuffer)
			}
			r.Notes = append(r.Notes, fmt.Sprintf("%s: unsupported GPU vendor %s", e.Name(), readTrim(dev, "vendor")))
		}
	}

	gpus, err := nvidia()
	switch {
	case err == nil:
		r.GPUs = append(r.GPUs, gpus...)
	case nvidiaCards > 0:
		// Cards are present but can't be measured: list them so the
		// caller knows a GPU exists, with memory unknown.
		for i := 0; i < nvidiaCards; i++ {
			r.GPUs = append(r.GPUs, GPU{Vendor: VendorNVIDIA, Name: "NVIDIA GPU", Source: "sysfs (nvidia-smi unavailable)"})
		}
		r.Notes = append(r.Notes, "NVIDIA GPU present but "+err.Error())
	case !errors.Is(err, errNoNvidiaSMI):
		r.Notes = append(r.Notes, err.Error())
	}
	return r
}

// amdCard reads one amdgpu device directory.
func amdCard(dev, card string) GPU {
	g := GPU{Vendor: VendorAMD, Name: "AMD GPU", Source: "amdgpu sysfs " + card}
	if name := readTrim(dev, "product_name"); name != "" {
		g.Name = name
	}
	total, _ := strconv.ParseUint(readTrim(dev, "mem_info_vram_total"), 10, 64)
	used, _ := strconv.ParseUint(readTrim(dev, "mem_info_vram_used"), 10, 64)
	g.TotalBytes = total
	if total > 0 && used <= total {
		g.FreeBytes = total - used
	}
	_, vendorErr := os.Stat(filepath.Join(dev, "mem_info_vram_vendor"))
	g.Integrated = vendorErr != nil || total <= minDiscreteVRAM
	if g.Integrated && g.Name == "AMD GPU" {
		g.Name = "AMD integrated graphics"
	}
	return g
}

// isIntelDiscrete reports whether an Intel DRM device is a discrete
// card. Intel iGPUs sit on PCI bus 0 as device 2 (0000:00:02.0) on
// every generation; Arc cards hang off a PCIe bridge and resolve to a
// non-zero bus.
func isIntelDiscrete(dev string) bool {
	real, err := filepath.EvalSymlinks(dev)
	if err != nil {
		return false
	}
	addr := filepath.Base(real) // e.g. 0000:03:00.0
	parts := strings.Split(addr, ":")
	return len(parts) == 3 && parts[1] != "00"
}

func readTrim(dir, file string) string {
	raw, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}
