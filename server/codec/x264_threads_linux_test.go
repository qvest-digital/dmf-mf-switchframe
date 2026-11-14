//go:build cgo && !noffmpeg

package codec

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// onlineCPUs counts the CPUs in /sys/devices/system/cpu/online, which is what
// sysconf(_SC_NPROCESSORS_ONLN) reports on Linux.
func onlineCPUs(t *testing.T) int {
	t.Helper()
	b, err := os.ReadFile("/sys/devices/system/cpu/online")
	require.NoError(t, err)
	n := 0
	for _, r := range strings.Split(strings.TrimSpace(string(b)), ",") {
		lo, hi, isRange := strings.Cut(r, "-")
		a, err := strconv.Atoi(lo)
		require.NoError(t, err)
		if !isRange {
			n++
			continue
		}
		z, err := strconv.Atoi(hi)
		require.NoError(t, err)
		n += z - a + 1
	}
	return n
}
