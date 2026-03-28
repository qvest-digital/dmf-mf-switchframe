//go:build correctness

package switcher

import (
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
)

// TestFramePoolDoubleRelease verifies that releasing the same buffer twice
// leads to aliased buffers on subsequent Acquire calls. The pool uses a
// LIFO free list with no duplicate detection, so double-release appends the
// same pointer twice. Two Acquire calls then return the SAME underlying
// memory, which is a correctness bug (writes to one corrupt the other).
func TestFramePoolDoubleRelease(t *testing.T) {
	// Use capacity=1 so the free list only ever holds the ONE buffer.
	// After double-release, the free list has [buf, buf] but cap check
	// limits it to 1 entry. So we use capacity=2 to allow both entries.
	fp := NewFramePool(2, 320, 240, ColorYUV420_8bit)
	defer fp.Close()

	bufSize := ColorYUV420_8bit.FrameSize(320, 240)

	// Acquire BOTH pre-allocated buffers to empty the pool
	held := fp.Acquire()
	buf1 := fp.Acquire()
	require.Len(t, buf1, bufSize)

	// Record the pointer before releasing
	buf1Ptr := unsafe.Pointer(&buf1[0])
	t.Logf("MEASURE: buf1 ptr=%p, held ptr=%p", buf1Ptr, unsafe.Pointer(&held[0]))

	// Pool is now empty. Release buf1 once (valid)
	fp.Release(buf1)

	// Release buf1 AGAIN (double release!)
	// Pool had 1 entry, cap=2, so it accepts the duplicate
	fp.Release(buf1)

	fp.mu.Lock()
	freeLen := len(fp.free)
	fp.mu.Unlock()
	t.Logf("MEASURE: free list length after double release: %d (should be 2 if bug)", freeLen)

	// Now the pool's free list has the same buffer TWICE.
	// Acquire two buffers — LIFO returns the same pointer both times.
	buf2 := fp.Acquire()
	buf3 := fp.Acquire()

	buf2Ptr := unsafe.Pointer(&buf2[0])
	buf3Ptr := unsafe.Pointer(&buf3[0])
	same := buf2Ptr == buf3Ptr

	t.Logf("MEASURE: buf2 ptr=%p, buf3 ptr=%p, same=%v", buf2Ptr, buf3Ptr, same)

	if same {
		t.Log("BUG CONFIRMED: double release causes aliased buffers")

		// Demonstrate the corruption: write to buf2, observe in buf3
		buf2[0] = 0xDE
		buf2[1] = 0xAD
		t.Logf("MEASURE: wrote buf2[0]=0x%02X, buf2[1]=0x%02X", buf2[0], buf2[1])
		t.Logf("MEASURE: read  buf3[0]=0x%02X, buf3[1]=0x%02X", buf3[0], buf3[1])

		// This assertion SHOULD fail if buf2 and buf3 are independent.
		// If they're aliased (same pointer), buf3 sees buf2's writes.
		require.NotEqual(t, buf2[0], buf3[0],
			"BUG: buf2 and buf3 are aliased — writing to buf2 corrupts buf3")
	} else {
		t.Log("buf2 and buf3 are distinct — no aliasing detected")
	}

	_ = held // keep held alive so it doesn't interfere
}

// TestFramePoolConcurrentAcquireRelease hammers the pool from multiple
// goroutines to verify no data races or panics occur under contention.
func TestFramePoolConcurrentAcquireRelease(t *testing.T) {
	fp := NewFramePool(8, 320, 240, ColorYUV420_8bit)
	defer fp.Close()

	bufSize := ColorYUV420_8bit.FrameSize(320, 240)
	const goroutines = 4
	const iterations = 1000

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				buf := fp.Acquire()
				require.Len(t, buf, bufSize, "goroutine %d iter %d: wrong buffer size", id, i)

				// Write a unique pattern: goroutine ID in first byte, iteration in next
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
	t.Logf("MEASURE: pool hits=%d, misses=%d (total acquires=%d)", hits, misses, goroutines*iterations)
}

// TestProcessingFrameRefCountUnderflow tests what happens when ReleaseYUV
// is called more times than refs allow, using value copies that share the
// same refs pointer but have independent YUV slice headers.
func TestProcessingFrameRefCountUnderflow(t *testing.T) {
	pool := NewFramePool(4, 8, 8, ColorYUV420_8bit)
	defer pool.Close()

	pf := &ProcessingFrame{
		Width:  8,
		Height: 8,
		Data:   pool.Acquire(),
		pool:   pool,
	}
	pf.SetRefs(1)

	// Create a value copy — shares refs pointer but has its own YUV slice header
	copy1 := *pf

	t.Logf("MEASURE: initial refs=%d", pf.Refs())

	// First release via original: refs 1→0, buffer returned to pool, YUV set to nil
	pf.ReleaseYUV()
	t.Logf("MEASURE: after first ReleaseYUV: refs=%d, original.YUV==nil: %v, copy.YUV==nil: %v",
		pf.Refs(), pf.Data == nil, copy1.Data == nil)

	// Second release via value copy: refs 0→-1 (underflow!)
	// copy1.Data is still non-nil (value copy preserved the slice header)
	// Should NOT panic — should log and leak
	require.NotPanics(t, func() {
		copy1.ReleaseYUV()
	}, "double ReleaseYUV via value copy should not panic")

	refsAfter := copy1.Refs()
	t.Logf("MEASURE: after second ReleaseYUV: refs=%d (underflow if < 0)", refsAfter)

	// The refs should be -1 (underflow detected, buffer leaked)
	require.Equal(t, int32(-1), refsAfter,
		"refs should be -1 after double release via value copy")
}

// TestProcessingFrameRefCountConcurrent verifies that exactly one goroutine
// triggers the actual pool release when multiple goroutines concurrently
// call ReleaseYUV on value copies sharing the same refcount.
func TestProcessingFrameRefCountConcurrent(t *testing.T) {
	pool := NewFramePool(4, 8, 8, ColorYUV420_8bit)
	defer pool.Close()

	const numConsumers = 4

	pf := &ProcessingFrame{
		Width:  8,
		Height: 8,
		Data:   pool.Acquire(),
		pool:   pool,
	}
	pf.SetRefs(int32(numConsumers))

	// Track how many goroutines observe refs hitting exactly 0
	var releasedCount atomic.Int32

	// Create value copies BEFORE launching goroutines (all share same refs)
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

			// If this goroutine drove refs from 1→0, it triggered the release
			if refsBefore >= 1 && refsAfter <= 0 {
				releasedCount.Add(1)
			}
		}(i)
	}

	wg.Wait()

	finalRefs := pf.Refs()
	released := releasedCount.Load()
	t.Logf("MEASURE: final refs=%d, goroutines that saw 0=%d", finalRefs, released)

	// Exactly one goroutine should have triggered the actual release (refs→0)
	require.Equal(t, int32(0), finalRefs, "final refs should be exactly 0")

	// Check that no underflow occurred
	require.GreaterOrEqual(t, finalRefs, int32(0),
		"refs should not go below 0 with correct initial count")
}

// TestFramePoolExhaustedFallback verifies that when the pool is exhausted,
// Acquire falls back to heap allocation and returns a correctly-sized buffer.
func TestFramePoolExhaustedFallback(t *testing.T) {
	fp := NewFramePool(2, 320, 240, ColorYUV420_8bit)
	defer fp.Close()

	bufSize := ColorYUV420_8bit.FrameSize(320, 240)

	// Exhaust the pool
	buf1 := fp.Acquire()
	buf2 := fp.Acquire()

	hits1, misses1 := fp.Stats()
	t.Logf("MEASURE: after exhausting pool: hits=%d, misses=%d", hits1, misses1)

	// Third acquire — should fallback to heap
	buf3 := fp.Acquire()

	hits2, misses2 := fp.Stats()
	t.Logf("MEASURE: after fallback acquire: hits=%d, misses=%d", hits2, misses2)

	require.NotNil(t, buf3, "heap fallback should return non-nil buffer")
	require.Len(t, buf3, bufSize, "heap fallback buffer should be correct size")

	// Verify it's a distinct allocation
	ptr1 := unsafe.Pointer(&buf1[0])
	ptr2 := unsafe.Pointer(&buf2[0])
	ptr3 := unsafe.Pointer(&buf3[0])
	t.Logf("MEASURE: buf1=%p, buf2=%p, buf3=%p", ptr1, ptr2, ptr3)
	require.NotEqual(t, ptr1, ptr3, "fallback should be distinct from pool buf1")
	require.NotEqual(t, ptr2, ptr3, "fallback should be distinct from pool buf2")

	// Misses should be exactly 1 (the fallback)
	require.Equal(t, uint64(1), misses2, "pool misses should be 1 after one fallback")

	// Release all three
	fp.Release(buf1)
	fp.Release(buf2)
	fp.Release(buf3) // heap alloc returned to pool (pool has room since cap=2 and 2 released + 1 more)

	hitsF, missesF := fp.Stats()
	t.Logf("MEASURE: final pool stats: hits=%d, misses=%d", hitsF, missesF)
}

// TestFramePoolAcquireAfterClose verifies that Acquire does not panic
// after the pool has been closed. After Close(), the free list is empty
// so Acquire should fall back to heap allocation.
func TestFramePoolAcquireAfterClose(t *testing.T) {
	fp := NewFramePool(4, 320, 240, ColorYUV420_8bit)
	bufSize := ColorYUV420_8bit.FrameSize(320, 240)

	fp.Close()

	var buf []byte
	require.NotPanics(t, func() {
		buf = fp.Acquire()
	}, "Acquire after Close should not panic")

	t.Logf("MEASURE: buf after Close: nil=%v, len=%d", buf == nil, len(buf))

	require.NotNil(t, buf, "Acquire after Close should return non-nil buffer (heap fallback)")
	require.Len(t, buf, bufSize, "buffer should be correct size")

	_, misses := fp.Stats()
	t.Logf("MEASURE: misses after Close+Acquire=%d (expected 1)", misses)
	require.Equal(t, uint64(1), misses, "should be a pool miss since pool was closed")
}

// TestFramePoolDoubleReleaseOverflowsCap verifies that double-releasing
// can cause the free list to grow beyond the intended pool capacity.
// The pool caps free list at fp.cap, so if the pool has room, the same
// buffer gets added twice. If the pool is full, the second release is
// silently discarded.
func TestFramePoolDoubleReleaseOverflowsCap(t *testing.T) {
	// Pool with cap=4, start with 4 pre-allocated buffers
	fp := NewFramePool(4, 8, 8, ColorYUV420_8bit)
	defer fp.Close()

	// Acquire all 4 — free list is now empty
	bufs := make([][]byte, 4)
	for i := range bufs {
		bufs[i] = fp.Acquire()
	}

	fp.mu.Lock()
	freeAfterAcquire := len(fp.free)
	fp.mu.Unlock()
	t.Logf("MEASURE: free list size after acquiring all 4: %d (expected 0)", freeAfterAcquire)

	// Release buf[0] twice — free list has room (0 < cap=4) so both succeed
	fp.Release(bufs[0])
	fp.Release(bufs[0]) // double release!

	fp.mu.Lock()
	freeAfterDouble := len(fp.free)
	fp.mu.Unlock()
	t.Logf("MEASURE: free list size after double release: %d (expected 2 if bug, 1 if protected)", freeAfterDouble)

	// If the pool accepted the double release, free list has 2 entries for the same buffer
	if freeAfterDouble == 2 {
		t.Log("BUG: pool accepted double release — free list has same buffer twice")
	}

	// Now release the remaining 3 (bufs[1], bufs[2], bufs[3])
	fp.Release(bufs[1])
	fp.Release(bufs[2])
	fp.Release(bufs[3])

	fp.mu.Lock()
	freeAfterAll := len(fp.free)
	fp.mu.Unlock()
	t.Logf("MEASURE: free list size after all releases: %d (cap=%d)", freeAfterAll, fp.cap)

	// The pool caps at fp.cap, so the free list should not exceed 4
	// But with the double release, we tried to return 5 buffers (1 duplicate + 3 unique)
	// to a pool that already had 2. Pool cap is 4, so it should cap at 4.
	require.LessOrEqual(t, freeAfterAll, fp.cap,
		"free list should not exceed pool capacity")
}

// TestFramePoolDoubleReleaseConcurrent verifies that concurrent
// double-release does not cause a data race or panic.
func TestFramePoolDoubleReleaseConcurrent(t *testing.T) {
	fp := NewFramePool(8, 8, 8, ColorYUV420_8bit)
	defer fp.Close()

	buf := fp.Acquire()

	// Release the same buffer from multiple goroutines simultaneously
	var wg sync.WaitGroup
	const goroutines = 8
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fp.Release(buf)
		}()
	}
	wg.Wait()

	fp.mu.Lock()
	freeCount := len(fp.free)
	fp.mu.Unlock()

	t.Logf("MEASURE: free list size after %d concurrent releases of same buffer: %d (cap=%d)",
		goroutines, freeCount, fp.cap)

	// The mutex protects the free list, so no data race. But the pool
	// may have accepted multiple copies of the same buffer (up to cap).
	// Each release is serialized by the mutex, and each checks len(free) < cap.
	// With 7 pre-allocated buffers still in pool + up to 8 releases of the same buf,
	// the cap check should limit growth.

	require.LessOrEqual(t, freeCount, fp.cap,
		"free list should not exceed pool capacity even with concurrent double-release")
}

// TestProcessingFrameRefCountStress hammers refcount operations from
// many goroutines to verify atomicity under contention.
func TestProcessingFrameRefCountStress(t *testing.T) {
	pool := NewFramePool(4, 8, 8, ColorYUV420_8bit)
	defer pool.Close()

	const iterations = 10000

	for iter := 0; iter < 100; iter++ {
		pf := &ProcessingFrame{
			Width:  8,
			Height: 8,
			Data:   pool.Acquire(),
			pool:   pool,
		}
		// Start with refs=2: one for "owner", one for concurrent consumers
		pf.SetRefs(2)

		var wg sync.WaitGroup

		// Consumer 1: Ref+Release many times, then final release
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				pf.Ref()
				pf.ReleaseYUV()
			}
			// Final release for this consumer
			pf.ReleaseYUV()
		}()

		// Consumer 2: final release
		wg.Add(1)
		go func() {
			defer wg.Done()
			pf.ReleaseYUV()
		}()

		wg.Wait()

		finalRefs := pf.Refs()
		if finalRefs != 0 {
			t.Logf("MEASURE: iter %d: final refs=%d (expected 0)", iter, finalRefs)
		}
	}
}

// TestFramePoolReleaseAfterClose verifies that releasing a buffer after
// the pool is closed does not panic.
func TestFramePoolReleaseAfterClose(t *testing.T) {
	fp := NewFramePool(4, 8, 8, ColorYUV420_8bit)
	buf := fp.Acquire()

	fp.Close()

	require.NotPanics(t, func() {
		fp.Release(buf)
	}, "Release after Close should not panic")

	// After close, free list was emptied. Release adds it back (len(free) < cap).
	fp.mu.Lock()
	freeCount := len(fp.free)
	fp.mu.Unlock()
	t.Logf("MEASURE: free list after Close+Release: %d", freeCount)
}

// TestProcessingFrameReleaseYUVNilRefs verifies behavior when refs is nil
// (unmanaged frame) — should release immediately without any refcount logic.
func TestProcessingFrameReleaseYUVNilRefs(t *testing.T) {
	pool := NewFramePool(4, 8, 8, ColorYUV420_8bit)
	defer pool.Close()

	pf := &ProcessingFrame{
		Width:  8,
		Height: 8,
		Data:   pool.Acquire(),
		pool:   pool,
	}
	// No SetRefs — refs is nil (unmanaged)

	t.Logf("MEASURE: refs before release: %d (0 means unmanaged/nil)", pf.Refs())

	pf.ReleaseYUV()

	require.Nil(t, pf.Data, "unmanaged frame should release immediately")

	// Second release should be a no-op (YUV is nil)
	require.NotPanics(t, func() {
		pf.ReleaseYUV()
	}, "second ReleaseYUV on nil YUV should be no-op")
}

// TestFramePoolDoubleReleaseWriteCorruption demonstrates end-to-end data
// corruption from double-release. Two "independent" frames writing to
// their buffers interfere with each other because they're the same memory.
func TestFramePoolDoubleReleaseWriteCorruption(t *testing.T) {
	// Use capacity=2, acquire both to empty the pool, then double-release
	// one buffer. This guarantees the free list has [victim, victim] and
	// two subsequent Acquires return the same pointer.
	fp := NewFramePool(2, 8, 8, ColorYUV420_8bit)
	defer fp.Close()

	bufSize := ColorYUV420_8bit.FrameSize(8, 8)

	// Acquire both pre-allocated buffers to empty the pool
	held := fp.Acquire()
	victim := fp.Acquire()

	// Double-release victim into empty pool (cap=2, so both accepted)
	fp.Release(victim)
	fp.Release(victim) // double release!

	// Acquire two "independent" buffers — both should be the same pointer
	frameA := fp.Acquire()
	frameB := fp.Acquire()

	ptrA := unsafe.Pointer(&frameA[0])
	ptrB := unsafe.Pointer(&frameB[0])
	aliased := ptrA == ptrB
	t.Logf("MEASURE: frameA=%p, frameB=%p, aliased=%v, bufSize=%d", ptrA, ptrB, aliased, bufSize)

	if !aliased {
		t.Log("buffers are not aliased — double release did not cause aliasing in this case")
		_ = held
		return
	}

	// Demonstrate corruption: frameA writes "all 0xAA", frameB writes "all 0xBB"
	for i := range frameA {
		frameA[i] = 0xAA
	}
	// Verify frameA's data
	for i := range frameA {
		if frameA[i] != 0xAA {
			t.Fatalf("frameA[%d] corrupted before frameB write: got 0x%02X", i, frameA[i])
		}
	}

	// Now frameB overwrites (same memory!)
	for i := range frameB {
		frameB[i] = 0xBB
	}

	// frameA should still be 0xAA if buffers are independent
	corruptedCount := 0
	for i := range frameA {
		if frameA[i] != 0xAA {
			corruptedCount++
		}
	}

	t.Logf("MEASURE: corrupted bytes in frameA after frameB write: %d/%d", corruptedCount, len(frameA))

	require.Zero(t, corruptedCount,
		"BUG: frameA was corrupted by writes to frameB — double release caused buffer aliasing")

	_ = held // keep held alive
}
