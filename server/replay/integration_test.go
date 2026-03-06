package replay

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

// TestIntegration_FullReplayWorkflow tests the complete mark-in → mark-out → play → stop cycle.
func TestIntegration_FullReplayWorkflow(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, Config{BufferDurationSecs: 10}, mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	_ = m.AddSource("cam1")

	// Simulate a live feed: record frames into the buffer via the viewer.
	v := m.Viewer("cam1")
	require.NotNil(t, v, "expected viewer for cam1")

	// Send a few GOPs through the viewer.
	pts := int64(0)
	for gop := 0; gop < 3; gop++ {
		for f := 0; f < 10; f++ {
			frame := &media.VideoFrame{
				PTS:        pts,
				IsKeyframe: f == 0,
				WireData:   makeAVC1Data(200),
				Codec:      "avc1.42C01E",
			}
			if f == 0 {
				frame.SPS = []byte{0x67, 0x42, 0xC0, 0x1E}
				frame.PPS = []byte{0x68, 0xCE, 0x38, 0x80}
			}
			v.SendVideo(frame)
			pts += 3003
			time.Sleep(1 * time.Millisecond) // Spread wall-clock times.
		}
	}

	// Mark in/out spanning the middle GOP.
	require.NoError(t, m.MarkIn("cam1"), "MarkIn")
	time.Sleep(5 * time.Millisecond)

	// Record a few more frames after mark-in.
	for f := 0; f < 10; f++ {
		frame := &media.VideoFrame{
			PTS:        pts,
			IsKeyframe: f == 0,
			WireData:   makeAVC1Data(200),
			Codec:      "avc1.42C01E",
		}
		if f == 0 {
			frame.SPS = []byte{0x67, 0x42, 0xC0, 0x1E}
			frame.PPS = []byte{0x68, 0xCE, 0x38, 0x80}
		}
		v.SendVideo(frame)
		pts += 3003
		time.Sleep(1 * time.Millisecond)
	}

	require.NoError(t, m.MarkOut("cam1"), "MarkOut")

	// Play at 1x.
	require.NoError(t, m.Play("cam1", 1.0, false), "Play")

	// Wait for playback to complete.
	require.Eventually(t, func() bool {
		return m.Status().State == PlayerIdle
	}, 10*time.Second, 50*time.Millisecond, "playback did not complete within timeout")

	// Verify frames were output to the relay.
	relay.mu.Lock()
	videoCount := len(relay.videos)
	relay.mu.Unlock()

	require.Greater(t, videoCount, 0, "expected output frames on replay relay")
	t.Logf("replay output %d frames to relay", videoCount)
}

// TestIntegration_MultiSourceBuffering tests buffering from multiple sources simultaneously.
func TestIntegration_MultiSourceBuffering(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, Config{BufferDurationSecs: 30}, mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	sources := []string{"cam1", "cam2", "cam3"}
	for _, s := range sources {
		_ = m.AddSource(s)
	}

	// Record frames to all sources concurrently.
	var wg sync.WaitGroup
	for _, s := range sources {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			pts := int64(0)
			for i := 0; i < 30; i++ {
				m.RecordFrame(key, &media.VideoFrame{
					PTS:        pts,
					IsKeyframe: i%10 == 0,
					WireData:   makeAVC1Data(100),
					SPS:        []byte{0x67, 0x42, 0xC0, 0x1E},
					PPS:        []byte{0x68, 0xCE, 0x38, 0x80},
				})
				pts += 3003
			}
		}(s)
	}
	wg.Wait()

	status := m.Status()
	require.Len(t, status.Buffers, 3, "expected 3 source buffers")
	for _, buf := range status.Buffers {
		require.Greater(t, buf.FrameCount, 0, "source %s has 0 frames", buf.Source)
	}
}
