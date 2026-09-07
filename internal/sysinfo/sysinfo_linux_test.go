package sysinfo

import (
	"os"
	"path/filepath"
	"testing"
)

func writeCard(t *testing.T, root, name, vram string) {
	t.Helper()
	dir := filepath.Join(root, name, "device")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if vram == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, "mem_info_vram_total"), []byte(vram+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAMDVRAMSumsDiscreteCardsOnly(t *testing.T) {
	root := t.TempDir()
	writeCard(t, root, "card0", "")                 // NVIDIA card: amdgpu attr absent
	writeCard(t, root, "card1", "536870912")        // 512 MB APU, ignored
	writeCard(t, root, "card2", "17163091968")      // 16 GB discrete
	writeCard(t, root, "card2-DP-1", "17163091968") // connector node, must not double count
	writeCard(t, root, "card3", "not-a-number")

	got, ok := amdVRAM(root)
	if !ok || got != 17163091968 {
		t.Errorf("amdVRAM = (%d, %v), want (17163091968, true)", got, ok)
	}
}

func TestAMDVRAMUnknownCases(t *testing.T) {
	if got, ok := amdVRAM(filepath.Join(t.TempDir(), "missing")); ok || got != 0 {
		t.Errorf("missing root: (%d, %v)", got, ok)
	}
	root := t.TempDir()
	writeCard(t, root, "card0", "536870912")
	if got, ok := amdVRAM(root); ok || got != 0 {
		t.Errorf("only an iGPU: (%d, %v), want unknown", got, ok)
	}
}
