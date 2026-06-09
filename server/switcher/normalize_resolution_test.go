package switcher

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNormalizeResolution_NilYUV_NoPanic reproduces a production panic where
// a ProcessingFrame with Width/Height set but nil YUV reached normalizeResolution.
// This happens when frame_sync delivers a value copy of lastRawVideo whose
// underlying pool buffer was freed by another goroutine (refcount dropped to 0
// sets YUV=nil, but Width/Height remain). ScaleYUV420 then panics on
// src[:srcYSize] with capacity 0.
func TestNormalizeResolution_NilYUV_NoPanic(t *testing.T) {
	pool := NewFramePool(4, 320, 240, ColorYUV420_8bit)
	sw := &Switcher{}
	sw.framePool.Store(pool)
	// Pipeline format: 320×240 (matching pool).
	pf := PipelineFormat{Width: 320, Height: 240}
	sw.pipelineFormat.Store(&pf)

	// Frame with dimensions that differ from pipeline format, triggering
	// ScaleYUV420 — but YUV is nil (freed by another goroutine).
	frame := &ProcessingFrame{
		Data:   nil,
		Width:  160,
		Height: 120,
		PTS:    12345,
	}

	// Before the fix, this panics with:
	//   runtime error: slice bounds out of range [:19200] with capacity 0
	result := sw.normalizeResolution(frame)

	// Should return the original frame unchanged (skip scaling).
	require.Same(t, frame, result, "should return original frame when YUV is nil")
}

// TestNormalizeResolution_EmptyYUV_NoPanic tests the same scenario but with
// an empty (non-nil, zero-length) YUV slice.
func TestNormalizeResolution_EmptyYUV_NoPanic(t *testing.T) {
	pool := NewFramePool(4, 320, 240, ColorYUV420_8bit)
	sw := &Switcher{}
	sw.framePool.Store(pool)
	pf := PipelineFormat{Width: 320, Height: 240}
	sw.pipelineFormat.Store(&pf)

	frame := &ProcessingFrame{
		Data:   []byte{},
		Width:  160,
		Height: 120,
		PTS:    12345,
	}

	result := sw.normalizeResolution(frame)
	require.Same(t, frame, result, "should return original frame when YUV is empty")
}
