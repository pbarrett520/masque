package sysinfo

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
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

// gpuMemory covers NVIDIA only for now: nvidia-smi is normally on PATH
// (recent drivers install it under System32) and otherwise lives in the
// legacy NVSMI folder. AMD reports unknown until a no-cgo route exists.
func gpuMemory() (uint64, bool) {
	candidates := []string{"nvidia-smi"}
	if pf := os.Getenv("ProgramFiles"); pf != "" {
		candidates = append(candidates, filepath.Join(pf, "NVIDIA Corporation", "NVSMI", "nvidia-smi.exe"))
	}
	return nvidiaVRAM(candidates)
}

// hideConsole stops the child from opening a console window over the
// GUI app.
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
