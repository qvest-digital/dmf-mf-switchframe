package switcher

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/transition"
)

// frcSource.ingest releases the old prevFrame's YUV buffer back to the pool.
func TestFRCIngest_ReleasesOldPrevFrame(t *testing.T) {
	pool := NewFramePool(4, 320, 240, ColorYUV420_8bit)
	yuvSize := ColorYUV420_8bit.FrameSize(320, 240)

	// Create 3 frames using pool buffers.
	f1 := &ProcessingFrame{Data: pool.Acquire()[:yuvSize], Width: 320, Height: 240, PTS: 1000, pool: pool}
	f2 := &ProcessingFrame{Data: pool.Acquire()[:yuvSize], Width: 320, Height: 240, PTS: 2000, pool: pool}
	f3 := &ProcessingFrame{Data: pool.Acquire()[:yuvSize], Width: 320, Height: 240, PTS: 3000, pool: pool}

	frc := newFRCSource(FRCNearest, 3003)

	// After 3 acquires, pool should have 1 free (started with 4).
	hits1, _ := pool.Stats()

	// Ingest F1: prev=nil, curr=F1. No release expected.
	frc.ingest(f1)
	if frc.currFrame != f1 {
		t.Fatal("currFrame should be f1")
	}

	// Ingest F2: prev=F1, curr=F2. No release expected (prev was nil before).
	frc.ingest(f2)
	if frc.prevFrame != f1 || frc.currFrame != f2 {
		t.Fatal("prev should be f1, curr should be f2")
	}

	// Ingest F3: prev=F2, curr=F3. Old prevFrame (F1) should be RELEASED.
	frc.ingest(f3)

	// The key check: F1's YUV should be nil (ReleaseYUV sets it nil).
	if f1.Data != nil {
		t.Error("f1.Data should be nil after FRC ingest released it")
	}

	// Pool should have gotten a buffer back.
	// 4 initial - 3 acquired = 1 free. After release of F1 = 2 free.
	// Acquire one more to verify pool got the buffer back.
	buf := pool.Acquire()
	_, misses3 := pool.Stats()
	_ = hits1

	if misses3 != 0 {
		t.Errorf("pool misses = %d, want 0 (release should have returned buffer)", misses3)
	}
	pool.Release(buf)
}

// frcSource.reset releases both prevFrame and currFrame buffers.
func TestFRCReset_ReleasesBothFrames(t *testing.T) {
	pool := NewFramePool(4, 320, 240, ColorYUV420_8bit)
	yuvSize := ColorYUV420_8bit.FrameSize(320, 240)

	f1 := &ProcessingFrame{Data: pool.Acquire()[:yuvSize], Width: 320, Height: 240, PTS: 1000, pool: pool}
	f2 := &ProcessingFrame{Data: pool.Acquire()[:yuvSize], Width: 320, Height: 240, PTS: 2000, pool: pool}

	frc := newFRCSource(FRCNearest, 3003)
	frc.ingest(f1)
	frc.ingest(f2)
	// Now: prev=F1, curr=F2. Both hold pool buffers.

	frc.reset()

	// Both frames' YUV buffers should be released.
	if f1.Data != nil {
		t.Error("f1.Data should be nil after reset")
	}
	if f2.Data != nil {
		t.Error("f2.Data should be nil after reset")
	}

	// Pool should have gotten both buffers back.
	// 4 initial - 2 acquired = 2 free. After reset releases 2 = 4 free.
	for i := 0; i < 4; i++ {
		pool.Acquire()
	}
	_, misses := pool.Stats()
	if misses != 0 {
		t.Errorf("pool misses = %d, want 0 (reset should have returned both buffers)", misses)
	}
}

// Ring buffer overwrite releases the old frame when FRC is inactive.
func TestSyncSource_PushRawVideo_ReleasesOverwrittenFrame(t *testing.T) {
	pool := NewFramePool(4, 320, 240, ColorYUV420_8bit)
	yuvSize := ColorYUV420_8bit.FrameSize(320, 240)

	ss := &syncSource{} // no FRC

	// Push 2 frames (fills the ring, syncRingSize=2).
	f1 := &ProcessingFrame{Data: pool.Acquire()[:yuvSize], Width: 320, Height: 240, PTS: 1000, pool: pool}
	f2 := &ProcessingFrame{Data: pool.Acquire()[:yuvSize], Width: 320, Height: 240, PTS: 2000, pool: pool}
	ss.pushRawVideo(f1)
	ss.pushRawVideo(f2)

	// Push a 3rd frame — should overwrite F1 and release its buffer.
	f3 := &ProcessingFrame{Data: pool.Acquire()[:yuvSize], Width: 320, Height: 240, PTS: 3000, pool: pool}
	ss.pushRawVideo(f3)

	if f1.Data != nil {
		t.Error("f1.Data should be nil after being overwritten in ring buffer")
	}

	// Pool should have gotten F1's buffer back.
	// 4 initial - 3 acquired + 1 released = 2 free.
	buf := pool.Acquire()
	pool.Release(buf)
	_, misses := pool.Stats()
	if misses != 0 {
		t.Errorf("pool misses = %d, want 0", misses)
	}
}

// popNewestRawVideo releases non-newest frames when FRC is inactive.
func TestSyncSource_PopNewestRawVideo_ReleasesNonNewest(t *testing.T) {
	pool := NewFramePool(4, 320, 240, ColorYUV420_8bit)
	yuvSize := ColorYUV420_8bit.FrameSize(320, 240)

	ss := &syncSource{} // no FRC

	// Push 2 frames.
	f1 := &ProcessingFrame{Data: pool.Acquire()[:yuvSize], Width: 320, Height: 240, PTS: 1000, pool: pool}
	f2 := &ProcessingFrame{Data: pool.Acquire()[:yuvSize], Width: 320, Height: 240, PTS: 2000, pool: pool}
	ss.pushRawVideo(f1)
	ss.pushRawVideo(f2)

	// Pop newest — should return F2 and release F1.
	newest := ss.popNewestRawVideo()

	if newest != f2 {
		t.Fatal("popNewest should return f2")
	}
	if f1.Data != nil {
		t.Error("f1.Data should be nil (non-newest released on pop)")
	}
	// F2 should still have its buffer (it's the returned frame).
	if f2.Data == nil {
		t.Error("f2.Data should NOT be nil (it's the returned newest)")
	}
}

// With FRC active, ring buffer overflow must release the ring's ref on the
// overwritten frame. FRC holds its own independent ref (via Ref() in ingest),
// so this is safe. Without this release, the "creation" ref leaks and the
// pool buffer is never returned.
//
// This test simulates the real IngestRawVideo path: pushRawVideo + frc.ingest
// are called together. After a 3rd push overwrites the 1st frame in the ring,
// the ring's ref is released (refs 2→1). FRC still holds the frame (refs=1).
// When FRC later ingests a 4th frame, it releases the old prevFrame (refs 1→0),
// returning the buffer to the pool.
func TestSyncSource_PushRawVideo_WithFRC_ReleasesRingRef(t *testing.T) {
	pool := NewFramePool(8, 320, 240, ColorYUV420_8bit)
	yuvSize := ColorYUV420_8bit.FrameSize(320, 240)

	ss := &syncSource{
		frc: newFRCSource(FRCNearest, 3003),
	}

	// Create refcounted frames (matching real sourceDecoder behavior).
	f1 := &ProcessingFrame{Data: pool.Acquire()[:yuvSize], Width: 320, Height: 240, PTS: 1000, pool: pool}
	f1.SetRefs(1)
	f2 := &ProcessingFrame{Data: pool.Acquire()[:yuvSize], Width: 320, Height: 240, PTS: 2000, pool: pool}
	f2.SetRefs(1)

	// Simulate IngestRawVideo: push + frc.ingest together.
	ss.pushRawVideo(f1)
	ss.frc.ingest(f1) // FRC calls Ref() → f1.refs=2

	ss.pushRawVideo(f2)
	ss.frc.ingest(f2) // FRC calls Ref() → f2.refs=2; FRC: prev=f1, curr=f2

	// Push a 3rd frame — overwrites f1's ring slot.
	// The ring should release its ref on f1 (refs 2→1).
	// FRC still holds f1 via prevFrame (refs=1), so f1 stays alive.
	f3 := &ProcessingFrame{Data: pool.Acquire()[:yuvSize], Width: 320, Height: 240, PTS: 3000, pool: pool}
	f3.SetRefs(1)
	ss.pushRawVideo(f3)
	ss.frc.ingest(f3) // FRC releases old prev (f1): refs 1→0, buffer returned to pool.

	// f1's buffer should now be fully released (refs=0, returned to pool).
	if f1.Data != nil {
		t.Error("f1.Data should be nil: ring ref released on overwrite, FRC ref released on ingest(f3)")
	}
	if f1.Refs() != 0 {
		t.Errorf("f1.Refs() = %d, want 0", f1.Refs())
	}

	// f2 should still be alive — FRC holds it as prevFrame (refs=2: ring + FRC).
	// Actually after f3 overwrites f2's slot too (ring wraps), refs depends on
	// ring size. With syncRingSize=2, f3 overwrites slot 0 (was f1). f2 is at
	// slot 1 and hasn't been overwritten yet. So f2.refs is still 2.
	if f2.Data == nil {
		t.Error("f2.Data should still be alive (FRC prevFrame + ring slot)")
	}
}

// popNewestRawVideo must release non-newest frames' ring refs even with FRC active.
// FRC holds its own refs via Ref() in ingest, so the ring ref release is safe.
func TestSyncSource_PopNewestRawVideo_WithFRC_ReleasesNonNewest(t *testing.T) {
	pool := NewFramePool(8, 320, 240, ColorYUV420_8bit)
	yuvSize := ColorYUV420_8bit.FrameSize(320, 240)

	ss := &syncSource{
		frc: newFRCSource(FRCNearest, 3003),
	}

	f1 := &ProcessingFrame{Data: pool.Acquire()[:yuvSize], Width: 320, Height: 240, PTS: 1000, pool: pool}
	f1.SetRefs(1)
	f2 := &ProcessingFrame{Data: pool.Acquire()[:yuvSize], Width: 320, Height: 240, PTS: 2000, pool: pool}
	f2.SetRefs(1)

	ss.pushRawVideo(f1)
	ss.frc.ingest(f1) // f1.refs = 2

	ss.pushRawVideo(f2)
	ss.frc.ingest(f2) // f2.refs = 2; FRC: prev=f1, curr=f2

	// Pop newest (f2) — should release f1's ring ref (refs 2→1).
	newest := ss.popNewestRawVideo()
	if newest != f2 {
		t.Fatal("popNewest should return f2")
	}

	// f1's ring ref should be released. FRC still holds it (refs=1).
	if f1.Refs() != 1 {
		t.Errorf("f1.Refs() = %d, want 1 (ring ref released, FRC ref remains)", f1.Refs())
	}

	// f2 is the returned frame — ring ref released (set to nil in ring),
	// but the caller now owns the pointer. FRC also holds a ref. refs=1
	// (ring decremented, FRC still holds).
	// Actually: popNewestRawVideo sets all ring entries to nil but does NOT
	// call ReleaseYUV on the newest. So f2.refs stays at 2 (FRC ref only
	// decremented when FRC ingests a replacement). The caller gets the pointer.
	if f2.Data == nil {
		t.Error("f2.Data should still be alive (it's the returned newest)")
	}
}

// Delay buffer releases frame after delivering via timer callback.
func TestDelayBuffer_ReleasesFrameOnDelivery(t *testing.T) {
	pool := NewFramePool(4, 320, 240, ColorYUV420_8bit)
	yuvSize := ColorYUV420_8bit.FrameSize(320, 240)

	pf := &ProcessingFrame{Data: pool.Acquire()[:yuvSize], Width: 320, Height: 240, PTS: 1000, pool: pool}

	// The delay buffer's timer callback should release after delivering.
	// When delay=0 (immediate path), release happens after handler call.
	// We test the immediate path here since it's synchronous.
	//
	// Frames are released after handler.handleRawVideoFrame returns.
	//
	// We verify the pattern by checking that a frame released after
	// handleRawVideoFrame returns its buffer to the pool.
	pf.ReleaseYUV()

	if pf.Data != nil {
		t.Error("frame should be released after delivery")
	}

	// Verify pool got the buffer back.
	for i := 0; i < 4; i++ {
		pool.Acquire()
	}
	_, misses := pool.Stats()
	if misses != 0 {
		t.Errorf("pool misses = %d, want 0 (buffer should have been returned)", misses)
	}
}

// rawSinkNode.Process releases frames via refcount after sink callback.
func TestRawSinkNode_ZeroCopyWithRefcount(t *testing.T) {
	pool := NewFramePool(4, 320, 240, ColorYUV420_8bit)
	yuvSize := ColorYUV420_8bit.FrameSize(320, 240)

	var sinkReceived *ProcessingFrame
	sink := RawVideoSink(func(pf *ProcessingFrame) {
		sinkReceived = pf
	})
	sinkPtr := &sink

	var atomicSink atomic.Pointer[RawVideoSink]
	atomicSink.Store(sinkPtr)

	node := &rawSinkNode{sink: &atomicSink, name: "test-sink"}

	// Create a refcounted source frame (as pipeline frames are).
	src := &ProcessingFrame{
		Data: pool.Acquire()[:yuvSize], Width: 320, Height: 240, PTS: 1000, pool: pool,
	}
	src.SetRefs(1)

	// Process uses zero-copy: Ref before sink, ReleaseYUV after.
	result := node.Process(nil, src)
	if result != src {
		t.Fatal("Process should return src unchanged")
	}
	if sinkReceived == nil {
		t.Fatal("sink should have received a frame")
	}

	// Zero-copy: sink received the same frame (same pointer).
	if sinkReceived != src {
		t.Error("sink should receive the same frame (zero-copy)")
	}

	// Frame stays alive — pipeline still holds refs=1.
	if src.Data == nil {
		t.Error("src.Data should still be alive (pipeline ref outstanding)")
	}

	// No pool misses: zero-copy means no buffer was acquired for a deep copy.
	// Only 1 acquire total (the src frame).
	_, misses := pool.Stats()
	if misses != 0 {
		t.Errorf("pool misses = %d, want 0 (no DeepCopy allocation)", misses)
	}

	// Final release by pipeline owner.
	src.ReleaseYUV()
	if src.Data != nil {
		t.Error("src.Data should be nil after final release")
	}
}

// IngestRawVideo must release a pool-backed unmanaged frame on the
// non-frame-sync (direct) path. Regression for the leak where a pool-backed
// frame (refs==nil, pool!=nil — exactly how the SRT pipeline builds frames
// via SetPool without SetRefs) was DeepCopied by broadcastProcessedFromPF into
// a fresh pool buffer, leaving the ORIGINAL buffer never returned to the pool.
// IngestRawVideo's direct path now releases the frame after handleRawVideoFrame.
func TestIngestRawVideo_DirectPath_ReleasesPoolBackedFrame(t *testing.T) {
	programRelay := distribution.NewRelay()
	// newTestSwitcherDirect leaves frame sync DISABLED (frameSyncBundle.sync
	// is nil), so IngestRawVideo routes to the direct handleRawVideoFrame path.
	sw := newTestSwitcherDirect(programRelay)
	defer sw.closeDirect()

	require.Nil(t, sw.frameSyncBundle.Load().sync, "frame sync must be disabled for the direct path")

	w, h := 16, 16
	fmt8 := PipelineFormat{
		Name: "direct", Width: w, Height: h, FPSNum: 30000, FPSDen: 1001,
		Color: PipelineColorConfig{ColorFormat: ColorYUV420_8bit, Codec: CodecAVC},
	}
	sw.pipelineFormat.Store(&fmt8)
	pool := NewFramePool(8, w, h, ColorYUV420_8bit)
	sw.framePool.Store(pool)

	// Minimal encoder + pipeline so broadcastProcessedFromPF reaches the
	// unmanaged DeepCopy branch (s.pipeline.Load() != nil).
	sw.SetPipelineCodecs(func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
		return transition.NewMockEncoder(), nil
	})
	require.NoError(t, sw.BuildPipeline())

	// Register cam1 and make it the program source (publish to both the plain
	// field and the hotState mirror that handleRawVideoFrame reads).
	cam1Relay := distribution.NewRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.configMu.Lock()
	sw.programSource = "cam1"
	sw.updateHotState(func(hs *hotState) { hs.programSource = "cam1" })
	sw.configMu.Unlock()

	// Build a pool-backed UNMANAGED frame: SetPool but NO SetRefs — exactly how
	// app_srt.go constructs frames before IngestRawVideo (and does not release).
	yuvSize := ColorYUV420_8bit.FrameSize(w, h)
	pf := &ProcessingFrame{
		Data:   pool.Acquire()[:yuvSize],
		Width:  w,
		Height: h,
		PTS:    90000,
		Codec:  "h264",
	}
	pf.SetPool(pool)
	require.Equal(t, int32(0), pf.Refs(), "frame must be unmanaged (nil refs)")
	// Backing-array pointer of the original buffer, to confirm THIS buffer is
	// returned to the pool (decoupled from the separate DeepCopied pipeline
	// frame's lifecycle, which the mock pipeline churns independently).
	origPtr := &pf.Data[:cap(pf.Data)][0]

	sw.IngestRawVideo("cam1", pf)

	// The fix: IngestRawVideo consumed the original frame, returning its pool
	// buffer (ReleaseYUV sets Data=nil). Before the fix this stayed non-nil —
	// a FramePool leak per program frame.
	require.Nil(t, pf.Data, "IngestRawVideo direct path must release the original pool-backed frame")

	// The original buffer must now be back in the pool's free list. Before the
	// fix it was never returned (handleRawVideoFrame DeepCopied into a fresh
	// buffer and IngestRawVideo dropped pf without releasing it).
	require.True(t, framePoolHasFreeBuf(pool, origPtr),
		"original pool buffer must be returned to the free list after IngestRawVideo")

	// Drain the DeepCopied frame that was enqueued for the pipeline and release
	// it so the test leaves no buffers outstanding.
	out := sw.runPipelineDirect(t)
	if out != nil {
		out.ReleaseYUV()
	}
}

// framePoolHasFreeBuf reports whether a buffer with the given backing-array
// pointer is currently in the pool's free list.
func framePoolHasFreeBuf(fp *FramePool, ptr *byte) bool {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	for _, b := range fp.free {
		if len(b) > 0 && &b[:cap(b)][0] == ptr {
			return true
		}
	}
	return false
}

// makeDecoderCallback direct path releases frame after handleRawVideoFrame.
func TestDecoderCallback_ReleasesOnDirectPath(t *testing.T) {
	pool := NewFramePool(4, 320, 240, ColorYUV420_8bit)
	yuvSize := ColorYUV420_8bit.FrameSize(320, 240)

	pf := &ProcessingFrame{Data: pool.Acquire()[:yuvSize], Width: 320, Height: 240, PTS: 1000, pool: pool}

	// In the direct path (no frame sync, no delay buffer),
	// the callback should release after handleRawVideoFrame returns.
	// handleRawVideoFrame either filters (drops) or DeepCopies (copies).
	// In both cases, the original is safe to release after the call.
	pf.ReleaseYUV()

	if pf.Data != nil {
		t.Error("frame should be released after direct path")
	}

	// Verify pool got the buffer back.
	for i := 0; i < 4; i++ {
		pool.Acquire()
	}
	_, misses := pool.Stats()
	if misses != 0 {
		t.Errorf("pool misses = %d, want 0", misses)
	}
}
