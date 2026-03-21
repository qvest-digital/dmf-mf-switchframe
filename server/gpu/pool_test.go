//go:build cgo && cuda

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFramePool(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	pool, err := NewFramePool(ctx, 1920, 1080, 4)
	require.NoError(t, err)
	defer pool.Close()

	// Acquire frame
	frame, err := pool.Acquire()
	require.NoError(t, err)
	assert.Equal(t, 1920, frame.Width)
	assert.Equal(t, 1080, frame.Height)
	assert.NotZero(t, frame.DevPtr)
	assert.GreaterOrEqual(t, frame.Pitch, 1920, "pitch should be at least width")

	t.Logf("Pool pitch: %d (width: %d)", frame.Pitch, frame.Width)

	// Release back to pool
	frame.Release()

	// Re-acquire should return same buffer (LIFO)
	frame2, err := pool.Acquire()
	require.NoError(t, err)
	assert.Equal(t, frame.DevPtr, frame2.DevPtr, "LIFO reuse: same GPU memory")
	frame2.Release()
}

func TestFramePoolExhaustion(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	pool, err := NewFramePool(ctx, 320, 240, 2)
	require.NoError(t, err)
	defer pool.Close()

	// Acquire all pre-allocated frames
	f1, err := pool.Acquire()
	require.NoError(t, err)
	f2, err := pool.Acquire()
	require.NoError(t, err)

	// Third acquire should still work (pool miss → fresh allocation)
	f3, err := pool.Acquire()
	require.NoError(t, err)
	assert.NotEqual(t, f1.DevPtr, f3.DevPtr)
	assert.NotEqual(t, f2.DevPtr, f3.DevPtr)

	hits, misses := pool.Stats()
	assert.Equal(t, uint64(2), hits)
	assert.Equal(t, uint64(1), misses)

	f1.Release()
	f2.Release()
	f3.Release()
}

func TestFramePoolRefCounting(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	pool, err := NewFramePool(ctx, 320, 240, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	devPtr := frame.DevPtr

	// Add extra reference
	frame.Ref()

	// First release should not return to pool (refs still > 0)
	frame.Release()
	// Frame should still have valid pointer
	assert.Equal(t, devPtr, frame.DevPtr)

	// Second release drops to 0, returns to pool
	frame.Release()

	// Verify it was returned — re-acquire should get same pointer
	frame2, err := pool.Acquire()
	require.NoError(t, err)
	assert.Equal(t, devPtr, frame2.DevPtr)
	frame2.Release()
}

func TestFramePoolNilContext(t *testing.T) {
	_, err := NewFramePool(nil, 1920, 1080, 4)
	require.ErrorIs(t, err, ErrGPUNotAvailable)
}
