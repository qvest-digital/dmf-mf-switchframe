//go:build cgo && !noffmpeg

package srt

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestStreamDecoderWithTSFile(t *testing.T) {
	t.Parallel()

	// Locate test clip relative to srt package: ../../test/clips/
	clipsDir := filepath.Join("..", "..", "test", "clips")
	path := filepath.Join(clipsDir, "tears_of_steel.ts")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skip("test clip not available:", path)
	}

	// Read first 500KB to simulate a short stream segment.
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("failed to open test clip: %v", err)
	}
	defer f.Close()

	buf := make([]byte, 500*1024)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF {
		t.Fatalf("failed to read test clip: %v", err)
	}
	buf = buf[:n]

	var videoFrames atomic.Int64
	var audioFrames atomic.Int64
	var lastWidth, lastHeight atomic.Int32

	cfg := StreamDecoderConfig{
		Reader:     bytes.NewReader(buf),
		MaxThreads: 2,
		OnVideo: func(yuv []byte, width, height int, pts int64) {
			videoFrames.Add(1)
			lastWidth.Store(int32(width))
			lastHeight.Store(int32(height))

			// Verify YUV420 buffer size: w * h * 3 / 2
			expected := width * height * 3 / 2
			if len(yuv) != expected {
				t.Errorf("YUV buffer size mismatch: got %d, want %d (w=%d h=%d)",
					len(yuv), expected, width, height)
			}
		},
		OnAudio: func(pcm []float32, pts int64, sampleRate, channels int) {
			audioFrames.Add(1)

			if sampleRate != 48000 {
				t.Errorf("expected sampleRate=48000, got %d", sampleRate)
			}
			if channels != 2 {
				t.Errorf("expected channels=2, got %d", channels)
			}
			if len(pcm) == 0 {
				t.Error("expected non-empty PCM data")
			}
		},
	}

	dec, err := NewStreamDecoder(cfg)
	if err != nil {
		t.Fatalf("NewStreamDecoder failed: %v", err)
	}

	done := make(chan struct{})
	go func() {
		dec.Run()
		close(done)
	}()

	select {
	case <-done:
		// Run completed (EOF on bytes.Reader)
	case <-time.After(10 * time.Second):
		dec.Stop()
		t.Fatal("StreamDecoder.Run did not complete within 10 seconds")
	}

	vf := videoFrames.Load()
	af := audioFrames.Load()
	w := lastWidth.Load()
	h := lastHeight.Load()

	t.Logf("decoded %d video frames, %d audio frames, resolution %dx%d", vf, af, w, h)

	if vf == 0 {
		t.Error("expected at least one video frame")
	}
	if af == 0 {
		t.Error("expected at least one audio frame")
	}
	if w <= 0 || h <= 0 {
		t.Errorf("expected positive resolution, got %dx%d", w, h)
	}
}

func TestStreamDecoderStop(t *testing.T) {
	t.Parallel()

	// Load real TS data for format probing, then stall.
	clipsDir := filepath.Join("..", "..", "test", "clips")
	path := filepath.Join(clipsDir, "tears_of_steel.ts")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skip("test clip not available:", path)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("failed to open test clip: %v", err)
	}
	defer f.Close()

	// Read enough data for format probing (~200KB), then stall.
	probeBuf := make([]byte, 200*1024)
	n, err := io.ReadFull(f, probeBuf)
	if err != nil && err != io.ErrUnexpectedEOF {
		t.Fatalf("failed to read probe data: %v", err)
	}
	probeBuf = probeBuf[:n]

	// stallReader provides real TS data initially, then blocks on a channel.
	// This allows avformat_open_input to succeed, while Run() blocks on reads.
	stallCh := make(chan struct{})
	reader := &stallReader{
		data:    probeBuf,
		stallCh: stallCh,
	}

	cfg := StreamDecoderConfig{
		Reader:     reader,
		MaxThreads: 1,
		OnVideo:    func(yuv []byte, width, height int, pts int64) {},
		OnAudio:    func(pcm []float32, pts int64, sampleRate, channels int) {},
	}

	dec, err := NewStreamDecoder(cfg)
	if err != nil {
		t.Fatalf("NewStreamDecoder failed: %v", err)
	}

	done := make(chan struct{})
	go func() {
		dec.Run()
		close(done)
	}()

	// Give Run a moment to start processing, then stop.
	time.Sleep(100 * time.Millisecond)
	dec.Stop()
	close(stallCh) // unblock any pending reads

	select {
	case <-done:
		// Good: Run returned after Stop
	case <-time.After(5 * time.Second):
		t.Fatal("StreamDecoder.Run did not return within 5 seconds after Stop")
	}
}

// stallReader provides initial data, then blocks until stallCh is closed.
type stallReader struct {
	data    []byte
	pos     int
	stallCh chan struct{}
	stalled bool
}

func (r *stallReader) Read(p []byte) (int, error) {
	if r.pos < len(r.data) {
		n := copy(p, r.data[r.pos:])
		r.pos += n
		return n, nil
	}
	// Data exhausted — block until stallCh is closed.
	if !r.stalled {
		r.stalled = true
	}
	<-r.stallCh
	return 0, io.EOF
}

func TestStreamDecoderNilReader(t *testing.T) {
	t.Parallel()

	cfg := StreamDecoderConfig{
		Reader:  nil,
		OnVideo: func(yuv []byte, width, height int, pts int64) {},
		OnAudio: func(pcm []float32, pts int64, sampleRate, channels int) {},
	}

	_, err := NewStreamDecoder(cfg)
	if err == nil {
		t.Fatal("expected error for nil Reader")
	}
}

func TestStreamDecoderNilCallbacks(t *testing.T) {
	t.Parallel()

	cfg := StreamDecoderConfig{
		Reader: bytes.NewReader([]byte{}),
	}

	_, err := NewStreamDecoder(cfg)
	if err == nil {
		t.Fatal("expected error for nil OnVideo callback")
	}
}
