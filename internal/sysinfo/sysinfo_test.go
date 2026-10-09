package sysinfo

import (
	"strings"
	"testing"
)

const mib = 1 << 20

func TestParseNvidiaSMI(t *testing.T) {
	cases := []struct {
		name  string
		out   string
		want  []GPU
		count int
	}{
		{"4090 (this machine)", "NVIDIA GeForce RTX 4090, 24564, 648, 23411\n",
			[]GPU{{Name: "NVIDIA GeForce RTX 4090", TotalBytes: 24564 * mib, FreeBytes: 23411 * mib}}, 1},
		{"2070 super", "NVIDIA GeForce RTX 2070 SUPER, 8192, 1534, 6658\n",
			[]GPU{{Name: "NVIDIA GeForce RTX 2070 SUPER", TotalBytes: 8192 * mib, FreeBytes: 6658 * mib}}, 1},
		{"two cards stay separate", "NVIDIA A, 24564, 0, 24564\nNVIDIA B, 12288, 0, 12288\n", nil, 2},
		{"crlf and padding", " NVIDIA X , 8192 , 10 , 8182 \r\n", []GPU{{Name: "NVIDIA X", TotalBytes: 8192 * mib, FreeBytes: 8182 * mib}}, 1},
		{"n/a line skipped", "Broken, [N/A], [N/A], [N/A]\nNVIDIA Y, 8192, 0, 8192\n", nil, 1},
		{"empty", "", nil, 0},
		{"no devices", "No devices were found\n", nil, 0},
		{"old single-column format rejected", "24564\n", nil, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseNvidiaSMI(c.out)
			if len(got) != c.count {
				t.Fatalf("got %d GPUs: %+v", len(got), got)
			}
			for i, want := range c.want {
				g := got[i]
				if g.Name != want.Name || g.TotalBytes != want.TotalBytes || g.FreeBytes != want.FreeBytes || g.Integrated || g.Vendor != VendorNVIDIA {
					t.Errorf("gpu %d = %+v, want name=%q total=%d free=%d", i, g, want.Name, want.TotalBytes, want.FreeBytes)
				}
			}
		})
	}
}

// Report fixtures for the selection rule, vendor-agnostic: these are
// what the per-OS probes produce for the configurations in the brief.
func TestBestNeverSumsAndIgnoresIntegrated(t *testing.T) {
	cases := []struct {
		name      string
		report    Report
		wantKind  Kind
		wantTotal uint64
		wantFree  uint64
		wantName  string
	}{
		{
			name: "tester: 2070 Super 8 GB + 2 GB AMD iGPU",
			report: Report{GPUs: []GPU{
				{Vendor: VendorAMD, Name: "AMD integrated graphics", TotalBytes: 2 << 30, Integrated: true},
				{Vendor: VendorNVIDIA, Name: "NVIDIA GeForce RTX 2070 SUPER", TotalBytes: 8192 * mib, FreeBytes: 6658 * mib},
			}},
			wantKind: KindDiscrete, wantTotal: 8192 * mib, wantFree: 6658 * mib, wantName: "NVIDIA GeForce RTX 2070 SUPER",
		},
		{
			name: "dev box: RTX 4090 24564 MiB + 512 MiB AMD iGPU",
			report: Report{GPUs: []GPU{
				{Vendor: VendorNVIDIA, Name: "NVIDIA GeForce RTX 4090", TotalBytes: 24564 * mib, FreeBytes: 23411 * mib},
				{Vendor: VendorAMD, Name: "AMD integrated graphics", TotalBytes: 512 * mib, FreeBytes: 492 * mib, Integrated: true},
			}},
			wantKind: KindDiscrete, wantTotal: 24564 * mib, wantFree: 23411 * mib, wantName: "NVIDIA GeForce RTX 4090",
		},
		{
			name: "laptop: RTX 3060 + Intel iGPU",
			report: Report{GPUs: []GPU{
				{Vendor: VendorIntel, Name: "Intel GPU", Integrated: true},
				{Vendor: VendorNVIDIA, Name: "NVIDIA GeForce RTX 3060 Laptop GPU", TotalBytes: 6144 * mib, FreeBytes: 5900 * mib},
			}},
			wantKind: KindDiscrete, wantTotal: 6144 * mib, wantFree: 5900 * mib, wantName: "NVIDIA GeForce RTX 3060 Laptop GPU",
		},
		{
			name: "AMD discrete",
			report: Report{GPUs: []GPU{
				{Vendor: VendorAMD, Name: "Radeon RX 7800 XT", TotalBytes: 16368 * mib, FreeBytes: 15800 * mib},
			}},
			wantKind: KindDiscrete, wantTotal: 16368 * mib, wantFree: 15800 * mib, wantName: "Radeon RX 7800 XT",
		},
		{
			name: "two discrete: largest wins, never the sum",
			report: Report{GPUs: []GPU{
				{Vendor: VendorNVIDIA, Name: "A", TotalBytes: 12 << 30},
				{Vendor: VendorNVIDIA, Name: "B", TotalBytes: 24 << 30},
			}},
			wantKind: KindDiscrete, wantTotal: 24 << 30, wantName: "B",
		},
		{
			name:     "apple silicon",
			report:   Report{GPUs: []GPU{{Vendor: VendorApple, Name: "Apple M3", Integrated: true}}},
			wantKind: KindUnified, wantName: "Apple M3",
		},
		{
			name:     "integrated only",
			report:   Report{GPUs: []GPU{{Vendor: VendorAMD, Name: "AMD integrated graphics", TotalBytes: 2 << 30, Integrated: true}}},
			wantKind: KindIntegrated, wantTotal: 2 << 30, wantName: "AMD integrated graphics",
		},
		{
			name:     "no GPU",
			report:   Report{},
			wantKind: KindNone,
		},
		{
			name:     "detection failed",
			report:   Report{Notes: []string{"nvidia-smi failed: exit status 1"}},
			wantKind: KindUnknown,
		},
		{
			name:     "discrete card with unreadable memory",
			report:   Report{GPUs: []GPU{{Vendor: VendorNVIDIA, Name: "NVIDIA GPU"}, {Vendor: VendorAMD, Name: "AMD integrated graphics", TotalBytes: 1 << 30, Integrated: true}}},
			wantKind: KindUnknown, wantName: "NVIDIA GPU",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := c.report.Best()
			if p.Kind != c.wantKind || p.GPU.TotalBytes != c.wantTotal || p.GPU.FreeBytes != c.wantFree || p.GPU.Name != c.wantName {
				t.Errorf("Best = %+v, want kind=%s total=%d free=%d name=%q", p, c.wantKind, c.wantTotal, c.wantFree, c.wantName)
			}
			if p.Why == "" {
				t.Error("Why must explain the pick")
			}
		})
	}
}

func TestLooksIntegrated(t *testing.T) {
	cases := map[[2]string]bool{
		{VendorIntel, "Intel(R) UHD Graphics 770"}:           true,
		{VendorIntel, "Intel(R) Iris(R) Xe Graphics"}:        true,
		{VendorIntel, "Intel(R) Arc(TM) A770 Graphics"}:      false,
		{VendorAMD, "AMD Radeon(TM) Graphics"}:               true,
		{VendorAMD, "AMD Radeon 780M"}:                       true,
		{VendorAMD, "AMD Radeon 8060S"}:                      true,
		{VendorAMD, "AMD Radeon RX Vega 11 Graphics"}:        true,
		{VendorAMD, "AMD Radeon RX 7800 XT"}:                 false,
		{VendorAMD, "AMD Radeon RX 6600"}:                    false,
		{VendorAMD, "Radeon RX 580 Series"}:                  false,
		{VendorNVIDIA, "NVIDIA GeForce RTX 3060 Laptop GPU"}: false,
		{VendorApple, "Apple M2"}:                            true,
	}
	for in, want := range cases {
		if got := looksIntegrated(in[0], in[1]); got != want {
			t.Errorf("looksIntegrated(%s, %q) = %v, want %v", in[0], in[1], got, want)
		}
	}
}

func TestWindowsAdapterRecords(t *testing.T) {
	recs := []adapterRecord{
		{Key: "0000", DriverDesc: "NVIDIA GeForce RTX 3080", MatchingDeviceID: `PCI\VEN_10DE&DEV_2206&SUBSYS_38811462`,
			QwMemorySize: 10 << 30, MemorySize: 0xFFFFFFFF}, // 32-bit field saturated at 4 GB
		{Key: "0001", DriverDesc: "AMD Radeon(TM) Graphics", MatchingDeviceID: `PCI\VEN_1002&DEV_164E`,
			QwMemorySize: 512 * mib},
		{Key: "0002", DriverDesc: "Intel(R) Arc(TM) A770 Graphics", MatchingDeviceID: `PCI\VEN_8086&DEV_56A0`,
			MemorySize: 0xFFFFFFFF}, // only the capped field: memory must stay unknown, not 4 GB
		{Key: "0003", DriverDesc: "NVIDIA GeForce GT 1030", MatchingDeviceID: `PCI\VEN_10DE&DEV_1D01`,
			MemorySize: 2 << 30}, // legacy field below the cap is fine
		{Key: "0004", DriverDesc: "Microsoft Basic Display Adapter", MatchingDeviceID: `ROOT\BasicDisplay`},
	}
	gpus := parseAdapterRecords(recs)
	if len(gpus) != 4 {
		t.Fatalf("got %d GPUs: %+v", len(gpus), gpus)
	}
	if g := gpus[0]; g.Vendor != VendorNVIDIA || g.TotalBytes != 10<<30 || g.Integrated {
		t.Errorf("3080: %+v", g)
	}
	if g := gpus[1]; g.Vendor != VendorAMD || !g.Integrated || g.TotalBytes != 512*mib {
		t.Errorf("APU: %+v", g)
	}
	if g := gpus[2]; g.Vendor != VendorIntel || g.Integrated || g.TotalBytes != 0 {
		t.Errorf("Arc with capped field must be unknown: %+v", g)
	}
	if g := gpus[3]; g.TotalBytes != 2<<30 {
		t.Errorf("GT 1030: %+v", g)
	}

	merged := mergeNvidiaFree(gpus, []GPU{{Vendor: VendorNVIDIA, Name: "NVIDIA GeForce RTX 3080", TotalBytes: 10240 * mib, FreeBytes: 9000 * mib}})
	if merged[0].FreeBytes != 9000*mib || merged[0].TotalBytes != 10240*mib || !strings.Contains(merged[0].Source, "nvidia-smi") {
		t.Errorf("merge: %+v", merged[0])
	}
	if merged[3].FreeBytes != 0 {
		t.Errorf("unmatched card must keep free unknown: %+v", merged[3])
	}
	// Selection on this box: the 3080, never 10 + 0.5 + 2.
	if p := (Report{GPUs: merged}).Best(); p.Kind != KindDiscrete || p.GPU.TotalBytes != 10240*mib {
		t.Errorf("Best = %+v", p)
	}
}

func TestReportStringIsExact(t *testing.T) {
	r := Report{GPUs: []GPU{{Vendor: VendorNVIDIA, Name: "NVIDIA GeForce RTX 4090", TotalBytes: 24564 * mib, FreeBytes: 23411 * mib, Source: "nvidia-smi"}},
		Notes: []string{"card1: something"}}
	s := r.String()
	for _, want := range []string{"24564 MiB total", "23411 MiB free", "nvidia-smi", "card1: something"} {
		if !strings.Contains(s, want) {
			t.Errorf("%q missing from %q", want, s)
		}
	}
	if (Report{}).String() != "no GPUs detected" {
		t.Errorf("empty report: %q", (Report{}).String())
	}
}

func TestDetectGPUsOnThisMachineIsConsistent(t *testing.T) {
	// Whatever the hardware, a discrete pick must carry a non-zero
	// total and free never exceeds total.
	p := DetectGPUs().Best()
	if p.Kind == KindDiscrete && p.GPU.TotalBytes == 0 {
		t.Errorf("discrete pick with unknown memory: %+v", p)
	}
	if p.GPU.FreeBytes > p.GPU.TotalBytes {
		t.Errorf("free > total: %+v", p)
	}
}
