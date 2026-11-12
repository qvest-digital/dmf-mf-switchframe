//go:build amd64

package transition

import "testing"

// A CPU without AVX2 runs whole rows through the SSE2 loop, which an AVX2
// host only reaches for a tail of 8 to 15 pixels. Not parallel: it
// switches the package-wide flag, and parallel tests start only after the
// sequential ones are done.
func TestBoxShrink2xRow_ExactWithoutAVX2(t *testing.T) {
	saved := avx2Available
	avx2Available = false
	t.Cleanup(func() { avx2Available = saved })
	checkBoxShrink2xRowExact(t)
}
