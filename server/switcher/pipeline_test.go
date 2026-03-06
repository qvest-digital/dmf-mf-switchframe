// server/switcher/pipeline_test.go
package switcher

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/transition"
)

func TestPipeline_NoProcessorsPassthrough(t *testing.T) {
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)
	sw.SetPipelineCodecs(
		func() (transition.VideoDecoder, error) { return transition.NewMockDecoder(4, 4), nil },
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) { return transition.NewMockEncoder(), nil },
	)
	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	frame := &media.VideoFrame{PTS: 100, IsKeyframe: true, WireData: []byte{0x00, 0x00, 0x00, 0x01, 0x65}}
	cam1Relay.BroadcastVideo(frame)

	time.Sleep(10 * time.Millisecond)

	viewer.mu.Lock()
	require.GreaterOrEqual(t, len(viewer.videos), 1)
	// Passthrough: frame should be the exact same object (no decode/encode)
	require.Same(t, frame, viewer.videos[len(viewer.videos)-1])
	viewer.mu.Unlock()
}

func TestPipeline_CompositorActiveDecodesOnce(t *testing.T) {
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)

	decodeCount := 0
	encodeCount := 0
	sw.SetPipelineCodecs(
		func() (transition.VideoDecoder, error) {
			decodeCount++
			return transition.NewMockDecoder(4, 4), nil
		},
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			encodeCount++
			return transition.NewMockEncoder(), nil
		},
	)

	comp := graphics.NewCompositor()
	rgba := make([]byte, 4*4*4)
	for i := 3; i < len(rgba); i += 4 {
		rgba[i] = 255
	}
	require.NoError(t, comp.SetOverlay(rgba, 4, 4, "test"))
	require.NoError(t, comp.On())
	sw.SetCompositor(comp)

	defer sw.Close()

	cam1Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	cam1Relay.BroadcastVideo(&media.VideoFrame{
		PTS: 100, IsKeyframe: true,
		WireData: []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x88, 0x80, 0x40},
		SPS: []byte{0x67, 0x42, 0x00, 0x0a}, PPS: []byte{0x68, 0x42, 0x00},
	})

	time.Sleep(10 * time.Millisecond)

	// Should have created exactly 1 decoder and 1 encoder
	require.Equal(t, 1, decodeCount, "should decode exactly once")
	require.Equal(t, 1, encodeCount, "should encode exactly once")
}

func TestPipeline_TransitionPlusCompositor_SingleEncode(t *testing.T) {
	programRelay := newTestRelay()
	viewer := newMockProgramViewer("test")
	programRelay.AddViewer(viewer)

	sw := New(programRelay)
	sw.SetTransitionConfig(TransitionConfig{
		DecoderFactory: func() (transition.VideoDecoder, error) {
			return transition.NewMockDecoder(4, 4), nil
		},
	})

	encodeCount := 0
	sw.SetPipelineCodecs(
		func() (transition.VideoDecoder, error) { return transition.NewMockDecoder(4, 4), nil },
		func(w, h, bitrate int, fps float32) (transition.VideoEncoder, error) {
			encodeCount++
			return transition.NewMockEncoder(), nil
		},
	)

	comp := graphics.NewCompositor()
	rgba := make([]byte, 4*4*4)
	for i := 3; i < len(rgba); i += 4 {
		rgba[i] = 255
	}
	require.NoError(t, comp.SetOverlay(rgba, 4, 4, "test"))
	require.NoError(t, comp.On())
	sw.SetCompositor(comp)

	defer sw.Close()

	cam1Relay := newTestRelay()
	cam2Relay := newTestRelay()
	sw.RegisterSource("cam1", cam1Relay)
	sw.RegisterSource("cam2", cam2Relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 50, IsKeyframe: true, WireData: []byte{0x01}})
	require.NoError(t, sw.SetPreview(context.Background(), "cam2"))

	// Start transition
	require.NoError(t, sw.StartTransition(context.Background(), "cam2", "mix", 60000, ""))

	// Feed frames from both sources
	cam1Relay.BroadcastVideo(&media.VideoFrame{PTS: 100, IsKeyframe: true, WireData: []byte{0x01}})
	cam2Relay.BroadcastVideo(&media.VideoFrame{PTS: 101, IsKeyframe: true, WireData: []byte{0x02}})

	time.Sleep(20 * time.Millisecond)

	// The transition engine decoded both sources internally (2 decoders).
	// The pipeline coordinator should encode exactly once (not 2 or 3 times).
	// encodeCount tracks encoder FACTORY calls, not Encode() calls.
	require.Equal(t, 1, encodeCount, "should create only one encoder for the pipeline")
}
