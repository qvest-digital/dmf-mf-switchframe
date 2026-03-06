package replay

import (
	"sync"
	"testing"
	"time"

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
	if v == nil {
		t.Fatal("expected viewer for cam1")
	}

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
	if err := m.MarkIn("cam1"); err != nil {
		t.Fatalf("MarkIn: %v", err)
	}
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

	if err := m.MarkOut("cam1"); err != nil {
		t.Fatalf("MarkOut: %v", err)
	}

	// Play at 1x.
	if err := m.Play("cam1", 1.0, false); err != nil {
		t.Fatalf("Play: %v", err)
	}

	// Wait for playback to complete.
	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("playback did not complete within timeout")
		default:
			status := m.Status()
			if status.State == PlayerIdle {
				goto done
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
done:

	// Verify frames were output to the relay.
	relay.mu.Lock()
	videoCount := len(relay.videos)
	relay.mu.Unlock()

	if videoCount == 0 {
		t.Error("expected output frames on replay relay")
	}
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
	if len(status.Buffers) != 3 {
		t.Errorf("expected 3 source buffers, got %d", len(status.Buffers))
	}
	for _, buf := range status.Buffers {
		if buf.FrameCount == 0 {
			t.Errorf("source %s has 0 frames", buf.Source)
		}
	}
}
