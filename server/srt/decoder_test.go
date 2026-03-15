//go:build cgo && !noffmpeg

package srt

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sync"
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

func TestStreamDecoderRWMutexConcurrentLookup(t *testing.T) {
	t.Parallel()

	// Verify that lookupDecoder uses RLock (concurrent reads don't block each other).
	// Register a dummy decoder, then hammer lookupDecoder from multiple goroutines.
	dummy := &StreamDecoder{}
	id := registerDecoder(dummy)
	defer unregisterDecoder(id)

	const goroutines = 8
	const iterations = 1000
	var ready sync.WaitGroup
	ready.Add(goroutines)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			ready.Done()
			<-start
			for j := 0; j < iterations; j++ {
				d := lookupDecoder(id)
				if d != dummy {
					t.Errorf("lookupDecoder returned wrong decoder")
					return
				}
			}
		}()
	}

	ready.Wait()
	close(start)
	wg.Wait()
}

func TestStreamDecoderNilCaptionSCTE35Callbacks(t *testing.T) {
	t.Parallel()

	// Decode a stream with nil OnCaptions and OnSCTE35 callbacks.
	// This must not crash even if the stream contains caption/SCTE-35 data.
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

	buf := make([]byte, 500*1024)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF {
		t.Fatalf("failed to read test clip: %v", err)
	}
	buf = buf[:n]

	cfg := StreamDecoderConfig{
		Reader:     bytes.NewReader(buf),
		MaxThreads: 2,
		OnVideo:    func(yuv []byte, width, height int, pts int64) {},
		OnAudio:    func(pcm []float32, pts int64, sampleRate, channels int) {},
		OnCaptions: nil, // explicitly nil
		OnSCTE35:   nil, // explicitly nil
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
		// Completed without crash — success
	case <-time.After(10 * time.Second):
		dec.Stop()
		t.Fatal("StreamDecoder.Run did not complete within 10 seconds")
	}
}

func TestStreamDecoderCaptionSCTE35CallbacksInvoked(t *testing.T) {
	t.Parallel()

	// Verify that caption and SCTE-35 callbacks are wired and don't crash
	// when set. Our test clips may not contain caption/SCTE-35 data, so we
	// just verify that:
	// 1. The decoder runs to completion without errors
	// 2. The callbacks are properly set (non-nil doesn't crash the code path)
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

	buf := make([]byte, 500*1024)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF {
		t.Fatalf("failed to read test clip: %v", err)
	}
	buf = buf[:n]

	var captionCalls atomic.Int64
	var scte35Calls atomic.Int64
	var videoFrames atomic.Int64

	cfg := StreamDecoderConfig{
		Reader:     bytes.NewReader(buf),
		MaxThreads: 2,
		OnVideo: func(yuv []byte, width, height int, pts int64) {
			videoFrames.Add(1)
		},
		OnAudio: func(pcm []float32, pts int64, sampleRate, channels int) {},
		OnCaptions: func(data []byte, pts int64) {
			captionCalls.Add(1)
			if len(data) == 0 {
				t.Error("caption callback received empty data")
			}
		},
		OnSCTE35: func(data []byte, pts int64) {
			scte35Calls.Add(1)
			if len(data) == 0 {
				t.Error("SCTE-35 callback received empty data")
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
	case <-time.After(10 * time.Second):
		dec.Stop()
		t.Fatal("StreamDecoder.Run did not complete within 10 seconds")
	}

	vf := videoFrames.Load()
	cc := captionCalls.Load()
	sc := scte35Calls.Load()
	t.Logf("video frames=%d, caption callbacks=%d, SCTE-35 callbacks=%d", vf, cc, sc)

	// Video must have decoded successfully.
	if vf == 0 {
		t.Error("expected at least one video frame")
	}
	// Caption/SCTE-35 data presence depends on the test clip content.
	// We just log the counts — the important thing is no crash.
}

func TestStreamDecoderMultiClipResolutionReuse(t *testing.T) {
	t.Parallel()

	// Decode two clips with different resolutions sequentially using
	// separate decoders. This exercises the buffer reallocation path
	// (video_buf realloc on resolution change) indirectly by ensuring
	// different resolutions decode correctly.
	clipsDir := filepath.Join("..", "..", "test", "clips")

	clips := []struct {
		name string
		path string
	}{
		{"tears_of_steel", filepath.Join(clipsDir, "tears_of_steel.ts")},
		{"bbb", filepath.Join(clipsDir, "bbb.ts")},
	}

	for _, clip := range clips {
		clip := clip
		t.Run(clip.name, func(t *testing.T) {
			if _, err := os.Stat(clip.path); os.IsNotExist(err) {
				t.Skip("test clip not available:", clip.path)
			}

			f, err := os.Open(clip.path)
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
			var lastWidth, lastHeight atomic.Int32

			cfg := StreamDecoderConfig{
				Reader:     bytes.NewReader(buf),
				MaxThreads: 2,
				OnVideo: func(yuv []byte, width, height int, pts int64) {
					videoFrames.Add(1)
					lastWidth.Store(int32(width))
					lastHeight.Store(int32(height))

					expected := width * height * 3 / 2
					if len(yuv) != expected {
						t.Errorf("YUV buffer size mismatch: got %d, want %d", len(yuv), expected)
					}
				},
				OnAudio: func(pcm []float32, pts int64, sampleRate, channels int) {},
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
			case <-time.After(10 * time.Second):
				dec.Stop()
				t.Fatal("StreamDecoder.Run did not complete within 10 seconds")
			}

			vf := videoFrames.Load()
			w := lastWidth.Load()
			h := lastHeight.Load()
			t.Logf("clip=%s: %d video frames, resolution %dx%d", clip.name, vf, w, h)

			if vf == 0 {
				t.Error("expected at least one video frame")
			}
		})
	}
}

func TestStreamDecoderZeroByteRead(t *testing.T) {
	t.Parallel()

	// A reader that returns (0, nil) a few times before providing real data,
	// then EOF. This tests the zero-byte read handling in goSRTRead.
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

	buf := make([]byte, 500*1024)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF {
		t.Fatalf("failed to read test clip: %v", err)
	}
	buf = buf[:n]

	reader := &zeroByteReader{inner: bytes.NewReader(buf), zeroEvery: 10}

	var videoFrames atomic.Int64

	cfg := StreamDecoderConfig{
		Reader:     reader,
		MaxThreads: 2,
		OnVideo: func(yuv []byte, width, height int, pts int64) {
			videoFrames.Add(1)
		},
		OnAudio: func(pcm []float32, pts int64, sampleRate, channels int) {},
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
	case <-time.After(10 * time.Second):
		dec.Stop()
		t.Fatal("StreamDecoder.Run did not complete within 10 seconds")
	}

	vf := videoFrames.Load()
	t.Logf("decoded %d video frames with zero-byte reader", vf)

	if vf == 0 {
		t.Error("expected at least one video frame despite zero-byte reads")
	}
}

// zeroByteReader wraps a reader and returns (0, nil) every N reads.
type zeroByteReader struct {
	inner     io.Reader
	zeroEvery int
	count     int
}

func (r *zeroByteReader) Read(p []byte) (int, error) {
	r.count++
	if r.count%r.zeroEvery == 0 {
		return 0, nil
	}
	return r.inner.Read(p)
}
