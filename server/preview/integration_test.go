package preview

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestMultipleEncoders_Independent(t *testing.T) {
	skipWithoutEncoder(t)
	const nSources = 4
	relays := make([]*mockRelay, nSources)
	encoders := make([]*Encoder, nSources)

	for i := range nSources {
		relays[i] = &mockRelay{}
		enc, err := NewEncoder(Config{
			SourceKey: fmt.Sprintf("source-%d", i),
			Width:     426,
			Height:    240,
			Bitrate:   200_000,
			FPSNum:    30,
			FPSDen:    1,
			Relay:     relays[i],
		})
		if err != nil {
			t.Fatalf("encoder %d: %v", i, err)
		}
		encoders[i] = enc
	}

	// Send frames to all encoders concurrently.
	srcW, srcH := 640, 480
	yuv := make([]byte, srcW*srcH*3/2)

	var wg sync.WaitGroup
	for i := range nSources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range 30 {
				encoders[i].Send(yuv, srcW, srcH, int64(f)*3000)
				time.Sleep(time.Millisecond)
			}
		}()
	}
	wg.Wait()

	// Wait for encoders to drain.
	time.Sleep(2 * time.Second)

	for i := range nSources {
		encoders[i].Stop()
		frames := relays[i].getVideos()
		if len(frames) == 0 {
			t.Errorf("encoder %d: no frames produced", i)
		}
		t.Logf("encoder %d: %d frames", i, len(frames))
	}
}
