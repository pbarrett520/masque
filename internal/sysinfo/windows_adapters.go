package sysinfo

import (
	"regexp"
	"strings"
)

// adapterRecord is one display adapter as read from the Windows
// registry's display class key
// (HKLM\SYSTEM\CurrentControlSet\Control\Class\{4d36e968-…}\000N).
// Kept OS-independent so the mapping can be tested anywhere.
type adapterRecord struct {
	Key string // subkey name, e.g. "0000"
	// DriverDesc is the adapter name, e.g. "NVIDIA GeForce RTX 3080".
	DriverDesc string
	// MatchingDeviceID carries the PCI ids: "pci\ven_10de&dev_2206&…".
	MatchingDeviceID string
	// QwMemorySize is HardwareInformation.qwMemorySize, a 64-bit byte
	// count that reports cards over 4 GB correctly. 0 when absent.
	QwMemorySize uint64
	// MemorySize is the legacy 32-bit HardwareInformation.MemorySize —
	// the same source WMI's Win32_VideoController.AdapterRAM uses — and
	// it saturates at 4 GB. Only trusted when the 64-bit value is absent
	// and it is below the cap.
	MemorySize uint64
}

var venID = regexp.MustCompile(`(?i)ven_([0-9a-f]{4})`)

// parseAdapterRecords maps registry adapter entries to GPUs. Software
// adapters (no PCI vendor) are dropped; memory prefers the 64-bit
// field; integrated vs discrete comes from the name heuristic since
// the registry doesn't say.
func parseAdapterRecords(recs []adapterRecord) []GPU {
	var gpus []GPU
	for _, rec := range recs {
		m := venID.FindStringSubmatch(rec.MatchingDeviceID)
		if m == nil {
			continue // Microsoft Basic Display, RDP mirror drivers, …
		}
		vendor := pciVendor(m[1])
		if vendor == VendorUnknown {
			continue
		}
		name := strings.TrimSpace(rec.DriverDesc)
		if name == "" {
			name = strings.ToUpper(vendor) + " GPU"
		}
		g := GPU{Vendor: vendor, Name: name, Integrated: looksIntegrated(vendor, name), Source: "registry " + rec.Key}
		switch {
		case rec.QwMemorySize > 0:
			g.TotalBytes = rec.QwMemorySize
		case rec.MemorySize > 0 && rec.MemorySize < 0xFFFFFFFF:
			g.TotalBytes = rec.MemorySize
		}
		gpus = append(gpus, g)
	}
	return gpus
}

// mergeNvidiaFree copies free-memory figures from nvidia-smi onto the
// registry entries for the same NVIDIA cards (matched by name, in
// order), so the picker sees free VRAM on Windows too. nvidia-smi
// totals also replace the registry's when both exist: the driver's own
// number is the one Ollama will see.
func mergeNvidiaFree(gpus, smi []GPU) []GPU {
	used := make([]bool, len(smi))
	for i := range gpus {
		if gpus[i].Vendor != VendorNVIDIA {
			continue
		}
		for j := range smi {
			if used[j] || !sameGPUName(gpus[i].Name, smi[j].Name) {
				continue
			}
			used[j] = true
			gpus[i].TotalBytes = smi[j].TotalBytes
			gpus[i].FreeBytes = smi[j].FreeBytes
			gpus[i].Source += " + nvidia-smi"
			break
		}
	}
	return gpus
}

func sameGPUName(a, b string) bool {
	norm := func(s string) string {
		s = strings.ToLower(s)
		s = strings.ReplaceAll(s, "nvidia ", "")
		return strings.Join(strings.Fields(s), " ")
	}
	return norm(a) == norm(b)
}
