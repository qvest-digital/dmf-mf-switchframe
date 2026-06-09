//go:build cgo && darwin

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMetalV210UploadValidation verifies that the Metal UploadV210/DownloadV210
// enforce the same width%6==0 / height%2==0 input contract as the CUDA path.
// Without these guards the v210 shader silently leaves the trailing 1-5 luma
// pixels of each row unwritten (non-multiple-of-6 width) and truncates the
// bottom row's chroma (odd height) — a silent-correctness divergence between
// the Metal and CUDA backends.
func TestMetalV210UploadValidation(t *testing.T) {
	ctx := testContext(t)

	// A valid frame so we get past the nil guard and reach the dimension checks.
	const w, h = 12, 4
	pool, err := NewFramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	// Buffer large enough that the size check never fires before the dim checks.
	buf := make([]byte, V210LineStride(w)*h)

	t.Run("upload non-multiple-of-6 width rejected", func(t *testing.T) {
		err := UploadV210(ctx, frame, buf, 13, h)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not divisible by 6")
	})

	t.Run("upload odd height rejected", func(t *testing.T) {
		err := UploadV210(ctx, frame, buf, w, 5)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not even")
	})

	t.Run("download non-multiple-of-6 width rejected", func(t *testing.T) {
		err := DownloadV210(ctx, buf, frame, 13, h)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not divisible by 6")
	})

	t.Run("download odd height rejected", func(t *testing.T) {
		err := DownloadV210(ctx, buf, frame, w, 5)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not even")
	})

	t.Run("valid dimensions pass validation", func(t *testing.T) {
		// Must not fail with a validation error. The conversion itself may
		// succeed or fail on the device, but it must get PAST the dimension
		// guards (i.e., the error, if any, is not the validation message).
		if err := UploadV210(ctx, frame, buf, w, h); err != nil {
			assert.NotContains(t, err.Error(), "not divisible by 6")
			assert.NotContains(t, err.Error(), "not even")
		}
	})
}
