//go:build correctness

package switcher

import (
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// TestFramePool10bit_BufferSize
//
// Pool with ColorYUV422_10bit allocates w*h*4 byte buffers.
// ---------------------------------------------------------------------------

func TestFramePool10bit_BufferSize(t *testing.T) {
	fp := NewFramePool(4, 1920, 1080, ColorYUV422_10bit)
	defer fp.Close()

	expectedSize := ColorYUV422_10bit.FrameSize(1920, 1080)
	// YUV422P10LE: Y(1920*1080*2) + Cb(960*1080*2) + Cr(960*1080*2) = 1920*1080*4
	require.Equal(t, 1920*1080*4, expectedSize, "10-bit 422 frame size should be w*h*4")

	buf := fp.Acquire()
	require.Len(t, buf, expectedSize, "acquired buffer should be w*h*4 bytes")

	fp.Release(buf)

	hits, misses := fp.Stats()
	require.Equal(t, uint64(1), hits)
	require.Equal(t, uint64(0), misses)
}

// ---------------------------------------------------------------------------
// TestFramePool10bit_SmallFrame
//
// Pool with small dimensions still allocates correctly.
// ---------------------------------------------------------------------------

func TestFramePool10bit_SmallFrame(t *testing.T) {
	fp := NewFramePool(2, 8, 8, ColorYUV422_10bit)
	defer fp.Close()

	expectedSize := 8 * 8 * 4 // 256 bytes
	buf := fp.Acquire()
	require.Len(t, buf, expectedSize)

	fp.Release(buf)
}

// ---------------------------------------------------------------------------
// TestFramePool10bit_ConcurrentAcquireRelease
//
// Same concurrency test but with 10-bit buffer sizes.
// ---------------------------------------------------------------------------

func TestFramePool10bit_ConcurrentAcquireRelease(t *testing.T) {
	fp := NewFramePool(8, 320, 240, ColorYUV422_10bit)
	defer fp.Close()

	expectedSize := ColorYUV422_10bit.FrameSize(320, 240)
	const goroutines = 4
	const iterations = 1000

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				buf := fp.Acquire()
				require.Len(t, buf, expectedSize, "goroutine %d iter %d: wrong buffer size", id, i)

				// Write a unique pattern
				pattern := byte(id*37 + i%256)
				buf[0] = pattern

				// Verify it wasn't overwritten by another goroutine before release
				if buf[0] != pattern {
					t.Errorf("goroutine %d iter %d: buffer corrupted before release: wrote 0x%02X, got 0x%02X",
						id, i, pattern, buf[0])
				}

				fp.Release(buf)
			}
		}(g)
	}

	wg.Wait()

	hits, misses := fp.Stats()
	t.Logf("pool hits=%d, misses=%d (total acquires=%d)", hits, misses, goroutines*iterations)
}

// ---------------------------------------------------------------------------
// TestFramePool10bit_DoubleReleaseCorruption
//
// Same corruption detection with 10-bit buffers.
// Verifies that the duplicate-detection guard prevents aliasing.
// ---------------------------------------------------------------------------

func TestFramePool10bit_DoubleReleaseCorruption(t *testing.T) {
	fp := NewFramePool(2, 8, 8, ColorYUV422_10bit)
	defer fp.Close()

	expectedSize := ColorYUV422_10bit.FrameSize(8, 8) // 256 bytes

	// Acquire both pre-allocated buffers to empty the pool
	held := fp.Acquire()
	victim := fp.Acquire()
	require.Len(t, victim, expectedSize)

	// Double-release victim
	fp.Release(victim)
	fp.Release(victim) // should be detected as duplicate and ignored

	// Acquire two buffers
	buf1 := fp.Acquire()
	buf2 := fp.Acquire()

	ptr1 := unsafe.Pointer(&buf1[0])
	ptr2 := unsafe.Pointer(&buf2[0])
	aliased := ptr1 == ptr2

	t.Logf("buf1=%p, buf2=%p, aliased=%v", ptr1, ptr2, aliased)

	if aliased {
		t.Log("BUG: double release caused aliased buffers")

		// Demonstrate corruption
		for i := range buf1 {
			buf1[i] = 0xAA
		}
		for i := range buf2 {
			buf2[i] = 0xBB
		}

		corruptedCount := 0
		for i := range buf1 {
			if buf1[i] != 0xAA {
				corruptedCount++
			}
		}

		require.Zero(t, corruptedCount,
			"BUG: buf1 was corrupted by writes to buf2 - double release caused buffer aliasing")
	} else {
		t.Log("buffers are distinct - double release guard working correctly")
	}

	_ = held
}

// ---------------------------------------------------------------------------
// TestFramePool10bit_ExhaustedFallback
//
// When the pool is exhausted, Acquire falls back to heap allocation.
// ---------------------------------------------------------------------------

func TestFramePool10bit_ExhaustedFallback(t *testing.T) {
	fp := NewFramePool(2, 320, 240, ColorYUV422_10bit)
	defer fp.Close()

	expectedSize := ColorYUV422_10bit.FrameSize(320, 240)

	// Exhaust the pool
	buf1 := fp.Acquire()
	buf2 := fp.Acquire()

	// Third acquire - should fallback to heap
	buf3 := fp.Acquire()

	require.NotNil(t, buf3)
	require.Len(t, buf3, expectedSize)

	// Verify distinct allocations
	ptr1 := unsafe.Pointer(&buf1[0])
	ptr2 := unsafe.Pointer(&buf2[0])
	ptr3 := unsafe.Pointer(&buf3[0])
	t.Logf("buf1=%p, buf2=%p, buf3=%p", ptr1, ptr2, ptr3)

	require.NotEqual(t, ptr1, ptr3)
	require.NotEqual(t, ptr2, ptr3)

	_, misses := fp.Stats()
	require.Equal(t, uint64(1), misses)

	fp.Release(buf1)
	fp.Release(buf2)
	fp.Release(buf3)
}

// ---------------------------------------------------------------------------
// TestFramePool10bit_SizeVs8bit
//
// Verify that 10-bit pool allocates 4x the size of 8-bit for same resolution.
// YUV420 8-bit: w*h*3/2, YUV422 10-bit: w*h*4
// ---------------------------------------------------------------------------

func TestFramePool10bit_SizeVs8bit(t *testing.T) {
	w, h := 1920, 1080

	size8bit := ColorYUV420_8bit.FrameSize(w, h)
	size10bit := ColorYUV422_10bit.FrameSize(w, h)

	t.Logf("8-bit YUV420 frame: %d bytes", size8bit)
	t.Logf("10-bit YUV422 frame: %d bytes", size10bit)

	// 8-bit: 1920*1080*3/2 = 3,110,400
	require.Equal(t, w*h*3/2, size8bit)
	// 10-bit: 1920*1080*4 = 8,294,400
	require.Equal(t, w*h*4, size10bit)

	// The 10-bit frame is exactly 8/3 times larger
	ratio := float64(size10bit) / float64(size8bit)
	t.Logf("10-bit/8-bit ratio: %.4f (expected ~2.667)", ratio)

	// Allocate both and verify sizes
	fp8 := NewFramePool(1, w, h, ColorYUV420_8bit)
	defer fp8.Close()
	fp10 := NewFramePool(1, w, h, ColorYUV422_10bit)
	defer fp10.Close()

	buf8 := fp8.Acquire()
	buf10 := fp10.Acquire()

	require.Len(t, buf8, size8bit)
	require.Len(t, buf10, size10bit)

	fp8.Release(buf8)
	fp10.Release(buf10)
}

// ---------------------------------------------------------------------------
// TestProcessingFrame10bit_RefCountUnderflow
//
// Same as TestProcessingFrameRefCountUnderflow but with 10-bit pool.
// ---------------------------------------------------------------------------

func TestProcessingFrame10bit_RefCountUnderflow(t *testing.T) {
	pool := NewFramePool(4, 8, 8, ColorYUV422_10bit)
	defer pool.Close()

	pf := &ProcessingFrame{
		Width:  8,
		Height: 8,
		Format: ColorYUV422_10bit,
		Data:   pool.Acquire(),
		pool:   pool,
	}
	pf.SetRefs(1)

	// Create a value copy
	copy1 := *pf

	pf.ReleaseYUV()

	// Second release via value copy
	require.NotPanics(t, func() {
		copy1.ReleaseYUV()
	}, "double ReleaseYUV via value copy should not panic")

	refsAfter := copy1.Refs()
	t.Logf("refs after double release: %d (underflow if < 0)", refsAfter)
	require.Equal(t, int32(-1), refsAfter)
}

// ---------------------------------------------------------------------------
// TestProcessingFrame10bit_RefCountConcurrent
//
// Concurrent refcount operations with 10-bit frames.
// ---------------------------------------------------------------------------

func TestProcessingFrame10bit_RefCountConcurrent(t *testing.T) {
	pool := NewFramePool(4, 8, 8, ColorYUV422_10bit)
	defer pool.Close()

	const numConsumers = 4

	pf := &ProcessingFrame{
		Width:  8,
		Height: 8,
		Format: ColorYUV422_10bit,
		Data:   pool.Acquire(),
		pool:   pool,
	}
	pf.SetRefs(int32(numConsumers))

	var releasedCount atomic.Int32

	copies := make([]ProcessingFrame, numConsumers)
	for i := 0; i < numConsumers; i++ {
		copies[i] = *pf
	}

	var wg sync.WaitGroup
	for i := 0; i < numConsumers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			refsBefore := copies[idx].Refs()
			copies[idx].ReleaseYUV()
			refsAfter := copies[idx].Refs()

			if refsBefore >= 1 && refsAfter <= 0 {
				releasedCount.Add(1)
			}
		}(i)
	}

	wg.Wait()

	finalRefs := pf.Refs()
	t.Logf("final refs=%d, goroutines that saw 0=%d", finalRefs, releasedCount.Load())

	require.Equal(t, int32(0), finalRefs, "final refs should be exactly 0")
	require.GreaterOrEqual(t, finalRefs, int32(0), "refs should not go below 0")
}

// ---------------------------------------------------------------------------
// TestFramePool10bit_AcquireAfterClose
//
// Acquire does not panic after pool is closed.
// ---------------------------------------------------------------------------

func TestFramePool10bit_AcquireAfterClose(t *testing.T) {
	fp := NewFramePool(4, 320, 240, ColorYUV422_10bit)
	expectedSize := ColorYUV422_10bit.FrameSize(320, 240)

	fp.Close()

	var buf []byte
	require.NotPanics(t, func() {
		buf = fp.Acquire()
	}, "Acquire after Close should not panic")

	require.NotNil(t, buf)
	require.Len(t, buf, expectedSize)

	_, misses := fp.Stats()
	require.Equal(t, uint64(1), misses, "should be a pool miss since pool was closed")
}

// ---------------------------------------------------------------------------
// TestFramePool10bit_ReleaseAfterClose
//
// Release after close does not panic.
// ---------------------------------------------------------------------------

func TestFramePool10bit_ReleaseAfterClose(t *testing.T) {
	fp := NewFramePool(4, 8, 8, ColorYUV422_10bit)
	buf := fp.Acquire()

	fp.Close()

	require.NotPanics(t, func() {
		fp.Release(buf)
	}, "Release after Close should not panic")
}
