//go:build cgo && !noffmpeg

package codec

import (
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// onlineCPUs reads hw.ncpu, which is what sysconf(_SC_NPROCESSORS_ONLN)
// reports on macOS.
func onlineCPUs(t *testing.T) int {
	t.Helper()
	n, err := unix.SysctlUint32("hw.ncpu")
	require.NoError(t, err)
	return int(n)
}
