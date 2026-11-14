//go:build cgo && !noffmpeg && !linux && !darwin

package codec

import "testing"

// onlineCPUs has no reading of sysconf(_SC_NPROCESSORS_ONLN) to compare
// against here.
func onlineCPUs(t *testing.T) int {
	t.Skip("no online CPU count to compare against on this OS")
	return 0
}
