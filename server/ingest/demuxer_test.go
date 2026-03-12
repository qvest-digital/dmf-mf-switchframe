package ingest

import (
	"bytes"
	"context"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

// mockBroadcaster records BroadcastVideo/BroadcastAudio calls.
type mockBroadcaster struct {
	mu    sync.Mutex
	video []*media.VideoFrame
	audio []*media.AudioFrame
}

func (m *mockBroadcaster) BroadcastVideo(f *media.VideoFrame) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.video = append(m.video, f)
}

func (m *mockBroadcaster) BroadcastAudio(f *media.AudioFrame) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audio = append(m.audio, f)
}

func (m *mockBroadcaster) videoFrames() []*media.VideoFrame {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]*media.VideoFrame, len(m.video))
	copy(cp, m.video)
	return cp
}

func (m *mockBroadcaster) audioFrames() []*media.AudioFrame {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]*media.AudioFrame, len(m.audio))
	copy(cp, m.audio)
	return cp
}

func TestStreamDemuxer_EmptyReader(t *testing.T) {
	bc := &mockBroadcaster{}
	d := NewStreamDemuxer("test", bytes.NewReader(nil), bc)
	err := d.Run(context.Background())
	require.NoError(t, err)
	require.Empty(t, bc.videoFrames())
}

func TestStreamDemuxer_ContextCancellation(t *testing.T) {
	r, _ := io.Pipe()
	bc := &mockBroadcaster{}
	d := NewStreamDemuxer("test", r, bc)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

func TestStreamDemuxer_RealTS(t *testing.T) {
	f, err := os.Open("testdata/sample.ts")
	if err != nil {
		t.Skip("testdata/sample.ts not available")
	}
	defer f.Close()

	bc := &mockBroadcaster{}
	d := NewStreamDemuxer("test", f, bc)
	err = d.Run(context.Background())
	require.NoError(t, err)

	vf := bc.videoFrames()
	require.NotEmpty(t, vf, "expected video frames")

	// First video frame should be a keyframe with SPS/PPS and AVC1 WireData.
	require.True(t, vf[0].IsKeyframe, "first frame should be a keyframe")
	require.NotEmpty(t, vf[0].SPS, "keyframe should have SPS")
	require.NotEmpty(t, vf[0].PPS, "keyframe should have PPS")
	require.NotEmpty(t, vf[0].WireData, "frame should have AVC1 WireData")
	require.Equal(t, "h264", vf[0].Codec)

	// Audio frames should have sample rate and channels parsed from ADTS.
	af := bc.audioFrames()
	require.NotEmpty(t, af, "expected audio frames")
	require.Greater(t, af[0].SampleRate, 0, "sample rate should be parsed from ADTS")
	require.Greater(t, af[0].Channels, 0, "channels should be parsed from ADTS")
}
