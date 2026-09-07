package sysinfo

import (
	"bufio"
	"strconv"
	"strings"
	"time"
)

// minDiscreteVRAM is the threshold below which a GPU is treated as an
// integrated/display-only part and left out of the VRAM sum.
const minDiscreteVRAM = 2 << 30

// nvidiaSMITimeout bounds the nvidia-smi probe. A healthy driver answers
// in well under a second; a wedged one must not stall the roster.
const nvidiaSMITimeout = 3 * time.Second

// nvidiaSMIArgs asks for one line per GPU holding the MiB total as a
// bare integer.
var nvidiaSMIArgs = []string{"--query-gpu=memory.total", "--format=csv,noheader,nounits"}

// parseNvidiaSMI sums the per-GPU totals from nvidia-smi's
// csv,noheader,nounits output (one MiB integer per line). Unparsable
// lines ("[N/A]", blanks) are skipped; sub-threshold GPUs are ignored.
// ok is false when no GPU contributed.
func parseNvidiaSMI(out string) (uint64, bool) {
	var total uint64
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		field := strings.TrimSpace(sc.Text())
		if field == "" {
			continue
		}
		mib, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			continue
		}
		total += countDiscrete(mib << 20)
	}
	return total, total > 0
}

// countDiscrete returns bytes if the GPU is big enough to count as a
// discrete part, else 0.
func countDiscrete(bytes uint64) uint64 {
	if bytes < minDiscreteVRAM {
		return 0
	}
	return bytes
}
