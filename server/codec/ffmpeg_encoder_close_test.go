//go:build cgo && !noffmpeg

package codec

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFFmpegEncoder_ConcurrentClose verifies that calling Close() from
// multiple goroutines simultaneously does not panic, race, or corrupt
// state. The mutex guard in Close() should serialize all calls.
func TestFFmpegEncoder_ConcurrentClose(t *testing.T) {
	t.Parallel()

	enc, err := NewFFmpegEncoder("libx264", 256, 256, 1_000_000, 30, 1, 2, nil)
	if err != nil {
		t.Skipf("libx264 not available: %v", err)
	}

	// Close from 10 goroutines simultaneously.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			enc.Close()
		}()
	}
	wg.Wait()
	// If we get here without panic or race detector complaint, the mutex guard works.
}

// TestFFmpegEncoder_EncodeAfterClose verifies that encoding after Close
// does not panic. The encoder should be safely guarded.
func TestFFmpegEncoder_EncodeAfterClose(t *testing.T) {
	t.Parallel()

	enc, err := NewFFmpegEncoder("libx264", 256, 256, 1_000_000, 30, 1, 2, nil)
	if err != nil {
		t.Skipf("libx264 not available: %v", err)
	}

	// Encode one frame successfully first.
	yuv := make([]byte, 256*256*3/2)
	for i := range yuv {
		yuv[i] = 128
	}
	_, _, err = enc.Encode(yuv, 0, true)
	require.NoError(t, err)

	enc.Close()

	// Encode after close should not panic. It may return an error — that's fine.
	// The key property is no segfault or undefined behavior.
	_, _, _ = enc.Encode(yuv, 3000, false)
}

// TestFFmpegDecoder_ConcurrentClose verifies that calling Close() from
// multiple goroutines simultaneously does not panic or race.
func TestFFmpegDecoder_ConcurrentClose(t *testing.T) {
	t.Parallel()

	dec, err := NewFFmpegDecoder(nil)
	if err != nil {
		t.Skipf("FFmpeg decoder not available: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			dec.Close()
		}()
	}
	wg.Wait()
}
