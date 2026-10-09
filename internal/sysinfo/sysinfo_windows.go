package sysinfo

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// memoryStatusEx mirrors the Win32 MEMORYSTATUSEX struct.
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

var (
	kernel32                 = windows.NewLazySystemDLL("kernel32.dll")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
)

func totalRAM() uint64 {
	var m memoryStatusEx
	m.Length = uint32(unsafe.Sizeof(m))
	ret, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m)))
	if ret == 0 {
		return 0
	}
	return m.TotalPhys
}

// displayClassKey is the registry node holding one subkey per display
// adapter driver instance.
const displayClassKey = `SYSTEM\CurrentControlSet\Control\Class\{4d36e968-e325-11ce-bfc1-08002be10318}`

// detectGPUs reads every adapter from the display class registry key
// (vendor from the PCI id, memory from the 64-bit qwMemorySize — the
// 32-bit MemorySize that WMI's AdapterRAM exposes caps at 4 GB and is
// only a fallback) and then overlays nvidia-smi for NVIDIA free
// memory. The registry route needs no vendor tool, so AMD and Intel
// cards get a total too.
func detectGPUs() Report {
	var r Report
	recs, err := readAdapterRecords()
	if err != nil {
		r.Notes = append(r.Notes, "display adapter registry: "+err.Error())
	}
	r.GPUs = parseAdapterRecords(recs)

	candidates := []string{"nvidia-smi"}
	if pf := os.Getenv("ProgramFiles"); pf != "" {
		candidates = append(candidates, filepath.Join(pf, "NVIDIA Corporation", "NVSMI", "nvidia-smi.exe"))
	}
	smi, err := nvidiaGPUs(candidates)
	switch {
	case err == nil:
		r.GPUs = mergeNvidiaFree(r.GPUs, smi)
		if len(r.GPUs) == 0 {
			r.GPUs = smi
		}
	default:
		for _, g := range r.GPUs {
			if g.Vendor == VendorNVIDIA {
				r.Notes = append(r.Notes, "NVIDIA free memory unknown: "+err.Error())
				break
			}
		}
	}
	return r
}

// readAdapterRecords enumerates the display class subkeys.
func readAdapterRecords() ([]adapterRecord, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, displayClassKey, registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE)
	if err != nil {
		return nil, fmt.Errorf("opening display class key: %w", err)
	}
	defer k.Close() //nolint:errcheck // read-only key
	names, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return nil, fmt.Errorf("listing adapters: %w", err)
	}
	var recs []adapterRecord
	for _, name := range names {
		sub, err := registry.OpenKey(k, name, registry.QUERY_VALUE)
		if err != nil {
			continue // "Properties" and friends are ACL-protected
		}
		rec := adapterRecord{Key: name}
		rec.DriverDesc, _, _ = sub.GetStringValue("DriverDesc")
		rec.MatchingDeviceID, _, _ = sub.GetStringValue("MatchingDeviceId")
		if v, _, err := sub.GetIntegerValue("HardwareInformation.qwMemorySize"); err == nil {
			rec.QwMemorySize = v
		}
		if raw, _, err := sub.GetBinaryValue("HardwareInformation.MemorySize"); err == nil && len(raw) >= 4 {
			rec.MemorySize = uint64(raw[0]) | uint64(raw[1])<<8 | uint64(raw[2])<<16 | uint64(raw[3])<<24
		} else if v, _, err := sub.GetIntegerValue("HardwareInformation.MemorySize"); err == nil {
			rec.MemorySize = v
		}
		_ = sub.Close()
		if rec.DriverDesc != "" || rec.MatchingDeviceID != "" {
			recs = append(recs, rec)
		}
	}
	return recs, nil
}

// hideConsole stops the child from opening a console window over the
// GUI app.
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
