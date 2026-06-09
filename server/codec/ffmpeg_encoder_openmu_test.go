//go:build cgo && !noffmpeg

package codec

import (
	"path/filepath"
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
// TestTranscodeFile_HoldsFFmpegOpenMu verifies that the file transcode path
// serializes its open phase (avformat_open_input plus four avcodec_open2
// calls, including h264_nvenc which initializes CUDA) through FFmpegOpenMu.
// A mutex only protects when every open site holds it, so the transcode must
// block while another open is in flight — but only for the open phase, not
// for the long-running frame loop.
func TestTranscodeFile_HoldsFFmpegOpenMu(t *testing.T) {
	dir := t.TempDir()
	// Create the input before taking the lock: the test encoder used to
	// generate it holds FFmpegOpenMu itself and would deadlock otherwise.
	inputPath := createTestTSInputFile(t, dir, 64, 64, 30)
	outputPath := filepath.Join(dir, "output.ts")

	FFmpegOpenMu.Lock()

	done := make(chan error, 1)
	go func() {
		_, err := TranscodeFile(inputPath, outputPath, "libx264", 500000)
		done <- err
	}()

	// While FFmpegOpenMu is held by this test, the transcode must not be able
	// to get past its open phase.
	select {
	case <-done:
		FFmpegOpenMu.Unlock()
		t.Fatal("TranscodeFile completed while FFmpegOpenMu was held — its opens ran outside the open lock")
	case <-time.After(200 * time.Millisecond):
	}

	FFmpegOpenMu.Unlock()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("TranscodeFile did not complete after FFmpegOpenMu was released")
	}

	// The frame loop must run with the lock released: if the transcode held
	// FFmpegOpenMu for its full duration, this immediate re-acquire after
	// completion would be the least of it — concurrent opens would have been
	// blocked for the entire transcode. Sanity-check the lock is free now.
	if !FFmpegOpenMu.TryLock() {
		t.Fatal("FFmpegOpenMu still held after TranscodeFile returned")
	}
	FFmpegOpenMu.Unlock()
}

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
