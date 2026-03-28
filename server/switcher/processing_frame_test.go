package switcher

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProcessingFrameYUVSize(t *testing.T) {
	pf := &ProcessingFrame{
		Width:  8,
		Height: 8,
		Data:    make([]byte, ColorYUV420_8bit.FrameSize(8, 8)),
	}
	require.Equal(t, ColorYUV420_8bit.FrameSize(8, 8), len(pf.Data))
	require.Equal(t, 8, pf.Width)
	require.Equal(t, 8, pf.Height)
}

func TestProcessingFrameDeepCopy(t *testing.T) {
	original := &ProcessingFrame{
		Width:      4,
		Height:     4,
		Data:        []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24},
		PTS:        1000,
		DTS:        900,
		IsKeyframe: true,
		GroupID:    5,
		Codec:      "h264",
	}

	copied := original.DeepCopy()

	require.Equal(t, original.Width, copied.Width)
	require.Equal(t, original.Height, copied.Height)
	require.Equal(t, original.PTS, copied.PTS)
	require.Equal(t, original.DTS, copied.DTS)
	require.Equal(t, original.IsKeyframe, copied.IsKeyframe)
	require.Equal(t, original.GroupID, copied.GroupID)
	require.Equal(t, original.Codec, copied.Codec)
	require.Equal(t, original.Data, copied.Data)

	// Verify it's a true deep copy
	copied.Data[0] = 255
	require.NotEqual(t, original.Data[0], copied.Data[0])
}

func TestProcessingFrameDeepCopyWithPool(t *testing.T) {
	pool := NewFramePool(4, 4, 4, ColorYUV420_8bit)
	defer pool.Close()

	original := &ProcessingFrame{
		Width:      4,
		Height:     4,
		Data:        pool.Acquire(),
		PTS:        1000,
		DTS:        900,
		IsKeyframe: true,
		GroupID:    5,
		Codec:      "h264",
		pool:       pool,
	}
	copy(original.Data, []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24})

	copied := original.DeepCopy()

	// Copy inherits pool reference
	require.NotNil(t, copied.pool)

	// Data is independent
	require.Equal(t, original.Data, copied.Data)
	copied.Data[0] = 255
	require.NotEqual(t, original.Data[0], copied.Data[0])

	// Release both — should go back to pool
	original.ReleaseYUV()
	copied.ReleaseYUV()

	hits, misses := pool.Stats()
	require.Equal(t, uint64(2), hits)   // 2 acquires (original + copy)
	require.Equal(t, uint64(0), misses) // pool had 4 slots
}

func TestProcessingFrameReleaseYUVWithPool(t *testing.T) {
	pool := NewFramePool(4, 8, 8, ColorYUV420_8bit)
	defer pool.Close()

	pf := &ProcessingFrame{
		Width:  8,
		Height: 8,
		Data:    pool.Acquire(),
		pool:   pool,
	}

	pf.ReleaseYUV()
	require.Nil(t, pf.Data)

	// Double release is safe
	pf.ReleaseYUV()
}

func TestProcessingFrame_TimestampsDeepCopy(t *testing.T) {
	original := &ProcessingFrame{
		Width:           4,
		Height:          4,
		Data:             make([]byte, ColorYUV420_8bit.FrameSize(4, 4)),
		PTS:             1000,
		ArrivalNano:     100000,
		DecodeStartNano: 200000,
		DecodeEndNano:   300000,
		SyncReleaseNano: 400000,
	}

	copied := original.DeepCopy()

	require.Equal(t, original.ArrivalNano, copied.ArrivalNano, "ArrivalNano should be preserved")
	require.Equal(t, original.DecodeStartNano, copied.DecodeStartNano, "DecodeStartNano should be preserved")
	require.Equal(t, original.DecodeEndNano, copied.DecodeEndNano, "DecodeEndNano should be preserved")
	require.Equal(t, original.SyncReleaseNano, copied.SyncReleaseNano, "SyncReleaseNano should be preserved")

	// Verify modifying the copy doesn't affect the original's YUV
	copied.Data[0] = 255
	require.NotEqual(t, original.Data[0], copied.Data[0])
}

func TestReleaseYUVUnderflowLogsAndLeaks(t *testing.T) {
	pool := NewFramePool(4, 8, 8, ColorYUV420_8bit)
	defer pool.Close()

	pf := &ProcessingFrame{
		Width:  8,
		Height: 8,
		Data:    pool.Acquire(),
		pool:   pool,
	}
	pf.SetRefs(1)

	// Create a value copy that shares the refs pointer (simulates frame_sync
	// delivery or pipeline shallow copy). Both copies have non-nil YUV and
	// share the same atomic refcount.
	copy1 := *pf

	// First release via original: valid, drops refs to 0, returns buffer to pool.
	pf.ReleaseYUV()

	// Second release via the copy: refs goes to -1 (underflow).
	// Should NOT panic — logs the error and leaks the buffer instead of
	// corrupting pool state. A leaked buffer is invisible to viewers;
	// a panic takes down the broadcast.
	require.NotPanics(t, func() {
		copy1.ReleaseYUV()
	}, "double ReleaseYUV should log and leak, not panic")

	// Refs should be -1 (underflow detected, buffer not returned to pool).
	require.Equal(t, int32(-1), copy1.Refs())
}

func TestDeepCopyPoolSizeMismatch(t *testing.T) {
	// Create a small pool (2x2 = 6 bytes YUV420)
	pool := NewFramePool(4, 2, 2, ColorYUV420_8bit)
	defer pool.Close()

	// Create a frame with YUV larger than the pool's bufSize (ColorYUV420_8bit.FrameSize(2, 2) = 6 bytes).
	// Use an 8x8 frame = 96 bytes, which exceeds pool.bufSize of 6.
	largeYUV := make([]byte, ColorYUV420_8bit.FrameSize(8, 8))
	for i := range largeYUV {
		largeYUV[i] = byte(i % 256)
	}

	original := &ProcessingFrame{
		Width:  8,
		Height: 8,
		Data:    largeYUV,
		PTS:    2000,
		pool:   pool,
	}

	// DeepCopy must NOT panic even though len(YUV) > pool.bufSize.
	// It should fall back to make() instead of pool.Acquire()[:len(YUV)].
	var copied *ProcessingFrame
	require.NotPanics(t, func() {
		copied = original.DeepCopy()
	})

	// Verify data was copied correctly
	require.Equal(t, original.Data, copied.Data)
	require.Equal(t, original.Width, copied.Width)
	require.Equal(t, original.Height, copied.Height)
	require.Equal(t, original.PTS, copied.PTS)

	// Verify it's a true deep copy
	copied.Data[0] = 255
	require.NotEqual(t, original.Data[0], copied.Data[0])
}

func TestProcessingFrameGPUData(t *testing.T) {
	// GPUData is typed as `any` to avoid circular import with gpu package.
	// Verify it can hold an arbitrary pointer and survives DeepCopy.
	type fakeGPU struct{ id int }

	pf := &ProcessingFrame{
		Width:  4,
		Height: 4,
		Data:    make([]byte, ColorYUV420_8bit.FrameSize(4, 4)),
	}

	// Initially nil.
	require.Nil(t, pf.GPUData)

	// Set a fake GPU frame.
	pf.GPUData = &fakeGPU{id: 42}
	require.NotNil(t, pf.GPUData)

	fg, ok := pf.GPUData.(*fakeGPU)
	require.True(t, ok)
	require.Equal(t, 42, fg.id)

	// DeepCopy preserves GPUData reference (shallow copy — the GPU
	// upload/download nodes manage GPU frame lifecycle explicitly).
	cp := pf.DeepCopy()
	require.NotNil(t, cp.GPUData)
	fg2, ok := cp.GPUData.(*fakeGPU)
	require.True(t, ok)
	require.Equal(t, 42, fg2.id)
	require.Same(t, fg, fg2, "DeepCopy should shallow-copy GPUData pointer")
}

func TestProcessingFrameGPUDataRefCounting(t *testing.T) {
	// Verify GPUData Ref/Release lifecycle through DeepCopy and ReleaseYUV.
	fakeRC := &mockGPURefCounter{refs: 1}

	pf := &ProcessingFrame{
		Width:   4,
		Height:  4,
		Data:     make([]byte, ColorYUV420_8bit.FrameSize(4, 4)),
		GPUData: fakeRC,
	}

	// DeepCopy should call Ref() on GPUData
	cp := pf.DeepCopy()
	require.NotNil(t, cp.GPUData)
	require.Equal(t, 2, fakeRC.refs, "DeepCopy should increment GPU ref count")

	// ReleaseYUV on copy should call Release() on GPUData
	cp.ReleaseYUV()
	require.Equal(t, 1, fakeRC.refs, "ReleaseYUV should decrement GPU ref count")
	require.Nil(t, cp.GPUData, "GPUData should be nil after ReleaseYUV")

	// ReleaseYUV on original
	pf.ReleaseYUV()
	require.Equal(t, 0, fakeRC.refs, "Original ReleaseYUV should decrement GPU ref count")
}

// mockGPURefCounter implements GPURefCounter for testing.
type mockGPURefCounter struct {
	refs int
}

func (m *mockGPURefCounter) Ref()     { m.refs++ }
func (m *mockGPURefCounter) Release() { m.refs-- }

// GPUData must only be released when the LAST reference drops (refs→0),
// not on every ReleaseYUV call. With refcounted frames shared across
// goroutines (e.g., async encode), premature GPU release corrupts state.
func TestProcessingFrame_GPUData_OnlyReleasedOnFinalRef(t *testing.T) {
	fakeRC := &mockGPURefCounter{refs: 1}

	pf := &ProcessingFrame{
		Width:   4,
		Height:  4,
		Data:     make([]byte, ColorYUV420_8bit.FrameSize(4, 4)),
		GPUData: fakeRC,
	}
	pf.SetRefs(2) // simulate pipeline ref + async encode ref

	// First release (e.g., pipeline done): refs 2→1.
	// GPUData must NOT be released yet — async encode still needs it.
	pf.ReleaseYUV()
	require.Equal(t, 1, fakeRC.refs, "GPU ref should NOT be released when YUV refs > 0")
	require.Equal(t, int32(1), pf.Refs(), "YUV refs should be 1")

	// Second release (e.g., encode goroutine done): refs 1→0.
	// NOW GPUData should be released.
	pf.ReleaseYUV()
	require.Equal(t, 0, fakeRC.refs, "GPU ref should be released when YUV refs reach 0")
}

func TestProcessingFrameNilPoolFallback(t *testing.T) {
	// No pool — DeepCopy falls back to make()
	original := &ProcessingFrame{
		Width:  4,
		Height: 4,
		Data:    make([]byte, ColorYUV420_8bit.FrameSize(4, 4)),
	}

	copied := original.DeepCopy()
	require.Len(t, copied.Data, ColorYUV420_8bit.FrameSize(4, 4))

	// ReleaseYUV with nil pool doesn't panic
	copied.ReleaseYUV()
	require.Nil(t, copied.Data)
}
