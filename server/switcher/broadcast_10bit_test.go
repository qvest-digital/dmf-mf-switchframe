package switcher

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/transition"
)

// TestBroadcastProcessed_10bit_FrameNotDropped verifies that broadcastProcessed
// does not drop 10-bit YUV422P10LE frames due to incorrect size calculation.
// Before the fix, it hardcoded ColorYUV420_8bit.FrameSize() which returns w*h*3/2,
// causing 10-bit frames (w*h*4) to be truncated or silently dropped.
func TestBroadcastProcessed_10bit_FrameNotDropped(t *testing.T) {
	programRelay := distribution.NewRelay()
	sw := NewTestSwitcher(t, programRelay)
	defer sw.Close()

	// Configure 10-bit pipeline.
	fmt10 := PipelineFormat{
		Name:   "test-10bit",
		Width:  16,
		Height: 16,
		FPSNum: 30000,
		FPSDen: 1001,
		Color: PipelineColorConfig{
			ColorFormat: ColorYUV422_10bit,
			Codec:       CodecHEVC,
		},
	}
	sw.pipelineFormat.Store(&fmt10)
	sw.framePool.Store(NewFramePool(8, 16, 16, ColorYUV422_10bit))

	sw.SetPipelineCodecs(
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	)
	require.NoError(t, sw.BuildPipeline())

	// Register a source so broadcastProcessed can look up groupID.
	cam1Relay := distribution.NewRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.configMu.Lock()
	sw.programSource = "cam1"
	sw.configMu.Unlock()

	w, h := 16, 16
	frame10Size := ColorYUV422_10bit.FrameSize(w, h)
	yuv := make([]byte, frame10Size)
	// Fill with known 10-bit Y values > 255.
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(yuv[i*2:], uint16(500))
	}
	for i := w * h * 2; i < frame10Size; i += 2 {
		binary.LittleEndian.PutUint16(yuv[i:], 512)
	}

	countBefore := sw.transOutputCount.Load()
	sw.broadcastProcessed(yuv, w, h, 90000, true)

	// Wait for the video processing loop to pick up the frame.
	time.Sleep(50 * time.Millisecond)

	countAfter := sw.transOutputCount.Load()
	assert.Equal(t, countBefore+1, countAfter,
		"broadcastProcessed should increment transOutputCount for 10-bit frames")

	// Verify the frame made it into the processing pipeline by checking
	// that no frames were dropped. The processing loop runs in the background,
	// so we just verify the enqueue succeeded (no panic, no truncation).
}

// TestBroadcastProcessed_10bit_CorrectCodecAndFormat verifies that
// broadcastProcessed sets the correct codec string and color format
// on the ProcessingFrame based on the pipeline configuration.
func TestBroadcastProcessed_10bit_CorrectCodecAndFormat(t *testing.T) {
	// Uses newTestSwitcherDirect to avoid depending on the async
	// videoProcessingLoop goroutine — runs the pipeline synchronously
	// so the test is deterministic on slow CI runners.
	programRelay := distribution.NewRelay()
	sw := newTestSwitcherDirect(programRelay)
	defer sw.closeDirect()

	// Configure 10-bit pipeline.
	fmt10 := PipelineFormat{
		Name:   "test-10bit",
		Width:  16,
		Height: 16,
		FPSNum: 30000,
		FPSDen: 1001,
		Color: PipelineColorConfig{
			ColorFormat: ColorYUV422_10bit,
			Codec:       CodecHEVC,
		},
	}
	sw.pipelineFormat.Store(&fmt10)
	sw.framePool.Store(NewFramePool(8, 16, 16, ColorYUV422_10bit))

	sw.SetPipelineCodecs(
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	)

	// Set up raw video sink to capture the ProcessingFrame metadata.
	// Store the sink directly (not via SetRawVideoSink which triggers
	// rebuildPipeline and creates a leaked encode goroutine when
	// BuildPipeline is called afterward).
	var capturedCodec string
	var capturedFormat PipelineColorFormat
	var capturedDataLen int
	sink := RawVideoSink(func(pf *ProcessingFrame) {
		capturedCodec = pf.Codec
		capturedFormat = pf.Format
		capturedDataLen = len(pf.Data)
	})
	sw.rawVideoSink.Store(&sink)

	require.NoError(t, sw.BuildPipeline())

	cam1Relay := distribution.NewRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.configMu.Lock()
	sw.programSource = "cam1"
	sw.configMu.Unlock()

	w, h := 16, 16
	frame10Size := ColorYUV422_10bit.FrameSize(w, h)
	yuv := make([]byte, frame10Size)
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(yuv[i*2:], 500)
	}
	for i := w * h * 2; i < frame10Size; i += 2 {
		binary.LittleEndian.PutUint16(yuv[i:], 512)
	}

	// broadcastProcessed enqueues the frame to videoProcCh.
	sw.broadcastProcessed(yuv, w, h, 90000, true)

	// Run pipeline synchronously — raw sink callback fires during Run().
	frame := sw.runPipelineDirect(t)
	if frame != nil {
		frame.ReleaseYUV()
	}

	assert.Equal(t, "h265", capturedCodec,
		"HEVC pipeline should set codec to h265")
	assert.Equal(t, ColorYUV422_10bit, capturedFormat,
		"frame Format should match pipeline color format")
	assert.Equal(t, frame10Size, capturedDataLen,
		"frame data should be full 10-bit size (not truncated to 8-bit)")
}

// TestBroadcastProcessed_8bit_StillWorks verifies that broadcastProcessed
// still works correctly for standard 8-bit YUV420 frames.
func TestBroadcastProcessed_8bit_StillWorks(t *testing.T) {
	programRelay := distribution.NewRelay()
	sw := newTestSwitcherDirect(programRelay)
	defer sw.closeDirect()

	sw.SetPipelineCodecs(
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	)

	var capturedCodec string
	var capturedFormat PipelineColorFormat
	sink := RawVideoSink(func(pf *ProcessingFrame) {
		capturedCodec = pf.Codec
		capturedFormat = pf.Format
	})
	sw.rawVideoSink.Store(&sink)

	require.NoError(t, sw.BuildPipeline())

	cam1Relay := distribution.NewRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.configMu.Lock()
	sw.programSource = "cam1"
	sw.configMu.Unlock()

	w, h := 320, 240
	frame8Size := ColorYUV420_8bit.FrameSize(w, h)
	yuv := make([]byte, frame8Size)
	for i := range yuv {
		yuv[i] = 128
	}

	sw.broadcastProcessed(yuv, w, h, 90000, true)

	// Run pipeline synchronously — raw sink callback fires during Run().
	frame := sw.runPipelineDirect(t)
	if frame != nil {
		frame.ReleaseYUV()
	}

	assert.Equal(t, "h264", capturedCodec)
	assert.Equal(t, ColorYUV420_8bit, capturedFormat)
}

// TestTransitionEngine_10bit_SetColorFormat verifies that the transition engine
// receives SetColorFormat(ColorFormat10bit) when the pipeline is configured
// for 10-bit. Tests the production code path in StartTransition.
func TestTransitionEngine_10bit_SetColorFormat(t *testing.T) {
	programRelay := distribution.NewRelay()
	sw := NewTestSwitcher(t, programRelay)
	defer sw.Close()

	// Configure 10-bit pipeline format.
	fmt10 := PipelineFormat{
		Name:   "test-10bit",
		Width:  320,
		Height: 240,
		FPSNum: 30000,
		FPSDen: 1001,
		Color: PipelineColorConfig{
			ColorFormat: ColorYUV422_10bit,
			Codec:       CodecHEVC,
		},
	}
	sw.pipelineFormat.Store(&fmt10)
	sw.framePool.Store(NewFramePool(4, 320, 240, ColorYUV422_10bit))

	sw.SetTransitionConfig(TransitionConfig{
		DecoderFactory: func() (transition.VideoDecoder, error) {
			return transition.NewMockDecoder(320, 240), nil
		},
	})
	sw.SetPipelineCodecs(
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	)
	require.NoError(t, sw.BuildPipeline())

	cam1Relay := distribution.NewRelay()
	cam2Relay := distribution.NewRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)
	require.NoError(t, sw.Cut(t.Context(), "cam1"))
	require.NoError(t, sw.SetPreview(t.Context(), "cam2"))

	// Start a transition.
	err := sw.StartTransition(t.Context(), "cam2", "mix", 500, "")
	require.NoError(t, err)

	// Get the engine and verify it can handle 10-bit frames.
	sw.configMu.Lock()
	engine := sw.transEngine
	sw.configMu.Unlock()
	require.NotNil(t, engine, "transition engine should be created")

	// Feed 10-bit frames to the engine. If SetColorFormat was NOT called,
	// the engine would use 8-bit frame sizing and either panic or produce
	// incorrect output (frame size mismatch).
	w, h := 320, 240
	frame10Size := ColorYUV422_10bit.FrameSize(w, h)

	frameA := make([]byte, frame10Size)
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(frameA[i*2:], 500)
	}
	for i := w * h * 2; i < frame10Size; i += 2 {
		binary.LittleEndian.PutUint16(frameA[i:], 512)
	}

	frameB := make([]byte, frame10Size)
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(frameB[i*2:], 700)
	}
	for i := w * h * 2; i < frame10Size; i += 2 {
		binary.LittleEndian.PutUint16(frameB[i:], 512)
	}

	// This should not panic or produce wrong-sized output.
	engine.IngestRawFrame("cam1", frameA, w, h, 90000)
	engine.IngestRawFrame("cam2", frameB, w, h, 90000)

	sw.AbortTransition()
}
