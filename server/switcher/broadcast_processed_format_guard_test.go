package switcher

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/transition"
)

// ==========================================================================
// Regression guards: broadcastProcessed must use format-aware sizing.
//
// broadcastProcessed converts raw YUV from the transition engine into a
// ProcessingFrame for the pipeline. The bug: if it hardcodes
// ColorYUV420_8bit.FrameSize() or "h264" codec, 10-bit HEVC frames are either
// silently dropped (size check fails) or truncated and mis-tagged.
// ==========================================================================

// TestBroadcastProcessedGuard_10bit_SentinelSurvivesPipeline verifies that a
// 10-bit frame with sentinel data in the Cr plane survives through
// broadcastProcessed and reaches the pipeline's raw sink intact.
func TestBroadcastProcessedGuard_10bit_SentinelSurvivesPipeline(t *testing.T) {
	programRelay := distribution.NewRelay()
	sw := newTestSwitcherDirect(programRelay)
	defer sw.closeDirect()

	w, h := 16, 16
	fmt10 := PipelineFormat{
		Name:   "guard-10bit",
		Width:  w,
		Height: h,
		FPSNum: 30000,
		FPSDen: 1001,
		Color: PipelineColorConfig{
			ColorFormat: ColorYUV422_10bit,
			Codec:       CodecHEVC,
		},
	}
	sw.pipelineFormat.Store(&fmt10)
	sw.framePool.Store(NewFramePool(8, w, h, ColorYUV422_10bit))

	sw.SetPipelineCodecs(
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	)

	var capturedData []byte
	sink := RawVideoSink(func(pf *ProcessingFrame) {
		capturedData = make([]byte, len(pf.Data))
		copy(capturedData, pf.Data)
	})
	sw.rawVideoSink.Store(&sink)

	require.NoError(t, sw.BuildPipeline())

	cam1Relay := distribution.NewRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.configMu.Lock()
	sw.programSource = "cam1"
	sw.configMu.Unlock()

	// Build a 10-bit frame with sentinel in the last quarter (Cr plane tail).
	frameSize := ColorYUV422_10bit.FrameSize(w, h)
	yuv := make([]byte, frameSize)
	sentinel := byte(0xEF)
	// Fill Y plane with valid 10-bit data.
	yPlaneSize := w * h * 2
	for i := 0; i < yPlaneSize; i += 2 {
		binary.LittleEndian.PutUint16(yuv[i:], 500)
	}
	// Fill chroma with neutral.
	for i := yPlaneSize; i < frameSize; i += 2 {
		binary.LittleEndian.PutUint16(yuv[i:], 512)
	}
	// Overwrite last quarter with sentinel.
	for i := frameSize * 3 / 4; i < frameSize; i++ {
		yuv[i] = sentinel
	}

	sw.broadcastProcessed(yuv, w, h, 90000, true)

	// Run pipeline synchronously — raw sink callback fires during Run().
	frame := sw.runPipelineDirect(t)
	if frame != nil {
		frame.ReleaseYUV()
	}

	eightBitSize := ColorYUV420_8bit.FrameSize(w, h)

	// Guard 1: full 10-bit data reached the pipeline.
	require.Equal(t, frameSize, len(capturedData),
		"frame data truncated: got %d, want %d (8-bit truncation = %d)",
		len(capturedData), frameSize, eightBitSize)

	// Guard 2: sentinel in Cr plane is intact.
	for i := frameSize * 3 / 4; i < frameSize; i++ {
		if capturedData[i] != sentinel {
			t.Fatalf("sentinel lost at byte %d: got 0x%02X, want 0x%02X "+
				"(broadcastProcessed truncated to 8-bit?)", i, capturedData[i], sentinel)
		}
	}
}

// TestBroadcastProcessedGuard_10bit_CodecMatchesPipeline verifies that
// broadcastProcessed sets codec to "h265" (not "h264") for HEVC pipelines.
// Incorrect codec tagging causes downstream muxers to fail.
func TestBroadcastProcessedGuard_10bit_CodecMatchesPipeline(t *testing.T) {
	programRelay := distribution.NewRelay()
	sw := newTestSwitcherDirect(programRelay)
	defer sw.closeDirect()

	w, h := 16, 16
	fmt10 := PipelineFormat{
		Name:   "guard-hevc",
		Width:  w,
		Height: h,
		FPSNum: 30000,
		FPSDen: 1001,
		Color: PipelineColorConfig{
			ColorFormat: ColorYUV422_10bit,
			Codec:       CodecHEVC,
		},
	}
	sw.pipelineFormat.Store(&fmt10)
	sw.framePool.Store(NewFramePool(8, w, h, ColorYUV422_10bit))

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

	frameSize := ColorYUV422_10bit.FrameSize(w, h)
	yuv := make([]byte, frameSize)
	for i := 0; i < frameSize; i += 2 {
		binary.LittleEndian.PutUint16(yuv[i:], 512)
	}

	sw.broadcastProcessed(yuv, w, h, 90000, true)

	// Run pipeline synchronously — raw sink callback fires during Run().
	frame := sw.runPipelineDirect(t)
	if frame != nil {
		frame.ReleaseYUV()
	}

	assert.Equal(t, "h265", capturedCodec,
		"HEVC pipeline should tag frames as h265, got %q (hardcoded h264?)", capturedCodec)
	assert.Equal(t, ColorYUV422_10bit, capturedFormat,
		"frame Format should be 10-bit, got %v (hardcoded 8-bit?)", capturedFormat)
}

// TestBroadcastProcessedGuard_10bit_NotSilentlyDropped verifies that a properly
// sized 10-bit frame is NOT silently dropped by the size validation check.
// Before the fix, broadcastProcessed compared len(yuv) against 8-bit frame size,
// which was always smaller than the actual 10-bit frame, passing the check.
// But then it would copy only expectedSize bytes, truncating the frame.
func TestBroadcastProcessedGuard_10bit_NotSilentlyDropped(t *testing.T) {
	programRelay := distribution.NewRelay()
	sw := newTestSwitcherDirect(programRelay)
	defer sw.closeDirect()

	w, h := 16, 16
	fmt10 := PipelineFormat{
		Name:   "guard-drop",
		Width:  w,
		Height: h,
		FPSNum: 30000,
		FPSDen: 1001,
		Color: PipelineColorConfig{
			ColorFormat: ColorYUV422_10bit,
			Codec:       CodecHEVC,
		},
	}
	sw.pipelineFormat.Store(&fmt10)
	sw.framePool.Store(NewFramePool(8, w, h, ColorYUV422_10bit))

	sw.SetPipelineCodecs(
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	)

	sinkCalled := false
	sink2 := RawVideoSink(func(pf *ProcessingFrame) {
		sinkCalled = true
	})
	sw.rawVideoSink.Store(&sink2)

	require.NoError(t, sw.BuildPipeline())

	cam1Relay := distribution.NewRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.configMu.Lock()
	sw.programSource = "cam1"
	sw.configMu.Unlock()

	frameSize := ColorYUV422_10bit.FrameSize(w, h)
	yuv := make([]byte, frameSize)
	for i := 0; i < frameSize; i += 2 {
		binary.LittleEndian.PutUint16(yuv[i:], 500)
	}

	countBefore := sw.transOutputCount.Load()
	sw.broadcastProcessed(yuv, w, h, 90000, true)
	countAfter := sw.transOutputCount.Load()

	// The frame must be accepted (counter incremented).
	assert.Equal(t, countBefore+1, countAfter,
		"broadcastProcessed should accept 10-bit frames (counter not incremented)")

	// Run pipeline synchronously — raw sink callback fires during Run().
	frame := sw.runPipelineDirect(t)
	if frame != nil {
		frame.ReleaseYUV()
	}

	assert.True(t, sinkCalled,
		"10-bit frame was silently dropped — never reached raw sink")
}
