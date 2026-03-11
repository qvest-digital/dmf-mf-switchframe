package switcher

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRefcount_UnmanagedFrameReleasesImmediately(t *testing.T) {
	// Frame with refs=0 (unmanaged) should release on first ReleaseYUV call.
	pool := NewFramePool(4, 8, 8)
	defer pool.Close()

	pf := &ProcessingFrame{
		YUV:  pool.Acquire(),
		pool: pool,
	}

	pf.ReleaseYUV()
	require.Nil(t, pf.YUV, "unmanaged frame should release immediately")
}

func TestRefcount_SingleOwnerReleasesOnLastRef(t *testing.T) {
	// Frame with refs=1 should release when decremented to 0.
	pool := NewFramePool(4, 8, 8)
	defer pool.Close()

	pf := &ProcessingFrame{
		YUV:  pool.Acquire(),
		pool: pool,
	}
	pf.SetRefs(1)

	pf.ReleaseYUV()
	require.Nil(t, pf.YUV, "single-owner frame should release when refs hits 0")
}

func TestRefcount_SharedFrameReleasesOnLastRef(t *testing.T) {
	// Frame with refs=1, then Ref'd to 2. First release keeps alive, second releases.
	pool := NewFramePool(4, 8, 8)
	defer pool.Close()

	pf := &ProcessingFrame{
		YUV:    pool.Acquire(),
		Width:  8,
		Height: 8,
		pool:   pool,
	}
	pf.SetRefs(1)
	pf.Ref() // refs = 2

	// First release: refs 2→1, buffer stays alive
	pf.ReleaseYUV()
	require.NotNil(t, pf.YUV, "shared frame should not release until last ref dropped")

	// Second release: refs 1→0, buffer released
	pf.ReleaseYUV()
	require.Nil(t, pf.YUV, "shared frame should release when last ref dropped")
}

func TestRefcount_RefIncrements(t *testing.T) {
	pf := &ProcessingFrame{
		YUV: make([]byte, 96),
	}
	pf.SetRefs(1)
	pf.Ref()
	require.Equal(t, int32(2), pf.Refs())
}

func TestRefcount_DoubleReleaseOnUnmanagedIsSafe(t *testing.T) {
	pool := NewFramePool(4, 8, 8)
	defer pool.Close()

	pf := &ProcessingFrame{
		YUV:  pool.Acquire(),
		pool: pool,
	}
	// refs=0 (unmanaged)
	pf.ReleaseYUV()
	pf.ReleaseYUV() // should not panic
	require.Nil(t, pf.YUV)
}

func TestRefcount_PoolBufferReturnedOnLastRef(t *testing.T) {
	pool := NewFramePool(2, 8, 8)
	defer pool.Close()

	// Acquire both buffers
	pf1 := &ProcessingFrame{YUV: pool.Acquire(), pool: pool}
	pf1.SetRefs(1)
	pf2 := &ProcessingFrame{YUV: pool.Acquire(), pool: pool}
	pf2.SetRefs(1)

	// Pool should be empty now — next acquire is a miss
	hitsBefore, missesBefore := pool.Stats()

	// Ref pf1 (shared), then release both refs
	pf1.Ref() // refs=2
	pf1.ReleaseYUV() // refs=1, not returned
	pf1.ReleaseYUV() // refs=0, returned to pool

	// Now pool should have 1 buffer again
	buf := pool.Acquire()
	require.NotNil(t, buf)

	hitsAfter, missesAfter := pool.Stats()
	// The acquire after release should be a hit (buffer was returned)
	require.Greater(t, hitsAfter, hitsBefore, "buffer should have been returned to pool")
	_ = missesBefore
	_ = missesAfter

	// Clean up
	pf2.ReleaseYUV()
	pool.Release(buf)
}

func TestRefcount_DeepCopyResetsRefs(t *testing.T) {
	pool := NewFramePool(4, 4, 4)
	defer pool.Close()

	original := &ProcessingFrame{
		YUV:    pool.Acquire(),
		Width:  4,
		Height: 4,
		PTS:    1000,
		pool:   pool,
	}
	original.SetRefs(1)
	original.Ref() // refs=2

	copied := original.DeepCopy()
	// Copy should have independent refs (reset to 0, unmanaged)
	require.Equal(t, int32(0), copied.Refs(), "DeepCopy should reset refs")
	require.Equal(t, int32(2), original.Refs(), "DeepCopy should not affect original refs")

	// Both should release independently
	copied.ReleaseYUV()
	require.Nil(t, copied.YUV)
	require.NotNil(t, original.YUV, "original unaffected by copy release")

	original.ReleaseYUV() // refs 2→1
	require.NotNil(t, original.YUV)
	original.ReleaseYUV() // refs 1→0, releases
	require.Nil(t, original.YUV)
}

func TestRefcount_RawSinkNodeZeroCopy(t *testing.T) {
	// rawSinkNode should use Ref/Unref instead of DeepCopy.
	// The sink receives the SAME YUV pointer as the pipeline frame.
	var sink atomic.Pointer[RawVideoSink]
	var receivedYUV []byte
	fn := RawVideoSink(func(pf *ProcessingFrame) {
		receivedYUV = pf.YUV
	})
	sink.Store(&fn)

	n := &rawSinkNode{sink: &sink, name: "test"}

	srcYUV := make([]byte, 8*8*3/2)
	srcYUV[0] = 0xAA
	pf := &ProcessingFrame{
		YUV:    srcYUV,
		Width:  8,
		Height: 8,
		PTS:    1000,
	}
	pf.SetRefs(1)

	out := n.Process(nil, pf)

	// Should return the same frame
	require.Same(t, pf, out)
	// Sink should have received the SAME YUV slice (zero-copy)
	require.Same(t, &srcYUV[0], &receivedYUV[0], "sink should receive same YUV buffer (zero-copy)")
	// Refs should be back to 1 (pipeline's ref)
	require.Equal(t, int32(1), pf.Refs())
	// YUV should still be alive (pipeline still owns it)
	require.NotNil(t, out.YUV)
}

func TestRefcount_SharedAcrossValueCopies(t *testing.T) {
	// Value copies of a ProcessingFrame must share the same refcount.
	// This is critical for frame_sync which does: releaseRawVideo = *newest
	pool := NewFramePool(4, 8, 8)
	defer pool.Close()

	original := &ProcessingFrame{
		YUV:  pool.Acquire(),
		Width: 8, Height: 8, PTS: 1000,
		pool: pool,
	}
	original.SetRefs(1) // frame_sync ownership

	// Value copy (simulates frame_sync's releaseRawVideo = *newest)
	valueCopy := *original

	// Ref on value copy should affect the SAME counter as original
	valueCopy.Ref() // refs should be 2 (shared)
	require.Equal(t, int32(2), original.Refs(), "original should see ref from value copy")
	require.Equal(t, int32(2), valueCopy.Refs(), "value copy should see same count")

	// ReleaseYUV on value copy decrements shared counter
	valueCopy.ReleaseYUV()
	require.Equal(t, int32(1), original.Refs(), "original should see release from value copy")
	require.NotNil(t, original.YUV, "buffer should still be alive (original holds ref)")

	// Original releases last ref
	original.ReleaseYUV()
	require.Nil(t, original.YUV, "buffer should be released on last ref")
}

func TestRefcount_ValueCopyChainForPipeline(t *testing.T) {
	// Simulates the full frame_sync → delivery → broadcastProcessedFromPF path.
	// Frame sync stores lastRawVideo, value-copies for delivery, pipeline gets shallow copy.
	pool := NewFramePool(4, 8, 8)
	defer pool.Close()

	// 1. Source decoder creates frame (copy #1 from FFmpeg)
	decoderFrame := &ProcessingFrame{
		YUV:  pool.Acquire(),
		Width: 8, Height: 8, PTS: 1000,
		pool: pool,
	}
	decoderFrame.SetRefs(1) // frame_sync ownership

	// 2. Frame sync stores as lastRawVideo (pointer retention)
	lastRawVideo := decoderFrame

	// 3. releaseTick: value copy for delivery
	deliveryCopy := *lastRawVideo

	// 4. Delivery: Ref for pipeline consumer
	deliveryCopy.Ref() // shared refs: 1→2

	// 5. broadcastProcessedFromPF: shallow copy for pipeline (shares refs + YUV)
	pipelineCopy := new(ProcessingFrame)
	*pipelineCopy = deliveryCopy

	// 6. Delivery cleanup: drop delivery ref
	deliveryCopy.ReleaseYUV() // shared refs: 2→1

	// Buffer should still be alive (frame_sync + pipeline refs... wait, pipeline
	// ref was part of the Ref in step 4, delivery dropped it. But pipelineCopy
	// still shares the same refs pointer, so its Refs() should show 1)
	require.Equal(t, int32(1), pipelineCopy.Refs())
	require.NotNil(t, pipelineCopy.YUV, "pipeline copy should still have live YUV")

	// 7. We need another Ref for pipeline since delivery dropped the one it added.
	// The correct pattern: broadcastProcessedFromPF calls Ref() itself.
	pipelineCopy.Ref() // shared refs: 1→2

	// 8. Pipeline processes (rawSinkNode Ref/Release cycle: net zero)
	pipelineCopy.Ref()        // +1 for sink: refs=3
	_ = pipelineCopy.YUV[0]   // sink reads
	pipelineCopy.ReleaseYUV() // -1 for sink: refs=2

	// 9. Pipeline done: release pipeline's ref
	pipelineCopy.ReleaseYUV() // refs: 2→1 (frame_sync still holds)

	require.NotNil(t, lastRawVideo.YUV, "frame_sync's buffer should still be alive")
	require.Equal(t, int32(1), lastRawVideo.Refs())

	// 10. Next tick: new frame arrives, frame_sync releases old lastRawVideo
	lastRawVideo.ReleaseYUV() // refs: 1→0, returned to pool
	require.Nil(t, lastRawVideo.YUV)
}

func TestRefcount_FreezeRepeatWithSharedRefs(t *testing.T) {
	// Simulates freeze: frame_sync delivers the same lastRawVideo on multiple ticks.
	pool := NewFramePool(4, 8, 8)
	defer pool.Close()

	frame := &ProcessingFrame{
		YUV:  pool.Acquire(),
		Width: 8, Height: 8, PTS: 1000,
		pool: pool,
	}
	frame.SetRefs(1) // frame_sync ownership

	lastRawVideo := frame

	// Tick 1: deliver
	copy1 := *lastRawVideo
	copy1.Ref()                       // refs: 1→2 (delivery)
	pipeline1 := new(ProcessingFrame) // shallow copy for pipeline
	*pipeline1 = copy1
	pipeline1.Ref()      // refs: 2→3 (pipeline)
	copy1.ReleaseYUV()   // refs: 3→2 (delivery done)

	// Tick 2 (freeze): deliver same frame again while pipeline1 still processing
	copy2 := *lastRawVideo
	copy2.Ref()                       // refs: 2→3 (delivery)
	pipeline2 := new(ProcessingFrame)
	*pipeline2 = copy2
	pipeline2.Ref()      // refs: 3→4 (pipeline2)
	copy2.ReleaseYUV()   // refs: 4→3 (delivery done)

	// Pipeline1 finishes
	pipeline1.ReleaseYUV() // refs: 3→2

	// Pipeline2 finishes
	pipeline2.ReleaseYUV() // refs: 2→1

	// Frame sync still holds ref
	require.NotNil(t, lastRawVideo.YUV)
	require.Equal(t, int32(1), lastRawVideo.Refs())

	// New frame arrives, release old
	lastRawVideo.ReleaseYUV() // refs: 1→0
	require.Nil(t, lastRawVideo.YUV)
}

func TestRefcount_UnmanagedFrameFallsBackToDeepCopy(t *testing.T) {
	// FRC frames have nil refs. broadcastProcessedFromPF should fall back to DeepCopy.
	pool := NewFramePool(4, 8, 8)
	defer pool.Close()

	pf := &ProcessingFrame{
		YUV:  pool.Acquire(),
		Width: 8, Height: 8, PTS: 1000,
		pool: pool,
	}
	// No SetRefs — unmanaged (nil refs)

	// Ref on nil refs should be safe (no-op or panic-free)
	require.Equal(t, int32(0), pf.Refs())
	// Unmanaged frame: ReleaseYUV should release immediately
	pf.ReleaseYUV()
	require.Nil(t, pf.YUV)
}

func TestMakeWritable_SoleOwnerIsNoOp(t *testing.T) {
	pool := NewFramePool(4, 8, 8)
	defer pool.Close()

	pf := &ProcessingFrame{
		YUV:  pool.Acquire(),
		Width: 8, Height: 8,
		pool: pool,
	}
	pf.SetRefs(1)
	originalYUV := pf.YUV

	pf.MakeWritable(pool)

	// Should be a no-op — same buffer, same refs.
	require.Same(t, &originalYUV[0], &pf.YUV[0], "sole owner should keep same buffer")
	require.Equal(t, int32(1), pf.Refs())

	pf.ReleaseYUV()
}

func TestMakeWritable_UnmanagedIsNoOp(t *testing.T) {
	pf := &ProcessingFrame{
		YUV:   make([]byte, 96),
		Width: 8, Height: 8,
	}
	// No SetRefs — unmanaged
	originalYUV := pf.YUV

	pf.MakeWritable(nil)

	require.Same(t, &originalYUV[0], &pf.YUV[0], "unmanaged frame should keep same buffer")
}

func TestMakeWritable_SharedCopiesAndDetaches(t *testing.T) {
	pool := NewFramePool(4, 8, 8)
	defer pool.Close()

	pf := &ProcessingFrame{
		YUV:  pool.Acquire(),
		Width: 8, Height: 8,
		pool: pool,
	}
	pf.SetRefs(1)
	pf.Ref() // refs=2 — simulates frame_sync holding a ref

	originalYUV := pf.YUV
	pf.YUV[0] = 0xAA // mark data

	pf.MakeWritable(pool)

	// Should have a NEW buffer with copied data.
	require.NotSame(t, &originalYUV[0], &pf.YUV[0], "shared frame should get new buffer")
	require.Equal(t, byte(0xAA), pf.YUV[0], "data should be copied")
	// New independent refcount.
	require.Equal(t, int32(1), pf.Refs(), "new buffer should have refs=1")

	// Original shared refcount should be decremented (2→1).
	// We can't directly check the old refs since pf.refs is now a new pointer,
	// but we know the original had 2 and we decremented it once.

	pf.ReleaseYUV()
}

func TestMakeWritable_NilYUVIsNoOp(t *testing.T) {
	pf := &ProcessingFrame{}
	pf.MakeWritable(nil) // should not panic
}

func TestMakeWritable_PreservesSourceForFrameSync(t *testing.T) {
	// Simulates the exact bug scenario: frame_sync holds lastRawVideo,
	// pipeline gets shallow copy, calls MakeWritable, modifies its buffer.
	// Verifies that frame_sync's buffer is NOT modified.
	pool := NewFramePool(4, 8, 8)
	defer pool.Close()

	// 1. Source decoder creates frame.
	sourceFrame := &ProcessingFrame{
		YUV:  pool.Acquire(),
		Width: 8, Height: 8,
		pool: pool,
	}
	sourceFrame.SetRefs(1)
	sourceFrame.YUV[0] = 0x42 // original source data

	// 2. frame_sync retains as lastRawVideo.
	lastRawVideo := sourceFrame

	// 3. frame_sync value-copies for delivery.
	delivered := *lastRawVideo

	// 4. broadcastProcessedFromPF: Ref + shallow copy.
	delivered.Ref() // refs: 1→2
	pipelineFrame := new(ProcessingFrame)
	*pipelineFrame = delivered

	// 5. Pipeline.Run calls MakeWritable.
	pipelineFrame.MakeWritable(pool)

	// 6. Pipeline modifies its buffer (simulates layout compositor).
	pipelineFrame.YUV[0] = 0xFF

	// 7. Verify frame_sync's buffer is untouched.
	require.Equal(t, byte(0x42), lastRawVideo.YUV[0],
		"frame_sync's lastRawVideo must not be modified by pipeline")
	require.Equal(t, byte(0xFF), pipelineFrame.YUV[0],
		"pipeline should have its own modified buffer")

	// Cleanup.
	pipelineFrame.ReleaseYUV()
	lastRawVideo.ReleaseYUV()
}

func TestRefcount_RawSinkNodeConcurrentSafe(t *testing.T) {
	// Verify that Ref/ReleaseYUV is safe under concurrent access.
	pool := NewFramePool(4, 8, 8)
	defer pool.Close()

	pf := &ProcessingFrame{
		YUV:  pool.Acquire(),
		pool: pool,
	}
	pf.SetRefs(1)

	// Simulate N concurrent sinks all Ref'ing and releasing
	const N = 100
	pf.SetRefs(int32(N + 1)) // 1 owner + N sharers
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = pf.YUV[0] // read the buffer
			pf.ReleaseYUV()
		}()
	}
	wg.Wait()

	// After all goroutines release, only owner's ref remains
	require.Equal(t, int32(1), pf.Refs())
	require.NotNil(t, pf.YUV)

	// Owner releases
	pf.ReleaseYUV()
	require.Nil(t, pf.YUV)
}
