package sysinfo

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// card describes one fixture DRM node. pci is the device's address;
// files are written under cardN/device.
type card struct {
	name  string
	pci   string
	files map[string]string
}

func writeTree(t *testing.T, cards []card) string {
	t.Helper()
	root := t.TempDir()
	devices := filepath.Join(root, "pci")
	for _, c := range cards {
		cardDir := filepath.Join(root, c.name)
		if err := os.MkdirAll(cardDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if c.pci == "" {
			continue // a node with no device link (virtual output)
		}
		dev := filepath.Join(devices, c.pci)
		if err := os.MkdirAll(dev, 0o755); err != nil {
			t.Fatal(err)
		}
		for f, v := range c.files {
			if err := os.WriteFile(filepath.Join(dev, f), []byte(v+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Symlink(dev, filepath.Join(cardDir, "device")); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func nvidiaOut(lines string) func() ([]GPU, error) {
	return func() ([]GPU, error) { return parseNvidiaSMI(lines), nil }
}

func nvidiaErr(err error) func() ([]GPU, error) {
	return func() ([]GPU, error) { return nil, err }
}

var (
	nvidiaCard = map[string]string{"vendor": "0x10de", "class": "0x030000", "boot_vga": "1"}
	intelIGPU  = map[string]string{"vendor": "0x8086", "class": "0x030000"}
)

func amdCardFiles(total, used string, discrete bool, name string) map[string]string {
	f := map[string]string{"vendor": "0x1002", "class": "0x030000",
		"mem_info_vram_total": total, "mem_info_vram_used": used, "mem_info_vis_vram_total": total}
	if discrete {
		f["mem_info_vram_vendor"] = "gddr6"
	}
	if name != "" {
		f["product_name"] = name
	}
	return f
}

func TestDetectLinuxFixtures(t *testing.T) {
	cases := []struct {
		name      string
		cards     []card
		nvidia    func() ([]GPU, error)
		wantKind  Kind
		wantTotal uint64
		wantFree  uint64
		wantName  string
		wantGPUs  int
		wantNote  bool
	}{
		{
			name: "tester: 2070 Super + 2 GiB AMD APU carve-out",
			cards: []card{
				{"card0", "0000:01:00.0", nvidiaCard},
				{"card0-DP-1", "", nil},
				{"card1", "0000:0c:00.0", amdCardFiles("2147483648", "104857600", false, "")},
				{"card1-HDMI-A-1", "", nil},
			},
			nvidia:   nvidiaOut("NVIDIA GeForce RTX 2070 SUPER, 8192, 1534, 6658\n"),
			wantKind: KindDiscrete, wantTotal: 8192 * mib, wantFree: 6658 * mib, wantName: "NVIDIA GeForce RTX 2070 SUPER", wantGPUs: 2,
		},
		{
			name: "dev box: 4090 + 512 MiB AMD APU",
			cards: []card{
				{"card0", "0000:01:00.0", nvidiaCard},
				{"card1", "0000:7a:00.0", amdCardFiles("536870912", "20951040", false, "")},
			},
			nvidia:   nvidiaOut("NVIDIA GeForce RTX 4090, 24564, 648, 23411\n"),
			wantKind: KindDiscrete, wantTotal: 24564 * mib, wantFree: 23411 * mib, wantName: "NVIDIA GeForce RTX 4090", wantGPUs: 2,
		},
		{
			name: "laptop: RTX 3060 + Intel iGPU at 00:02.0",
			cards: []card{
				{"card0", "0000:00:02.0", intelIGPU},
				{"card1", "0000:01:00.0", nvidiaCard},
			},
			nvidia:   nvidiaOut("NVIDIA GeForce RTX 3060 Laptop GPU, 6144, 100, 5900\n"),
			wantKind: KindDiscrete, wantTotal: 6144 * mib, wantFree: 5900 * mib, wantName: "NVIDIA GeForce RTX 3060 Laptop GPU", wantGPUs: 2,
		},
		{
			name: "AMD discrete, no nvidia-smi on the box",
			cards: []card{
				{"card0", "0000:03:00.0", amdCardFiles("17163091968", "600000000", true, "Radeon RX 7800 XT")},
			},
			nvidia:   nvidiaErr(errNoNvidiaSMI),
			wantKind: KindDiscrete, wantTotal: 17163091968, wantFree: 17163091968 - 600000000, wantName: "Radeon RX 7800 XT", wantGPUs: 1,
		},
		{
			name: "AMD discrete with ReBAR-sized carve-out look-alike is still discrete via vram_vendor",
			cards: []card{
				{"card0", "0000:03:00.0", amdCardFiles("8589934592", "0", true, "")},
			},
			nvidia:   nvidiaErr(errNoNvidiaSMI),
			wantKind: KindDiscrete, wantTotal: 8 << 30, wantFree: 8 << 30, wantName: "AMD GPU", wantGPUs: 1,
		},
		{
			name:     "integrated only (AMD APU)",
			cards:    []card{{"card0", "0000:0c:00.0", amdCardFiles("2147483648", "0", false, "")}},
			nvidia:   nvidiaErr(errNoNvidiaSMI),
			wantKind: KindIntegrated, wantTotal: 2 << 30, wantFree: 2 << 30, wantName: "AMD integrated graphics", wantGPUs: 1,
		},
		{
			name:     "integrated only (Intel)",
			cards:    []card{{"card0", "0000:00:02.0", intelIGPU}},
			nvidia:   nvidiaErr(errNoNvidiaSMI),
			wantKind: KindIntegrated, wantName: "Intel GPU", wantGPUs: 1,
		},
		{
			name:     "Intel Arc discrete: present, memory unknown",
			cards:    []card{{"card0", "0000:03:00.0", intelIGPU}},
			nvidia:   nvidiaErr(errNoNvidiaSMI),
			wantKind: KindUnknown, wantName: "Intel GPU", wantGPUs: 1, wantNote: true,
		},
		{
			name:     "no GPU at all",
			cards:    nil,
			nvidia:   nvidiaErr(errNoNvidiaSMI),
			wantKind: KindNone,
		},
		{
			name:     "NVIDIA card present but nvidia-smi missing",
			cards:    []card{{"card0", "0000:01:00.0", nvidiaCard}},
			nvidia:   nvidiaErr(errNoNvidiaSMI),
			wantKind: KindUnknown, wantName: "NVIDIA GPU", wantGPUs: 1, wantNote: true,
		},
		{
			name:     "nvidia-smi crashes",
			cards:    []card{{"card0", "0000:01:00.0", nvidiaCard}},
			nvidia:   nvidiaErr(errors.New("nvidia-smi failed: exit status 1")),
			wantKind: KindUnknown, wantName: "NVIDIA GPU", wantGPUs: 1, wantNote: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := writeTree(t, c.cards)
			r := detectLinux(root, c.nvidia)
			if len(r.GPUs) != c.wantGPUs {
				t.Errorf("found %d GPUs, want %d: %s", len(r.GPUs), c.wantGPUs, r)
			}
			if c.wantNote && len(r.Notes) == 0 {
				t.Errorf("expected a detection note: %s", r)
			}
			p := r.Best()
			if p.Kind != c.wantKind || p.GPU.TotalBytes != c.wantTotal || p.GPU.FreeBytes != c.wantFree || p.GPU.Name != c.wantName {
				t.Errorf("Best = kind=%s total=%d free=%d name=%q, want kind=%s total=%d free=%d name=%q\nreport: %s",
					p.Kind, p.GPU.TotalBytes, p.GPU.FreeBytes, p.GPU.Name, c.wantKind, c.wantTotal, c.wantFree, c.wantName, r)
			}
		})
	}
}

func TestDetectLinuxMissingRoot(t *testing.T) {
	r := detectLinux(filepath.Join(t.TempDir(), "nope"), nvidiaErr(errNoNvidiaSMI))
	if p := r.Best(); p.Kind != KindUnknown {
		t.Errorf("unreadable sysfs must be unknown, got %+v", p)
	}
}
