package switcher

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/transition"
)

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

// A pool-backed unmanaged frame (refs==nil, pool!=nil, as the SRT pipeline
// builds frames via SetPool without SetRefs) ingested through IngestRawVideo
// goes back to the pool once a later frame of the source replaces it. The
// sync loop shows it through a pool-detached copy, so neither the pipeline
// nor the retained last frame can return its buffer twice.
func TestIngestRawVideo_ReleasesPoolBackedUnmanagedFrame(t *testing.T) {
	programRelay := distribution.NewRelay()
	sw := newTestSwitcherDirect(programRelay)
	defer sw.closeDirect()

	w, h := 16, 16
	fmt8 := PipelineFormat{
		Name: "direct", Width: w, Height: h, FPSNum: 30000, FPSDen: 1001,
		Color: PipelineColorConfig{ColorFormat: ColorYUV420_8bit, Codec: CodecAVC},
	}
	sw.pipelineFormat.Store(&fmt8)
	pool := NewFramePool(8, w, h, ColorYUV420_8bit)
	sw.framePool.Store(pool)

	sw.SetPipelineCodecs(func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
		return transition.NewMockEncoder(), nil
	})
	require.NoError(t, sw.BuildPipeline())

	sw.RegisterSRTSource("srt:cam1")
	require.NoError(t, sw.Cut(t.Context(), "srt:cam1"))

	yuvSize := ColorYUV420_8bit.FrameSize(w, h)
	unmanaged := func() *ProcessingFrame {
		pf := &ProcessingFrame{Data: pool.Acquire()[:yuvSize], Width: w, Height: h, Codec: "h264"}
		pf.SetPool(pool)
		return pf
	}
	pf := unmanaged()
	require.Equal(t, int32(0), pf.Refs(), "frame must be unmanaged (nil refs)")
	origPtr := &pf.Data[:cap(pf.Data)][0]

	sw.IngestRawVideo("srt:cam1", pf)
	sw.tickNow()
	require.False(t, framePoolHasFreeBuf(pool, origPtr),
		"buffer returned to the pool while the frame is the source's last frame")

	sw.IngestRawVideo("srt:cam1", unmanaged())
	sw.tickNow()
	require.True(t, framePoolHasFreeBuf(pool, origPtr),
		"replaced unmanaged frame's buffer not returned to the pool")
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
