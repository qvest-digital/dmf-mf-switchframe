package ingest

import (
	"bytes"
	"context"
	"io"
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
	// Empty reader should return nil (clean EOF, not an error)
	require.NoError(t, err)
	require.Empty(t, bc.videoFrames())
}

func TestStreamDemuxer_ContextCancellation(t *testing.T) {
	// A reader that blocks forever
	r, _ := io.Pipe()
	bc := &mockBroadcaster{}
	d := NewStreamDemuxer("test", r, bc)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err) // context cancellation is clean
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}
