//go:build (cgo && darwin) || (cgo && cuda)

package gpu

import "testing"

// testContext creates a GPU context for testing, skipping the test if
// the GPU is not available (e.g., Metal language version unsupported on
// CI runner, no CUDA device). Registers cleanup via t.Cleanup.
func testContext(t *testing.T) *Context {
	t.Helper()
	ctx, err := NewContext()
	if err != nil {
		t.Skip("GPU not available:", err)
	}
	t.Cleanup(func() { _ = ctx.Close() })
	return ctx
}
