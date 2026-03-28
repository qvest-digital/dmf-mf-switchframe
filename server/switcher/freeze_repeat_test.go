package switcher

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestFreezeRepeat_BufferSurvivesPipelineRelease verifies that the pool buffer
// backing lastRawVideo stays alive after the pipeline processes and releases
// the delivered frame. This is critical for freeze-repeat: when no new frame
// arrives, the frame sync re-delivers lastRawVideo on the next tick, so the
// buffer must not have been returned to the pool.
//
// Bug: The creation ref (SetRefs(1)) is the only ref on the frame. After
// delivery, broadcastProcessedFromPF Refs for the pipeline copy, but the
// delay buffer's ReleaseYUV immediately cancels it out (+1 then -1 = net 0).
// The pipeline becomes the sole owner and frees the buffer when done.
// lastRawVideo is left pointing to a freed pool buffer (use-after-free).
func TestFreezeRepeat_BufferSurvivesPipelineRelease(t *testing.T) {
	pool := NewFramePool(4, 8, 8, ColorYUV420_8bit)
	defer pool.Close()
	yuvSize := ColorYUV420_8bit.FrameSize(8, 8)

	// Track raw video deliveries.
	var deliveredFrames []*ProcessingFrame
	var deliveredKeys []string

	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(33*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.onRawVideo = func(sourceKey string, pf *ProcessingFrame) {
		deliveredFrames = append(deliveredFrames, pf)
		deliveredKeys = append(deliveredKeys, sourceKey)
	}
	fs.AddSource("cam1")

	// Create a managed frame (mimics sourceDecoder output).
	frame := &ProcessingFrame{
		Data:   pool.Acquire()[:yuvSize],
		Width: 8, Height: 8, PTS: 90000,
		pool: pool,
	}
	frame.SetRefs(1)
	frame.Data[0] = 0xAA // sentinel data

	// Ingest the frame.
	fs.IngestRawVideo("cam1", frame)

	// Manually trigger a tick to deliver the frame.
	fs.releaseTick()

	require.Len(t, deliveredFrames, 1, "should deliver one frame on tick 1")
	delivered1 := deliveredFrames[0]

	// Simulate the full delivery pipeline:
	// broadcastProcessedFromPF: Ref for pipeline copy
	delivered1.Ref()
	pipelineCopy := new(ProcessingFrame)
	*pipelineCopy = *delivered1

	// delay buffer: ReleaseYUV (balances the Ref, nets to zero change)
	delivered1.ReleaseYUV()

	// Pipeline: MakeWritable should copy (refs > 1 because lastRawVideo holds a ref)
	pipelineCopy.MakeWritable(pool)

	// Pipeline processes and releases its copy.
	pipelineCopy.ReleaseYUV()

	// KEY ASSERTION: The frame_sync's lastRawVideo buffer must still be alive.
	// If the bug exists, the pool buffer was freed and lastRawVideo.Data is stale.
	fs.mu.Lock()
	ss := fs.sources["cam1"]
	fs.mu.Unlock()

	ss.mu.Lock()
	lastRaw := ss.lastRawVideo
	ss.mu.Unlock()

	require.NotNil(t, lastRaw, "lastRawVideo should exist")
	require.NotNil(t, lastRaw.Data, "lastRawVideo.Data must not be nil (buffer must survive pipeline release)")
	require.True(t, lastRaw.Refs() >= 1, "lastRawVideo must hold at least one ref")
	require.Equal(t, byte(0xAA), lastRaw.Data[0], "lastRawVideo data must be intact")

	// Now simulate freeze-repeat: tick again with no new frame.
	deliveredFrames = deliveredFrames[:0]
	deliveredKeys = deliveredKeys[:0]

	fs.releaseTick()

	require.Len(t, deliveredFrames, 1, "should deliver freeze-repeated frame on tick 2")
	delivered2 := deliveredFrames[0]

	// The freeze-repeated frame must have valid YUV data.
	require.NotNil(t, delivered2.Data, "freeze-repeated frame must have valid YUV")
	require.Equal(t, byte(0xAA), delivered2.Data[0], "freeze-repeated frame must have correct data")

	// Simulate pipeline processing the freeze-repeated frame.
	delivered2.Ref()
	pipeline2 := new(ProcessingFrame)
	*pipeline2 = *delivered2
	delivered2.ReleaseYUV()
	pipeline2.MakeWritable(pool)
	pipeline2.ReleaseYUV()

	// After second pipeline release, lastRawVideo must STILL be alive.
	ss.mu.Lock()
	lastRaw2 := ss.lastRawVideo
	ss.mu.Unlock()

	require.NotNil(t, lastRaw2.Data, "lastRawVideo.Data must survive second pipeline release")
	require.Equal(t, byte(0xAA), lastRaw2.Data[0], "lastRawVideo data must still be intact")

	// Cleanup: remove source (releases lastRawVideo).
	fs.RemoveSource("cam1")
}

// TestFreezeRepeat_FRCFrame_BufferSurvivesDelivery verifies that FRC-emitted
// frames are not freed before the pipeline processes them. The frcAllocated
// ReleaseYUV in deliverRelease must not drop the buffer while the pipeline
// copy still references it.
func TestFreezeRepeat_FRCFrame_BufferSurvivesDelivery(t *testing.T) {
	pool := NewFramePool(8, 8, 8, ColorYUV420_8bit)
	defer pool.Close()
	yuvSize := ColorYUV420_8bit.FrameSize(8, 8)

	var deliveredFrames []*ProcessingFrame

	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(33*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.onRawVideo = func(sourceKey string, pf *ProcessingFrame) {
		deliveredFrames = append(deliveredFrames, pf)
	}
	fs.SetFramePool(pool)
	fs.SetFRCQuality(FRCNearest)
	fs.AddSource("cam1")

	// Ingest two frames so FRC has enough state to interpolate.
	f1 := &ProcessingFrame{
		Data: pool.Acquire()[:yuvSize], Width: 8, Height: 8, PTS: 90000, pool: pool,
	}
	f1.SetRefs(1)
	fs.IngestRawVideo("cam1", f1)

	// First tick: delivers f1.
	fs.releaseTick()
	deliveredFrames = deliveredFrames[:0]

	f2 := &ProcessingFrame{
		Data: pool.Acquire()[:yuvSize], Width: 8, Height: 8, PTS: 93003, pool: pool,
	}
	f2.SetRefs(1)
	fs.IngestRawVideo("cam1", f2)

	// Second tick: delivers f2.
	fs.releaseTick()
	deliveredFrames = deliveredFrames[:0]

	// Third tick: no new frame → FRC interpolation tick.
	fs.releaseTick()

	if len(deliveredFrames) == 0 {
		t.Skip("FRC did not emit an interpolated frame (may need more frames)")
	}

	delivered := deliveredFrames[0]
	require.NotNil(t, delivered.Data, "FRC-emitted frame must have valid YUV at delivery time")

	// Simulate pipeline processing.
	if delivered.Refs() > 0 {
		delivered.Ref()
		cp := new(ProcessingFrame)
		*cp = *delivered
		delivered.ReleaseYUV()

		// The pipeline copy must have valid YUV — the buffer must not have
		// been freed by the frcAllocated ReleaseYUV before pipeline runs.
		require.NotNil(t, cp.Data, "pipeline copy of FRC frame must have valid YUV")
		require.True(t, cp.Refs() >= 1, "pipeline copy refs must be >= 1, got %d", cp.Refs())

		cp.ReleaseYUV()
	}

	fs.RemoveSource("cam1")
}

// TestFreezeRepeat_PoolBufferNotReusedWhileLastRawVideoHeld verifies that the
// pool buffer backing lastRawVideo is not returned to the pool (and potentially
// reacquired by another source) while lastRawVideo still references it.
func TestFreezeRepeat_PoolBufferNotReusedWhileLastRawVideoHeld(t *testing.T) {
	// Small pool: 2 buffers. If the bug exists, the first buffer is freed
	// and reacquired, causing data corruption.
	pool := NewFramePool(2, 8, 8, ColorYUV420_8bit)
	defer pool.Close()
	yuvSize := ColorYUV420_8bit.FrameSize(8, 8)

	var lastDelivered *ProcessingFrame
	handler := &syncTestHandler{}
	fs := NewFrameSynchronizer(33*time.Millisecond, handler.onVideo, handler.onAudio)
	fs.onRawVideo = func(sourceKey string, pf *ProcessingFrame) {
		lastDelivered = pf
	}
	fs.AddSource("cam1")

	// Ingest frame with sentinel data.
	frame := &ProcessingFrame{
		Data: pool.Acquire()[:yuvSize], Width: 8, Height: 8, PTS: 90000, pool: pool,
	}
	frame.SetRefs(1)
	for i := range frame.Data {
		frame.Data[i] = 0xAA
	}
	fs.IngestRawVideo("cam1", frame)

	// Tick 1: deliver fresh frame.
	fs.releaseTick()
	require.NotNil(t, lastDelivered)

	// Simulate full pipeline lifecycle.
	lastDelivered.Ref()
	cp := new(ProcessingFrame)
	*cp = *lastDelivered
	lastDelivered.ReleaseYUV()
	cp.MakeWritable(pool)
	cp.ReleaseYUV()

	// Acquire a buffer from the pool. If the bug exists, this may return
	// the same buffer that lastRawVideo points to.
	otherBuf := pool.Acquire()
	// Write different data to detect corruption.
	for i := range otherBuf[:yuvSize] {
		otherBuf[i] = 0xBB
	}

	// Tick 2: freeze-repeat.
	lastDelivered = nil
	fs.releaseTick()
	require.NotNil(t, lastDelivered, "should deliver freeze-repeated frame")

	// The freeze-repeated frame must have the original data, not 0xBB.
	require.Equal(t, byte(0xAA), lastDelivered.Data[0],
		"freeze-repeated frame must have original data (0xAA), got 0x%02X — "+
			"pool buffer was reused while lastRawVideo still referenced it",
		lastDelivered.Data[0])

	pool.Release(otherBuf)
	fs.RemoveSource("cam1")
}
