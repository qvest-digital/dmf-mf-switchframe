package switcher

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
)

func TestFramePoolAcquireRelease(t *testing.T) {
	fp := NewFramePool(4, 320, 240, ColorYUV420_8bit)
	defer fp.Close()

	bufSize := ColorYUV420_8bit.FrameSize(320, 240)

	buf := fp.Acquire()
	require.Len(t, buf, bufSize)

	fp.Release(buf)

	hits, misses := fp.Stats()
	require.Equal(t, uint64(1), hits)
	require.Equal(t, uint64(0), misses)
}

func TestFramePoolExhaustion(t *testing.T) {
	fp := NewFramePool(2, 8, 8, ColorYUV420_8bit)
	defer fp.Close()

	// Acquire all pre-allocated buffers
	b1 := fp.Acquire()
	b2 := fp.Acquire()

	// Third acquire should fallback to make()
	b3 := fp.Acquire()

	hits, misses := fp.Stats()
	require.Equal(t, uint64(2), hits)
	require.Equal(t, uint64(1), misses)

	require.Len(t, b3, ColorYUV420_8bit.FrameSize(8, 8))

	fp.Release(b1)
	fp.Release(b2)
	fp.Release(b3) // fallback alloc — released back if pool has room
}

func TestFramePoolWrongSizeDiscard(t *testing.T) {
	fp := NewFramePool(2, 320, 240, ColorYUV420_8bit)
	defer fp.Close()

	// Release a wrong-sized buffer — should be discarded
	tooSmall := make([]byte, 10)
	fp.Release(tooSmall)

	// Pool should still have 2 pre-allocated buffers
	b1 := fp.Acquire()
	b2 := fp.Acquire()
	hits, _ := fp.Stats()
	require.Equal(t, uint64(2), hits)

	fp.Release(b1)
	fp.Release(b2)
}

func TestFramePoolLIFOCacheWarmth(t *testing.T) {
	fp := NewFramePool(4, 8, 8, ColorYUV420_8bit)
	defer fp.Close()

	// Acquire 2 buffers
	b1 := fp.Acquire()
	b2 := fp.Acquire()

	// Mark them distinctly
	b1[0] = 0xAA
	b2[0] = 0xBB

	// Release in order: b1 then b2
	fp.Release(b1)
	fp.Release(b2)

	// LIFO: next acquire should return b2 (last released)
	got := fp.Acquire()
	require.Equal(t, byte(0xBB), got[0])
}

func TestFramePoolCapLimit(t *testing.T) {
	fp := NewFramePool(2, 8, 8, ColorYUV420_8bit)
	defer fp.Close()

	// Acquire all 2, then acquire 3 more (fallback allocs)
	bufs := make([][]byte, 5)
	for i := range bufs {
		bufs[i] = fp.Acquire()
	}

	// Release all 5 — only 2 should be retained (pool cap)
	for _, b := range bufs {
		fp.Release(b)
	}

	// Acquire 3: first 2 are hits, third is a miss (pool drained)
	_ = fp.Acquire()
	_ = fp.Acquire()
	_ = fp.Acquire()

	hits, misses := fp.Stats()
	// 2 initial hits + 2 reused = 4 hits total
	// 3 initial misses + 1 extra miss = 4 misses total
	require.Equal(t, uint64(4), hits)
	require.Equal(t, uint64(4), misses)
}

func TestFramePoolClose(t *testing.T) {
	fp := NewFramePool(4, 8, 8, ColorYUV420_8bit)
	fp.Close()

	// After close, Acquire falls back to make()
	buf := fp.Acquire()
	require.Len(t, buf, ColorYUV420_8bit.FrameSize(8, 8))
	_, misses := fp.Stats()
	require.Equal(t, uint64(1), misses)
}

func TestFramePoolFormatChange(t *testing.T) {
	// Simulate format change: old pool buffers are wrong size for new pool
	oldPool := NewFramePool(4, 320, 240, ColorYUV420_8bit)
	newPool := NewFramePool(4, 640, 480, ColorYUV420_8bit)

	oldBuf := oldPool.Acquire()
	require.Len(t, oldBuf, ColorYUV420_8bit.FrameSize(320, 240))

	// Release old buffer to new pool — should be discarded (too small)
	newPool.Release(oldBuf)

	// New pool should still have all 4 pre-allocated buffers
	for i := 0; i < 4; i++ {
		buf := newPool.Acquire()
		require.Len(t, buf, ColorYUV420_8bit.FrameSize(640, 480))
	}

	hits, _ := newPool.Stats()
	require.Equal(t, uint64(4), hits)

	oldPool.Close()
	newPool.Close()
}

func TestFramePool_MmapBacked(t *testing.T) {
	fp := NewFramePool(4, 64, 48, ColorYUV420_8bit)
	defer fp.Close()

	buf := fp.Acquire()
	require.Len(t, buf, ColorYUV420_8bit.FrameSize(64, 48))

	// Write and read back to verify the mmap region is usable.
	buf[0] = 0xAB
	buf[len(buf)-1] = 0xCD
	require.Equal(t, byte(0xAB), buf[0])
	require.Equal(t, byte(0xCD), buf[len(buf)-1])

	fp.Release(buf)

	hits, misses := fp.Stats()
	require.Equal(t, uint64(1), hits)
	require.Equal(t, uint64(0), misses)
}

func TestFramePool_CloseFreesMemory(t *testing.T) {
	fp := NewFramePool(4, 64, 48, ColorYUV420_8bit)
	// Acquire and release to exercise the pool.
	buf := fp.Acquire()
	fp.Release(buf)
	fp.Close()
	// After close, acquire falls back to make().
	buf2 := fp.Acquire()
	require.Len(t, buf2, ColorYUV420_8bit.FrameSize(64, 48))
}

func TestFramePool_MmapBufsTracked(t *testing.T) {
	fp := NewFramePool(8, 64, 48, ColorYUV420_8bit)

	// All pre-allocated buffers should be tracked as mmap'd.
	fp.mu.Lock()
	mmapCount := len(fp.mmapBufs)
	fp.mu.Unlock()

	require.Equal(t, 8, mmapCount)

	fp.Close()

	// After close, mmapBufs should be nil.
	fp.mu.Lock()
	require.Nil(t, fp.mmapBufs)
	fp.mu.Unlock()
}

func BenchmarkFramePoolAcquireRelease(b *testing.B) {
	fp := NewFramePool(8, 1920, 1080, ColorYUV420_8bit)
	defer fp.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf := fp.Acquire()
		fp.Release(buf)
	}
}

// Finding #87: Close used to munmap every pre-allocated buffer, including
// ones still checked out by in-flight frames (write → SIGSEGV), and Release
// had no closed flag, so a buffer released after Close re-entered the free
// list and a later Acquire handed out unmapped memory.

func TestFramePoolCloseKeepsCheckedOutBuffersMapped(t *testing.T) {
	fp := NewFramePool(4, 64, 48, ColorYUV420_8bit)

	buf := fp.Acquire() // checked out by an "in-flight frame"
	fp.Close()

	// The checked-out buffer must still be mapped and writable.
	buf[0] = 0xAB
	buf[len(buf)-1] = 0xCD
	require.Equal(t, byte(0xAB), buf[0])
	require.Equal(t, byte(0xCD), buf[len(buf)-1])

	// Returning it after Close must hand it to munmap, not the free list.
	fp.Release(buf)
	fp.mu.Lock()
	require.Empty(t, fp.free, "closed pool must not recycle buffers")
	require.Empty(t, fp.mmapBufs, "late-released mmap buffer should be unmapped and untracked")
	fp.mu.Unlock()
}

func TestFramePoolReleaseAfterCloseDoesNotRecycle(t *testing.T) {
	fp := NewFramePool(4, 64, 48, ColorYUV420_8bit)

	buf := fp.Acquire()
	bufAddr := uintptr(unsafe.Pointer(&buf[0]))
	fp.Close()
	fp.Release(buf)

	// A post-Close Acquire must get a fresh heap allocation, never the
	// released (now unmapped) buffer. Compare addresses as uintptr —
	// require.NotEqual on *byte would dereference the unmapped pointer.
	buf2 := fp.Acquire()
	require.NotEqual(t, bufAddr, uintptr(unsafe.Pointer(&buf2[0])),
		"Acquire after Close must not return the unmapped buffer")
	buf2[0] = 1 // must be writable
	require.Equal(t, byte(1), buf2[0])
}

func TestFramePoolCloseIdempotent(t *testing.T) {
	fp := NewFramePool(4, 64, 48, ColorYUV420_8bit)
	fp.Close()
	fp.Close() // second Close must be a no-op, not a double munmap
}
