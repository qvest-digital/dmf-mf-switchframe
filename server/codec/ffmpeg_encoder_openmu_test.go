//go:build cgo && !noffmpeg

package codec

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestNewFFmpegPreviewEncoder_HoldsFFmpegOpenMu verifies that the preview
// encoder constructor serializes its avcodec_open2 call through FFmpegOpenMu.
// NVENC initializes the CUDA runtime inside avcodec_open2, and concurrent
// FFmpeg opens SIGSEGV the CUDA driver — a mutex only protects when every
// open site holds it, so the constructor must block while another open is
// in flight.
func TestNewFFmpegPreviewEncoder_HoldsFFmpegOpenMu(t *testing.T) {
	FFmpegOpenMu.Lock()

	created := make(chan error, 1)
	go func() {
		enc, err := NewFFmpegPreviewEncoder(64, 64, 300_000, 30, 1, 2)
		if enc != nil {
			enc.Close()
		}
		created <- err
	}()

	// While FFmpegOpenMu is held by this test, the constructor must not be
	// able to complete its open.
	select {
	case <-created:
		FFmpegOpenMu.Unlock()
		t.Fatal("NewFFmpegPreviewEncoder completed while FFmpegOpenMu was held — avcodec_open2 ran outside the open lock")
	case <-time.After(200 * time.Millisecond):
	}

	FFmpegOpenMu.Unlock()

	select {
	case err := <-created:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("NewFFmpegPreviewEncoder did not complete after FFmpegOpenMu was released")
	}
}
