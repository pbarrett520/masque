package sysinfo

import "testing"

func TestParseNvidiaSMI(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want uint64
		ok   bool
	}{
		{"single 4090", "24564\n", 24564 << 20, true},
		{"two cards summed", "24564\n12288\n", (24564 + 12288) << 20, true},
		{"crlf and padding", " 24564 \r\n", 24564 << 20, true},
		{"n/a line skipped", "[N/A]\n8192\n", 8192 << 20, true},
		{"small igpu ignored", "512\n", 0, false},
		{"empty", "", 0, false},
		{"garbage", "No devices were found\n", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parseNvidiaSMI(c.out)
			if got != c.want || ok != c.ok {
				t.Errorf("parseNvidiaSMI(%q) = (%d, %v), want (%d, %v)", c.out, got, ok, c.want, c.ok)
			}
		})
	}
}

func TestGPUMemoryNeverReportsZeroAsKnown(t *testing.T) {
	// Whatever this machine has, ok=true must come with a non-zero figure.
	v, ok := GPUMemory()
	if ok && v == 0 {
		t.Errorf("GPUMemory = (0, true); unknown must be ok=false")
	}
}
