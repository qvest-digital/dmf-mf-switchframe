package switcher

import (
	"encoding/binary"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/transition"
)

// sinkCapture records what the pipeline's raw sink saw last.
type sinkCapture struct {
	mu     sync.Mutex
	n      int
	codec  string
	format PipelineColorFormat
	data   []byte
}

// formatSwitcher returns a clock-less switcher whose pipeline runs format
// cf at 16x16 with a mock encoder and a capturing raw sink.
func formatSwitcher(t *testing.T, cf PipelineColorFormat, codec PipelineCodec) (*Switcher, *sinkCapture) {
	t.Helper()
	sw := tickSwitcher(t)
	f := PipelineFormat{
		Name: "fmt", Width: 16, Height: 16, FPSNum: 30000, FPSDen: 1001,
		Color: PipelineColorConfig{ColorFormat: cf, Codec: codec},
	}
	sw.pipelineFormat.Store(&f)
	sw.framePool.Store(NewFramePool(8, 16, 16, cf))
	sw.SetPipelineCodecs(func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
		return transition.NewMockEncoder(), nil
	})
	c := &sinkCapture{}
	// Stored directly: SetRawVideoSink rebuilds the pipeline.
	sink := RawVideoSink(func(pf *ProcessingFrame) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.n++
		c.codec, c.format = pf.Codec, pf.Format
		c.data = append(c.data[:0], pf.Data...)
	})
	sw.rawVideoSink.Store(&sink)
	require.NoError(t, sw.BuildPipeline())
	return sw, c
}

// frame10 is a 16x16 10-bit 4:2:2 frame of luma y, its last quarter set to
// sentinel bytes when sentinel != 0, stamped with grain i.
func frame10(sw *Switcher, i uint64, y uint16, sentinel byte) *ProcessingFrame {
	size := ColorYUV422_10bit.FrameSize(16, 16)
	buf := make([]byte, size)
	for j := 0; j < 16*16; j++ {
		binary.LittleEndian.PutUint16(buf[j*2:], y)
	}
	for j := 16 * 16 * 2; j < size; j += 2 {
		binary.LittleEndian.PutUint16(buf[j:], 512)
	}
	if sentinel != 0 {
		for j := size * 3 / 4; j < size; j++ {
			buf[j] = sentinel
		}
	}
	return &ProcessingFrame{Data: buf, Width: 16, Height: 16, Format: ColorYUV422_10bit,
		Codec: "h265", PTS: grainPTS(sw, i)}
}

// A 10-bit source frame reaches the pipeline whole: full size, the
// sentinel in the Cr plane intact.
func TestProgram_10bit_SentinelSurvivesPipeline(t *testing.T) {
	sw, c := formatSwitcher(t, ColorYUV422_10bit, CodecHEVC)
	onProgram(t, sw, "mxl:cam1")
	g := grainNow(sw)
	sw.IngestRawVideo("mxl:cam1", frame10(sw, g, 500, 0xEF))
	sw.processVideoTick(tickAt(sw, g+clock.ReaderDelayGrains))

	c.mu.Lock()
	defer c.mu.Unlock()
	require.Equal(t, 1, c.n, "frame never reached the raw sink")
	size := ColorYUV422_10bit.FrameSize(16, 16)
	require.Len(t, c.data, size, "frame data truncated (8-bit size = %d)", ColorYUV420_8bit.FrameSize(16, 16))
	for i := size * 3 / 4; i < size; i++ {
		require.Equal(t, byte(0xEF), c.data[i], "sentinel lost at byte %d", i)
	}
	require.Equal(t, ColorYUV422_10bit, c.format)
}

// A transition in a 10-bit HEVC pipeline blends 10-bit frames: the blended
// frame reaches the pipeline whole and tagged with the pipeline's format
// and codec.
func TestTransitionOutput_10bit_CorrectCodecAndFormat(t *testing.T) {
	sw, c := formatSwitcher(t, ColorYUV422_10bit, CodecHEVC)
	sw.SetTransitionConfig(TransitionConfig{})
	onProgram(t, sw, "mxl:cam1")
	sw.RegisterMXLSource("mxl:cam2")
	require.NoError(t, sw.StartTransition(t.Context(), "mxl:cam2", "mix", 60000, ""))
	defer sw.AbortTransition()

	g := grainNow(sw)
	sw.IngestRawVideo("mxl:cam1", frame10(sw, g, 500, 0))
	sw.IngestRawVideo("mxl:cam2", frame10(sw, g, 700, 0))
	sw.processVideoTick(tickAt(sw, g+clock.ReaderDelayGrains))

	c.mu.Lock()
	defer c.mu.Unlock()
	require.Equal(t, 1, c.n, "transition output never reached the raw sink")
	assert.Equal(t, "h265", c.codec, "HEVC pipeline should set codec to h265")
	assert.Equal(t, ColorYUV422_10bit, c.format, "frame Format should match pipeline color format")
	assert.Len(t, c.data, ColorYUV422_10bit.FrameSize(16, 16),
		"frame data should be full 10-bit size (not truncated to 8-bit)")
}

// The same for an 8-bit AVC pipeline.
func TestTransitionOutput_8bit_StillWorks(t *testing.T) {
	sw, c := formatSwitcher(t, ColorYUV420_8bit, CodecAVC)
	sw.SetTransitionConfig(TransitionConfig{})
	onProgram(t, sw, "mxl:cam1")
	sw.RegisterMXLSource("mxl:cam2")
	require.NoError(t, sw.StartTransition(t.Context(), "mxl:cam2", "mix", 60000, ""))
	defer sw.AbortTransition()

	g := grainNow(sw)
	for _, k := range []string{"mxl:cam1", "mxl:cam2"} {
		pf := &ProcessingFrame{Data: makeTestYUV(16, 16, 128), Width: 16, Height: 16,
			Format: ColorYUV420_8bit, Codec: "h264", PTS: grainPTS(sw, g)}
		sw.IngestRawVideo(k, pf)
	}
	sw.processVideoTick(tickAt(sw, g+clock.ReaderDelayGrains))

	c.mu.Lock()
	defer c.mu.Unlock()
	require.Equal(t, 1, c.n, "transition output never reached the raw sink")
	assert.Equal(t, "h264", c.codec)
	assert.Equal(t, ColorYUV420_8bit, c.format)
}

// Black is in the pipeline's format: 10-bit 4:2:2 black is Y=64, Cb=Cr=512.
func TestProgramBlack_InPipelineFormat(t *testing.T) {
	sw, c := formatSwitcher(t, ColorYUV422_10bit, CodecHEVC)
	onProgram(t, sw, "mxl:never") // delivers nothing: black
	sw.tickNow()

	c.mu.Lock()
	defer c.mu.Unlock()
	require.Equal(t, 1, c.n, "black never reached the raw sink")
	require.Equal(t, ColorYUV422_10bit, c.format)
	require.Len(t, c.data, ColorYUV422_10bit.FrameSize(16, 16))
	ySize := ColorYUV422_10bit.YPlaneSize(16, 16)
	require.Equal(t, uint16(64), binary.LittleEndian.Uint16(c.data[0:]))
	require.Equal(t, uint16(64), binary.LittleEndian.Uint16(c.data[ySize-2:]))
	require.Equal(t, uint16(512), binary.LittleEndian.Uint16(c.data[ySize:]))
	require.Equal(t, uint16(512), binary.LittleEndian.Uint16(c.data[len(c.data)-2:]))
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
