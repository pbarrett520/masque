package sysinfo

import (
	"bufio"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// minDiscreteVRAM is the fallback threshold for sources that don't say
// whether a device is integrated: at or below this, treat it as an
// iGPU carve-out. Inclusive on purpose — APU frame buffers are set in
// round BIOS sizes, and a 2 GiB one is exactly 2 GiB (the "8 GB card
// reported as 10 GB" bug slipped through a strict less-than).
const minDiscreteVRAM = 2 << 30

// igpuModel matches AMD mobile iGPU model names: Radeon 680M, 780M,
// 890M, 8060S.
var igpuModel = regexp.MustCompile(`radeon \d{3,4}[ms]\b`)

// nvidiaSMITimeout bounds the nvidia-smi probe. A healthy driver answers
// in well under a second; a wedged one must not stall the roster.
const nvidiaSMITimeout = 3 * time.Second

// nvidiaSMIArgs asks for one CSV line per GPU: name, then MiB totals
// as bare integers.
var nvidiaSMIArgs = []string{
	"--query-gpu=name,memory.total,memory.used,memory.free",
	"--format=csv,noheader,nounits",
}

// parseNvidiaSMI turns nvidia-smi's csv,noheader,nounits output into
// one GPU per line. Lines that don't end in three integers ("[N/A]",
// "No devices were found", blanks) are skipped. nvidia-smi only sees
// NVIDIA parts, which are all discrete (Tegra SoCs aside, which don't
// run desktop Ollama).
func parseNvidiaSMI(out string) []GPU {
	var gpus []GPU
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		fields := strings.Split(sc.Text(), ",")
		if len(fields) < 4 {
			continue
		}
		n := len(fields)
		// memory.used (fields[n-2]) is implied by total − free.
		total, err1 := strconv.ParseUint(strings.TrimSpace(fields[n-3]), 10, 64)
		free, err3 := strconv.ParseUint(strings.TrimSpace(fields[n-1]), 10, 64)
		if err1 != nil || err3 != nil || total == 0 {
			continue
		}
		name := strings.TrimSpace(strings.Join(fields[:n-3], ","))
		if name == "" {
			name = "NVIDIA GPU"
		}
		gpus = append(gpus, GPU{
			Vendor:     VendorNVIDIA,
			Name:       name,
			TotalBytes: total << 20,
			FreeBytes:  free << 20,
			Integrated: looksIntegrated(VendorNVIDIA, name),
			Source:     "nvidia-smi",
		})
	}
	return gpus
}

// pciVendor maps a PCI vendor id (as sysfs/registry spell it) to a
// Vendor constant.
func pciVendor(id string) string {
	switch strings.ToLower(strings.TrimPrefix(strings.TrimSpace(id), "0x")) {
	case "10de":
		return VendorNVIDIA
	case "1002", "1022":
		return VendorAMD
	case "8086":
		return VendorIntel
	case "106b":
		return VendorApple
	}
	return VendorUnknown
}
