//go:build cgo && darwin

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFramePool_ReleaseAfterClose_FreesFrame verifies that a frame checked
// out (in flight) when the pool is closed is freed on its final Release
// instead of being re-pooled into the dead pool's free list, where its GPU
// allocation would leak forever (finding #92).
func TestFramePool_ReleaseAfterClose_FreesFrame(t *testing.T) {
	ctx := testContext(t)

	pool, err := NewFramePool(ctx, 32, 32, 2)
	require.NoError(t, err)

	// Check out a frame, then close the pool while the frame is in flight.
	frame, err := pool.Acquire()
	require.NoError(t, err)
	pool.Close()

	// The in-flight frame's memory must remain valid until it is released
	// (Close must not free memory out from under the holder).
	require.NotNil(t, frame.MetalBuf,
		"Close must not free an in-flight frame's buffer")

	// Releasing after Close must free the GPU buffer instead of re-pooling it.
	frame.Release()

	pool.mu.Lock()
	freeLen := len(pool.free)
	pool.mu.Unlock()
	assert.Equal(t, 0, freeLen,
		"released frame must not be re-pooled after Close")
	assert.Nil(t, frame.MetalBuf,
		"released frame's GPU buffer must be freed after pool Close")
}

// TestGPUFrame_RefKeepsFrameAliveAcrossOwnerRelease verifies the refcount
// invariant the GetFrame/MaskForSource protocols rely on: a frame Ref'd by a
// reader is not recycled when the original owner releases it; only the last
// Release returns it to the pool.
func TestGPUFrame_RefKeepsFrameAliveAcrossOwnerRelease(t *testing.T) {
	ctx := testContext(t)

	pool, err := NewFramePool(ctx, 32, 32, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)

	frame.Ref()     // reader takes a reference
	frame.Release() // owner drops its reference

	// The frame must NOT be in the free list while the reader's ref is live.
	require.False(t, framePoolFreeContains(pool, frame),
		"frame recycled while a reader still holds a reference")
	require.NotNil(t, frame.MetalBuf)

	frame.Release() // reader's final release recycles it
	require.True(t, framePoolFreeContains(pool, frame),
		"frame should return to the pool after the last release")
}

// framePoolFreeContains reports whether frame is in the pool's free list.
func framePoolFreeContains(p *FramePool, frame *GPUFrame) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, f := range p.free {
		if f == frame {
			return true
		}
	}
	return false
}

// TestFramePool_AcquireAfterClose_FreedOnRelease verifies that a frame
// allocated by Acquire after Close (pool-miss path) is freed on Release
// rather than parked in the closed pool's free list.
func TestFramePool_AcquireAfterClose_FreedOnRelease(t *testing.T) {
	ctx := testContext(t)

	pool, err := NewFramePool(ctx, 32, 32, 1)
	require.NoError(t, err)
	pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	require.NotNil(t, frame.MetalBuf)

	frame.Release()

	pool.mu.Lock()
	freeLen := len(pool.free)
	pool.mu.Unlock()
	assert.Equal(t, 0, freeLen,
		"frame released after Close must not be re-pooled")
	assert.Nil(t, frame.MetalBuf,
		"frame released after Close must have its GPU buffer freed")
}
