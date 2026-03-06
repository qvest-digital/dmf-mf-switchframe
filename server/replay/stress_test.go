package replay

import (
	"sync"
	"testing"
	"time"

	"github.com/zsiec/prism/media"
)

// TestStress_ConcurrentRecordAndExtract tests concurrent frame recording and clip extraction.
func TestStress_ConcurrentRecordAndExtract(t *testing.T) {
	buf := newReplayBuffer(10, 0)
	const nWriters = 4
	const framesPerWriter = 500

	var wg sync.WaitGroup
	for w := 0; w < nWriters; w++ {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			for i := 0; i < framesPerWriter; i++ {
				pts := int64(offset*framesPerWriter+i) * 3003
				keyframe := i%30 == 0
				f := &media.VideoFrame{
					PTS:        pts,
					IsKeyframe: keyframe,
					WireData:   makeAVC1Data(100),
				}
				if keyframe {
					f.SPS = []byte{0x67, 0x42, 0xC0, 0x1E}
					f.PPS = []byte{0x68, 0xCE, 0x38, 0x80}
				}
				buf.RecordFrame(f)
			}
		}(w)
	}

	// Concurrent readers.
	extractDone := make(chan struct{})
	go func() {
		defer close(extractDone)
		now := time.Now()
		for i := 0; i < 100; i++ {
			_, _ = buf.ExtractClip(now.Add(-10*time.Second), now.Add(10*time.Second))
			_ = buf.Status()
		}
	}()

	wg.Wait()
	<-extractDone
}

// TestStress_RapidMarkAndPlay tests rapid mark-in/mark-out/play/stop cycles.
func TestStress_RapidMarkAndPlay(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, Config{BufferDurationSecs: 5}, mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	_ = m.AddSource("cam1")

	// Pre-fill buffer.
	pts := int64(0)
	for i := 0; i < 60; i++ {
		f := &media.VideoFrame{
			PTS:        pts,
			IsKeyframe: i%10 == 0,
			WireData:   makeAVC1Data(100),
		}
		if i%10 == 0 {
			f.SPS = []byte{0x67, 0x42, 0xC0, 0x1E}
			f.PPS = []byte{0x68, 0xCE, 0x38, 0x80}
		}
		m.RecordFrame("cam1", f)
		pts += 3003
		time.Sleep(1 * time.Millisecond)
	}

	for cycle := 0; cycle < 5; cycle++ {
		_ = m.MarkIn("cam1")
		time.Sleep(5 * time.Millisecond)

		// Record a few more frames.
		for i := 0; i < 5; i++ {
			m.RecordFrame("cam1", &media.VideoFrame{
				PTS:        pts,
				IsKeyframe: i == 0,
				WireData:   makeAVC1Data(100),
				SPS:        []byte{0x67, 0x42, 0xC0, 0x1E},
				PPS:        []byte{0x68, 0xCE, 0x38, 0x80},
			})
			pts += 3003
			time.Sleep(1 * time.Millisecond)
		}

		_ = m.MarkOut("cam1")
		err := m.Play("cam1", 1.0, false)
		if err != nil {
			continue // Might be ErrEmptyClip if timing is tight.
		}

		// Wait briefly then stop.
		time.Sleep(100 * time.Millisecond)
		_ = m.Stop()
		time.Sleep(50 * time.Millisecond)
	}
}
