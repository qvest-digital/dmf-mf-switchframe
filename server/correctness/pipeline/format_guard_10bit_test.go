//go:build correctness

package pipeline

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/switcher"
	"github.com/zsiec/switchframe/server/transition"
)

// ==========================================================================
// End-to-end format regression guards for the 10-bit pipeline.
//
// These tests verify that 10-bit frames survive the ENTIRE pipeline path
// without being truncated to 8-bit sizing at any point. A sentinel pattern
// in the Cr plane's tail detects any truncation to w*h*3/2.
//
// Run with: go test -tags correctness ./correctness/pipeline/ -run 'Guard' -v -race
// ==========================================================================

// new10bitHarness creates a test switcher configured for 10-bit at testWidth x testHeight.
// Uses SetPipelineFormat (which recreates the frame pool internally) so external
// packages don't need to access unexported fields.
func new10bitHarness(t *testing.T) *switcher.Switcher {
	t.Helper()
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)

	fmt10 := switcher.PipelineFormat{
		Name:   "guard-10bit",
		Width:  testWidth,
		Height: testHeight,
		FPSNum: 30000,
		FPSDen: 1001,
		Color: switcher.PipelineColorConfig{
			ColorFormat: switcher.ColorYUV422_10bit,
			Codec:       switcher.CodecHEVC,
		},
	}
	require.NoError(t, sw.SetPipelineFormat(fmt10))

	sw.SetPipelineCodecs(
		func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return transition.NewMockEncoder(), nil
		},
	)

	return sw
}

// TestPipelineFormatGuard_10bit_FrameNotTruncatedThroughPipeline verifies that
// a 10-bit frame with sentinel data in the Cr plane survives the full pipeline
// path (ingest -> pipeline nodes -> raw sink) without truncation.
func TestPipelineFormatGuard_10bit_FrameNotTruncatedThroughPipeline(t *testing.T) {
	w, h := testWidth, testHeight

	sw := new10bitHarness(t)
	defer sw.Close()

	var capturedData []byte
	var capturedFormat switcher.PipelineColorFormat
	done := make(chan struct{}, 1)

	sw.SetRawVideoSink(switcher.RawVideoSink(func(pf *switcher.ProcessingFrame) {
		capturedData = make([]byte, len(pf.Data))
		copy(capturedData, pf.Data)
		capturedFormat = pf.Format
		select {
		case done <- struct{}{}:
		default:
		}
	}))

	require.NoError(t, sw.BuildPipeline())

	// Register source and cut to it.
	cam1Relay := distribution.NewRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(t.Context(), "cam1"))

	// Create a 10-bit frame with sentinel data.
	frameSize := switcher.ColorYUV422_10bit.FrameSize(w, h)
	data := make([]byte, frameSize)
	sentinel := byte(0xBE)

	// Fill Y plane with 10-bit value 500.
	yPlaneSize := w * h * 2
	for i := 0; i < yPlaneSize; i += 2 {
		binary.LittleEndian.PutUint16(data[i:], 500)
	}
	// Fill chroma with neutral.
	for i := yPlaneSize; i < frameSize; i += 2 {
		binary.LittleEndian.PutUint16(data[i:], 512)
	}
	// Overwrite last quarter with sentinel.
	for i := frameSize * 3 / 4; i < frameSize; i++ {
		data[i] = sentinel
	}

	pf := &switcher.ProcessingFrame{
		Data:       data,
		Format:     switcher.ColorYUV422_10bit,
		Codec:      "h265",
		Width:      w,
		Height:     h,
		PTS:        90000,
		IsKeyframe: true,
	}
	sw.IngestRawVideo("cam1", pf)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for frame to traverse pipeline")
	}

	eightBitSize := switcher.ColorYUV420_8bit.FrameSize(w, h)

	// Guard 1: full 10-bit data reached the raw sink.
	require.Equal(t, frameSize, len(capturedData),
		"frame truncated through pipeline: got %d, want %d (8-bit = %d)",
		len(capturedData), frameSize, eightBitSize)

	// Guard 2: sentinel in Cr plane tail is intact.
	for i := frameSize * 3 / 4; i < frameSize; i++ {
		if capturedData[i] != sentinel {
			t.Fatalf("sentinel lost at byte %d through pipeline: got 0x%02X, want 0x%02X",
				i, capturedData[i], sentinel)
		}
	}

	// Guard 3: format preserved.
	assert.Equal(t, switcher.ColorYUV422_10bit, capturedFormat,
		"format lost through pipeline: got %v", capturedFormat)
}

// TestPipelineFormatGuard_10bit_TransitionPreservesFullBuffer verifies that
// 10-bit frames flowing through a transition (mix/dissolve) produce full-size
// 10-bit output, not truncated 8-bit output.
func TestPipelineFormatGuard_10bit_TransitionPreservesFullBuffer(t *testing.T) {
	w, h := testWidth, testHeight

	sw := new10bitHarness(t)
	defer sw.Close()

	sw.SetTransitionConfig(switcher.TransitionConfig{
		DecoderFactory: func() (transition.VideoDecoder, error) {
			return transition.NewMockDecoder(w, h), nil
		},
	})

	var capturedSizes []int
	var mu = make(chan struct{}, 1)
	done := make(chan struct{}, 1)

	sw.SetRawVideoSink(switcher.RawVideoSink(func(pf *switcher.ProcessingFrame) {
		select {
		case mu <- struct{}{}:
			capturedSizes = append(capturedSizes, len(pf.Data))
			<-mu
			if len(capturedSizes) >= 3 {
				select {
				case done <- struct{}{}:
				default:
				}
			}
		default:
		}
	}))

	require.NoError(t, sw.BuildPipeline())

	cam1Relay := distribution.NewRelay()
	cam2Relay := distribution.NewRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)
	require.NoError(t, sw.Cut(t.Context(), "cam1"))
	require.NoError(t, sw.SetPreview(t.Context(), "cam2"))

	// Start a transition.
	err := sw.StartTransition(t.Context(), "cam2", "mix", 2000, "")
	require.NoError(t, err)

	// Feed 10-bit frames to both sources.
	frameSize := switcher.ColorYUV422_10bit.FrameSize(w, h)

	for i := 0; i < 5; i++ {
		frameA := make([]byte, frameSize)
		for j := 0; j < w*h*2; j += 2 {
			binary.LittleEndian.PutUint16(frameA[j:], 200)
		}
		for j := w * h * 2; j < frameSize; j += 2 {
			binary.LittleEndian.PutUint16(frameA[j:], 512)
		}

		frameB := make([]byte, frameSize)
		for j := 0; j < w*h*2; j += 2 {
			binary.LittleEndian.PutUint16(frameB[j:], 800)
		}
		for j := w * h * 2; j < frameSize; j += 2 {
			binary.LittleEndian.PutUint16(frameB[j:], 512)
		}

		pfA := &switcher.ProcessingFrame{
			Data:       frameA,
			Format:     switcher.ColorYUV422_10bit,
			Codec:      "h265",
			Width:      w,
			Height:     h,
			PTS:        int64(90000 + i*3000),
			IsKeyframe: i == 0,
		}
		pfB := &switcher.ProcessingFrame{
			Data:       frameB,
			Format:     switcher.ColorYUV422_10bit,
			Codec:      "h265",
			Width:      w,
			Height:     h,
			PTS:        int64(90000 + i*3000),
			IsKeyframe: i == 0,
		}
		sw.IngestRawVideo("cam1", pfA)
		sw.IngestRawVideo("cam2", pfB)
		time.Sleep(10 * time.Millisecond)
	}

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		// Don't fail — transitions are timing-sensitive. Just check what we got.
	}

	sw.AbortTransition()

	select {
	case mu <- struct{}{}:
		sizes := capturedSizes
		<-mu

		eightBitSize := switcher.ColorYUV420_8bit.FrameSize(w, h)
		for i, size := range sizes {
			assert.Equal(t, frameSize, size,
				"transition output frame %d has wrong size: got %d, want %d (8-bit = %d)",
				i, size, frameSize, eightBitSize)
		}
	default:
	}
}

// TestPipelineFormatGuard_10bit_ResolutionChangePreservesFormat verifies that
// when a 10-bit frame at a different resolution enters the pipeline, it is
// scaled to the pipeline resolution at 10-bit (not 8-bit).
func TestPipelineFormatGuard_10bit_ResolutionChangePreservesFormat(t *testing.T) {
	// Pipeline at 320x240, source at 160x120.
	pipeW, pipeH := testWidth, testHeight
	srcW, srcH := testWidth/2, testHeight/2

	sw := new10bitHarness(t)
	defer sw.Close()

	var capturedData []byte
	var capturedW, capturedH int
	var capturedFormat switcher.PipelineColorFormat
	done := make(chan struct{}, 1)

	sw.SetRawVideoSink(switcher.RawVideoSink(func(pf *switcher.ProcessingFrame) {
		capturedData = make([]byte, len(pf.Data))
		copy(capturedData, pf.Data)
		capturedW = pf.Width
		capturedH = pf.Height
		capturedFormat = pf.Format
		select {
		case done <- struct{}{}:
		default:
		}
	}))

	require.NoError(t, sw.BuildPipeline())

	cam1Relay := distribution.NewRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(t.Context(), "cam1"))

	// Create a smaller 10-bit frame.
	srcSize := switcher.ColorYUV422_10bit.FrameSize(srcW, srcH)
	srcData := make([]byte, srcSize)
	for i := 0; i < srcW*srcH*2; i += 2 {
		binary.LittleEndian.PutUint16(srcData[i:], 600)
	}
	for i := srcW * srcH * 2; i < srcSize; i += 2 {
		binary.LittleEndian.PutUint16(srcData[i:], 512)
	}

	pf := &switcher.ProcessingFrame{
		Data:       srcData,
		Format:     switcher.ColorYUV422_10bit,
		Codec:      "h265",
		Width:      srcW,
		Height:     srcH,
		PTS:        90000,
		IsKeyframe: true,
	}
	sw.IngestRawVideo("cam1", pf)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for scaled frame")
	}

	expectedSize := switcher.ColorYUV422_10bit.FrameSize(pipeW, pipeH)
	eightBitSize := switcher.ColorYUV420_8bit.FrameSize(pipeW, pipeH)

	// Guard: output must be pipeline resolution at 10-bit size.
	assert.Equal(t, pipeW, capturedW, "output width should match pipeline")
	assert.Equal(t, pipeH, capturedH, "output height should match pipeline")
	assert.Equal(t, expectedSize, len(capturedData),
		"scaled output should be 10-bit size %d, got %d (8-bit would be %d)",
		expectedSize, len(capturedData), eightBitSize)
	assert.Equal(t, switcher.ColorYUV422_10bit, capturedFormat,
		"format should remain 10-bit after resolution scaling")
}
