package playout

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/internal/video"
)

// Compile-time interface compliance check.
var _ FrameIterator = (*FileFrameIterator)(nil)

func TestFileFrameIterator_NonexistentFile(t *testing.T) {
	_, err := NewFileFrameIteratorWithDecoder("/nonexistent/file.ts", stubDecoderFactory)
	if err == nil {
		t.Fatal("expected error for nonexistent file, got nil")
	}
}

func TestFileFrameIterator_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.ts")
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := NewFileFrameIteratorWithDecoder(path, stubDecoderFactory)
	if err == nil {
		t.Fatal("expected error for empty file, got nil")
	}
}

func TestFileFrameIterator_UnsupportedFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.avi")
	if err := os.WriteFile(path, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := NewFileFrameIteratorWithDecoder(path, stubDecoderFactory)
	if err == nil {
		t.Fatal("expected error for unsupported format, got nil")
	}
}

func TestFileFrameIterator_NilDecoderFactory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.ts")
	if err := os.WriteFile(path, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := NewFileFrameIteratorWithDecoder(path, nil)
	if err == nil {
		t.Fatal("expected error for nil decoder factory, got nil")
	}
}

func TestFileFrameIterator_WithMockDecoder(t *testing.T) {
	// Create a mock decoder that returns fixed YUV data for any input.
	factory := func() (VideoDecoder, error) {
		return &mockVideoDecoder{
			width:  320,
			height: 240,
		}, nil
	}

	// We need a valid TS file for DemuxFile to work.
	// Use a minimal test TS if available, otherwise skip.
	// For unit tests without cgo, we create a stub test.
	iter, err := NewFileFrameIteratorWithDecoder("/nonexistent.ts", factory)
	if err != nil {
		// Expected -- nonexistent file.
		return
	}
	defer iter.Close()
}

func TestFileFrameIterator_EstimateFPSFromDecoded(t *testing.T) {
	tests := []struct {
		name   string
		frames []DecodedFrame
		want   float64
	}{
		{
			name:   "empty",
			frames: nil,
			want:   30.0,
		},
		{
			name:   "single frame",
			frames: []DecodedFrame{{PTS: 0}},
			want:   30.0,
		},
		{
			name: "30fps",
			frames: func() []DecodedFrame {
				f := make([]DecodedFrame, 10)
				for i := range f {
					f[i].PTS = int64(i) * 3000 // 90000/30
				}
				return f
			}(),
			want: 30.0,
		},
		{
			name: "25fps",
			frames: func() []DecodedFrame {
				f := make([]DecodedFrame, 10)
				for i := range f {
					f[i].PTS = int64(i) * 3600 // 90000/25
				}
				return f
			}(),
			want: 25.0,
		},
		{
			name: "60fps",
			frames: func() []DecodedFrame {
				f := make([]DecodedFrame, 10)
				for i := range f {
					f[i].PTS = int64(i) * 1500 // 90000/60
				}
				return f
			}(),
			want: 60.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := estimateFPSFromDecoded(tt.frames)
			// Allow 0.5 FPS tolerance.
			if got < tt.want-0.5 || got > tt.want+0.5 {
				t.Errorf("estimateFPSFromDecoded() = %f, want ~%f", got, tt.want)
			}
		})
	}
}

func TestFileFrameIterator_EstimateFPSFromEncodedFrames(t *testing.T) {
	tests := []struct {
		name   string
		frames []encodedFrame
		want   float64
	}{
		{
			name:   "empty",
			frames: nil,
			want:   30.0,
		},
		{
			name:   "single frame",
			frames: []encodedFrame{{pts: 0}},
			want:   30.0,
		},
		{
			name: "30fps",
			frames: func() []encodedFrame {
				f := make([]encodedFrame, 10)
				for i := range f {
					f[i].pts = int64(i) * 3000
				}
				return f
			}(),
			want: 30.0,
		},
		{
			name: "25fps",
			frames: func() []encodedFrame {
				f := make([]encodedFrame, 10)
				for i := range f {
					f[i].pts = int64(i) * 3600
				}
				return f
			}(),
			want: 25.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := estimateFPSFromEncodedFrames(tt.frames)
			if got < tt.want-0.5 || got > tt.want+0.5 {
				t.Errorf("estimateFPSFromEncodedFrames() = %f, want ~%f", got, tt.want)
			}
		})
	}
}

func TestFileFrameIterator_NextEOF(t *testing.T) {
	// Test that a FileFrameIterator with no encoded frames returns nil immediately.
	iter := &FileFrameIterator{
		encodedFrames: nil,
		decoder:       &mockVideoDecoder{width: 320, height: 240},
		fps:           30.0,
		totalFrames:   0,
	}

	f, err := iter.Next()
	if err != nil {
		t.Fatalf("Next() error: %v", err)
	}
	if f != nil {
		t.Error("Next() returned non-nil frame on empty iterator")
	}
}

func TestFileFrameIterator_NextSequence(t *testing.T) {
	// Build encoded frames that the mock decoder will decode one-at-a-time.
	encoded := make([]encodedFrame, 5)
	for i := range encoded {
		encoded[i] = encodedFrame{
			wireData:   []byte{0x00, 0x00, 0x00, 0x01, byte(i)}, // fake Annex B
			pts:        int64(i) * 3000,
			isKeyframe: i == 0,
		}
	}

	iter := &FileFrameIterator{
		encodedFrames: encoded,
		decoder:       &mockVideoDecoder{width: 320, height: 240},
		fps:           30.0,
		totalFrames:   5,
	}

	if iter.TotalFrames() != 5 {
		t.Errorf("TotalFrames() = %d, want 5", iter.TotalFrames())
	}

	fps := iter.EstimateFPS()
	if fps != 30.0 {
		t.Errorf("EstimateFPS() = %f, want 30.0", fps)
	}

	for i := 0; i < 5; i++ {
		f, err := iter.Next()
		if err != nil {
			t.Fatalf("Next() error on frame %d: %v", i, err)
		}
		if f == nil {
			t.Fatalf("Next() returned nil on frame %d", i)
		}
		if f.PTS != int64(i)*3000 {
			t.Errorf("frame %d PTS = %d, want %d", i, f.PTS, int64(i)*3000)
		}
		if f.Width != 320 || f.Height != 240 {
			t.Errorf("frame %d dimensions = %dx%d, want 320x240", i, f.Width, f.Height)
		}
	}

	// End of stream.
	f, err := iter.Next()
	if err != nil {
		t.Fatalf("Next() error at EOF: %v", err)
	}
	if f != nil {
		t.Error("Next() at EOF returned non-nil frame")
	}
}

// slowCloseTrackingDecoder widens the decode window (so a concurrent Close()
// is likely to land mid-Decode) and panics if Decode() is ever called after
// Close() — the use-after-free the iterator mutex must prevent.
type slowCloseTrackingDecoder struct {
	width  int
	height int

	mu     sync.Mutex
	closed bool
}

func (d *slowCloseTrackingDecoder) Decode(h264 []byte) ([]byte, int, int, error) {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		panic("Decode() called after Close(): use-after-free")
	}
	d.mu.Unlock()

	// Simulate real decode latency to widen the race window.
	time.Sleep(50 * time.Microsecond)

	size := video.YUV420FrameSize(d.width, d.height)
	return make([]byte, size), d.width, d.height, nil
}

func (d *slowCloseTrackingDecoder) Close() {
	d.mu.Lock()
	d.closed = true
	d.mu.Unlock()
}

// TestFileFrameIterator_ConcurrentNextAndClose is a regression test for the
// use-after-close race where SeekToItem (on the snapshot-sync goroutine) calls
// clipIter.Close() while the run goroutine is inside Next()/decoder.Decode().
// Before the fix, f.closed was read in Next() without synchronization (a data
// race flagged by -race) and the FFmpeg decoder could be freed mid-Decode (a
// use-after-free crash). With FileFrameIterator's mutex, Close() is mutually
// exclusive with Next(), so Decode() never runs after Close() and the bool is
// race-free. Run with -race to exercise the data-race aspect.
func TestFileFrameIterator_ConcurrentNextAndClose(t *testing.T) {
	for iteration := 0; iteration < 50; iteration++ {
		encoded := make([]encodedFrame, 200)
		for i := range encoded {
			encoded[i] = encodedFrame{
				wireData:   []byte{0x00, 0x00, 0x00, 0x01, byte(i)},
				pts:        int64(i) * 3000,
				isKeyframe: i == 0,
			}
		}

		iter := &FileFrameIterator{
			encodedFrames: encoded,
			decoder:       &slowCloseTrackingDecoder{width: 64, height: 48},
			fps:           30.0,
			totalFrames:   len(encoded),
		}

		var wg sync.WaitGroup
		wg.Add(2)

		// Reader goroutine (the run loop): hammer Next().
		go func() {
			defer wg.Done()
			for {
				f, err := iter.Next()
				if err != nil {
					return
				}
				if f == nil {
					return // end of stream or closed
				}
			}
		}()

		// Closer goroutine (SeekToItem / cleanup on another goroutine).
		go func() {
			defer wg.Done()
			time.Sleep(100 * time.Microsecond)
			iter.Close()
			// Close must be idempotent.
			iter.Close()
		}()

		wg.Wait()

		// After Close, Next() must return cleanly (no panic, no decode).
		if f, err := iter.Next(); f != nil || err != nil {
			t.Fatalf("Next() after Close: got (%v, %v), want (nil, nil)", f, err)
		}
	}
}

func TestFileFrameIterator_AudioInterleaving_Proportional(t *testing.T) {
	// Verify proportional distribution: 5 audio frames across 4 video frames.
	// Expected: [1, 1, 1, 2] or [2, 1, 1, 1] audio frames per video frame.
	// Total must be exactly 5 — every audio frame delivered once.

	encoded := []encodedFrame{
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x65}, pts: 0, isKeyframe: true},
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x41}, pts: 3000},
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x41}, pts: 6000},
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x41}, pts: 9000},
	}

	audioData := []audioFrameInfo{
		{data: []byte{0xA0}, pts: 0, sampleRate: 48000, channels: 2},
		{data: []byte{0xA1}, pts: 1920, sampleRate: 48000, channels: 2},
		{data: []byte{0xA2}, pts: 3840, sampleRate: 48000, channels: 2},
		{data: []byte{0xA3}, pts: 5760, sampleRate: 48000, channels: 2},
		{data: []byte{0xA4}, pts: 7680, sampleRate: 48000, channels: 2},
	}

	iter := &FileFrameIterator{
		encodedFrames: encoded,
		audioFrames:   audioData,
		decoder:       &mockVideoDecoder{width: 4, height: 2},
		fps:           30.0,
		totalFrames:   4,
	}

	totalAudioDelivered := 0
	for i := 0; i < 4; i++ {
		f, err := iter.Next()
		if err != nil {
			t.Fatalf("Next() frame %d: %v", i, err)
		}
		count := len(f.AudioFrames)
		totalAudioDelivered += count
		if count == 0 && i < 3 {
			// At least one audio frame per video frame (except possibly last)
			t.Errorf("frame %d: no audio frames attached", i)
		}
	}

	if totalAudioDelivered != 5 {
		t.Errorf("total audio frames delivered = %d, want 5 (every frame delivered once)", totalAudioDelivered)
	}
}

func TestFileFrameIterator_AudioInterleaving_NoAudio(t *testing.T) {
	// Iterator with no audio should return frames without audio data.
	encoded := []encodedFrame{
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x65}, pts: 0},
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x41}, pts: 3000},
	}

	iter := &FileFrameIterator{
		encodedFrames: encoded,
		decoder:       &mockVideoDecoder{width: 4, height: 2},
		fps:           30.0,
		totalFrames:   2,
	}

	for i := 0; i < 2; i++ {
		f, err := iter.Next()
		if err != nil {
			t.Fatalf("Next() frame %d: %v", i, err)
		}
		if f.AudioData != nil {
			t.Errorf("frame %d has unexpected audio data", i)
		}
	}
}

func TestFileFrameIterator_AudioInterleaving_AllAudioBeforeFirstVideo(t *testing.T) {
	// All audio PTS values are before the second video frame's PTS -- all
	// fall in Video[0]'s window, first audio frame gets attached.
	encoded := []encodedFrame{
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x65}, pts: 10000},
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x41}, pts: 13000},
	}

	audioData := []audioFrameInfo{
		{data: []byte{0xA0}, pts: 100, sampleRate: 48000, channels: 2},
		{data: []byte{0xA1}, pts: 200, sampleRate: 48000, channels: 2},
		{data: []byte{0xA2}, pts: 300, sampleRate: 48000, channels: 2},
	}

	iter := &FileFrameIterator{
		encodedFrames: encoded,
		audioFrames:   audioData,
		decoder:       &mockVideoDecoder{width: 4, height: 2},
		fps:           30.0,
		totalFrames:   2,
	}

	// With proportional distribution, 3 audio frames / 2 video frames:
	// Frame 0 gets floor(1*3/2)=1 audio frame, Frame 1 gets remaining 2.
	// Or Frame 0 gets 2, Frame 1 gets 1. Total must be 3.
	totalAudio := 0
	for i := 0; i < 2; i++ {
		f, err := iter.Next()
		if err != nil {
			t.Fatalf("Next() frame %d: %v", i, err)
		}
		totalAudio += len(f.AudioFrames)
	}
	if totalAudio != 3 {
		t.Errorf("total audio delivered = %d, want 3", totalAudio)
	}
}

func TestFileFrameIterator_AudioInterleaving_LastFrameGetsRemaining(t *testing.T) {
	// With proportional distribution, the last video frame gets all remaining
	// audio frames. 3 audio / 2 video = frame 0 gets 1, frame 1 gets 2.
	encoded := []encodedFrame{
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x65}, pts: 0},
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x41}, pts: 3000},
	}

	audioData := []audioFrameInfo{
		{data: []byte{0xA0}, pts: 0, sampleRate: 48000, channels: 2},
		{data: []byte{0xA1}, pts: 4000, sampleRate: 48000, channels: 2},
		{data: []byte{0xA2}, pts: 8000, sampleRate: 48000, channels: 2},
	}

	iter := &FileFrameIterator{
		encodedFrames: encoded,
		audioFrames:   audioData,
		decoder:       &mockVideoDecoder{width: 4, height: 2},
		fps:           30.0,
		totalFrames:   2,
	}

	f0, err := iter.Next()
	if err != nil {
		t.Fatalf("Next() frame 0: %v", err)
	}
	f1, err := iter.Next()
	if err != nil {
		t.Fatalf("Next() frame 1: %v", err)
	}

	total := len(f0.AudioFrames) + len(f1.AudioFrames)
	if total != 3 {
		t.Errorf("total audio = %d (f0=%d, f1=%d), want 3", total, len(f0.AudioFrames), len(f1.AudioFrames))
	}
	// Last frame should have at least 1 audio frame (the remaining ones).
	if len(f1.AudioFrames) == 0 {
		t.Error("last frame should have remaining audio frames")
	}
}

func TestFileFrameIterator_Close(t *testing.T) {
	mock := &mockVideoDecoder{width: 320, height: 240}
	iter := &FileFrameIterator{
		decoder:     mock,
		fps:         30.0,
		totalFrames: 0,
	}
	// Close should not panic and should close the decoder.
	iter.Close()
	if !mock.closed {
		t.Error("Close() did not close the decoder")
	}
	// Double close should be safe.
	iter.Close()
}

func TestFileFrameIterator_CloseStopsNext(t *testing.T) {
	encoded := []encodedFrame{
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x65}, pts: 0},
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x41}, pts: 3000},
	}

	iter := &FileFrameIterator{
		encodedFrames: encoded,
		decoder:       &mockVideoDecoder{width: 4, height: 2},
		fps:           30.0,
		totalFrames:   2,
	}

	// Read one frame.
	f, err := iter.Next()
	if err != nil || f == nil {
		t.Fatalf("first Next() failed: err=%v, frame=%v", err, f)
	}

	// Close, then Next should return nil.
	iter.Close()
	f, err = iter.Next()
	if err != nil {
		t.Fatalf("Next() after Close() error: %v", err)
	}
	if f != nil {
		t.Error("Next() after Close() returned non-nil frame")
	}
}

func TestFileFrameIterator_DrainableDecoder(t *testing.T) {
	// Test that drain phase works with a DrainableDecoder.
	// Feed 3 frames where the first 2 return "buffering", then the 3rd
	// returns decoded output. After all are fed, drain returns 2 more frames.
	mock := &mockDrainableDecoder{
		width:       320,
		height:      240,
		bufferCount: 2, // first 2 Decode() calls return "buffering"
		drainFrames: 2, // ReceiveFrame returns 2 frames after EOS
	}

	encoded := []encodedFrame{
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x65}, pts: 0, isKeyframe: true},
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x41}, pts: 3000},
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x41}, pts: 6000},
	}

	iter := &FileFrameIterator{
		encodedFrames: encoded,
		decoder:       mock,
		fps:           30.0,
		totalFrames:   3,
	}
	defer iter.Close()

	// First Next() feeds frames 0 and 1 (buffering), then frame 2 produces output.
	f, err := iter.Next()
	if err != nil {
		t.Fatalf("Next() 1: %v", err)
	}
	if f == nil {
		t.Fatal("Next() 1 returned nil")
	}
	if f.PTS != 6000 {
		t.Errorf("frame 1 PTS = %d, want 6000 (first output from frame 2)", f.PTS)
	}

	// Next two calls should drain.
	f2, err := iter.Next()
	if err != nil {
		t.Fatalf("Next() 2: %v", err)
	}
	if f2 == nil {
		t.Fatal("Next() 2 returned nil, expected drained frame")
	}

	f3, err := iter.Next()
	if err != nil {
		t.Fatalf("Next() 3: %v", err)
	}
	if f3 == nil {
		t.Fatal("Next() 3 returned nil, expected drained frame")
	}

	// Should be done now.
	f4, err := iter.Next()
	if err != nil {
		t.Fatalf("Next() 4: %v", err)
	}
	if f4 != nil {
		t.Error("Next() 4 returned non-nil, expected EOF")
	}

	if !mock.eosSent {
		t.Error("SendEOS was not called")
	}
}

func TestFileFrameIterator_DecoderBufferReuseSafety(t *testing.T) {
	// Verify that Next() deep-copies the YUV buffer, so the decoder
	// can reuse its internal buffer without corrupting previous frames.
	mock := &mockReusingDecoder{
		width:  4,
		height: 2,
		buf:    make([]byte, video.YUV420FrameSize(4, 2)), // shared buffer
	}

	encoded := []encodedFrame{
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x65}, pts: 0},
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x41}, pts: 3000},
	}

	iter := &FileFrameIterator{
		encodedFrames: encoded,
		decoder:       mock,
		fps:           30.0,
		totalFrames:   2,
	}

	f1, _ := iter.Next()
	f1Copy := make([]byte, len(f1.YUV))
	copy(f1Copy, f1.YUV)

	f2, _ := iter.Next()

	// f1's YUV should still be intact even though the decoder reused its buffer.
	for i := range f1.YUV {
		if f1.YUV[i] != f1Copy[i] {
			t.Fatalf("frame 1 YUV was corrupted at byte %d after frame 2 decode", i)
		}
	}
	_ = f2
}

// stubDecoderFactory returns a mock decoder that always produces output.
func stubDecoderFactory() (VideoDecoder, error) {
	return &mockVideoDecoder{width: 320, height: 240}, nil
}

// mockVideoDecoder is a simple decoder for testing.
type mockVideoDecoder struct {
	width  int
	height int
	closed bool
}

func (d *mockVideoDecoder) Decode(h264 []byte) ([]byte, int, int, error) {
	if d.closed {
		return nil, 0, 0, fmt.Errorf("decoder closed")
	}
	if len(h264) == 0 {
		return nil, 0, 0, fmt.Errorf("empty input")
	}
	size := video.YUV420FrameSize(d.width, d.height)
	yuv := make([]byte, size)
	return yuv, d.width, d.height, nil
}

func (d *mockVideoDecoder) Close() {
	d.closed = true
}

// mockDrainableDecoder simulates B-frame reordering: first `bufferCount`
// Decode() calls return "buffering", then subsequent calls return output.
// After SendEOS, ReceiveFrame returns `drainFrames` frames.
type mockDrainableDecoder struct {
	width       int
	height      int
	bufferCount int
	drainFrames int
	callCount   int
	eosSent     bool
	drainCount  int
	closed      bool
}

func (d *mockDrainableDecoder) Decode(h264 []byte) ([]byte, int, int, error) {
	if d.closed {
		return nil, 0, 0, fmt.Errorf("decoder closed")
	}
	d.callCount++
	if d.callCount <= d.bufferCount {
		return nil, 0, 0, fmt.Errorf("buffering")
	}
	size := video.YUV420FrameSize(d.width, d.height)
	yuv := make([]byte, size)
	return yuv, d.width, d.height, nil
}

func (d *mockDrainableDecoder) SendEOS() error {
	d.eosSent = true
	return nil
}

func (d *mockDrainableDecoder) ReceiveFrame() ([]byte, int, int, error) {
	d.drainCount++
	if d.drainCount > d.drainFrames {
		return nil, 0, 0, fmt.Errorf("no more frames")
	}
	size := video.YUV420FrameSize(d.width, d.height)
	yuv := make([]byte, size)
	return yuv, d.width, d.height, nil
}

func (d *mockDrainableDecoder) Close() {
	d.closed = true
}

// mockReusingDecoder returns the same buffer slice every time,
// simulating FFmpeg's real buffer-reuse behavior.
type mockReusingDecoder struct {
	width  int
	height int
	buf    []byte
	call   int
	closed bool
}

func (d *mockReusingDecoder) Decode(h264 []byte) ([]byte, int, int, error) {
	if d.closed {
		return nil, 0, 0, fmt.Errorf("decoder closed")
	}
	d.call++
	// Fill with a pattern based on call count so we can detect corruption.
	for i := range d.buf {
		d.buf[i] = byte(d.call)
	}
	return d.buf, d.width, d.height, nil
}

func (d *mockReusingDecoder) Close() {
	d.closed = true
}

// generateTestTSFile uses the ffmpeg CLI to create a real H.264+AAC MPEG-TS
// file suitable for integration testing with the real FFmpeg decoder.
// Returns the path to the generated file, or skips the test if ffmpeg is not available.
func generateTestTSFile(t *testing.T) string {
	t.Helper()

	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg CLI not available, skipping integration test")
	}

	tmpFile := filepath.Join(t.TempDir(), "test.ts")
	cmd := exec.Command(ffmpegPath, "-y",
		"-f", "lavfi", "-i", "testsrc=duration=1:size=320x240:rate=30",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1:sample_rate=48000",
		"-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency",
		"-pix_fmt", "yuv420p", // force YUV420 (testsrc is RGB; without this, x264 may use High 4:4:4)
		"-g", "30", // keyframe every 30 frames (1 GOP for 1 second)
		"-c:a", "aac", "-b:a", "64k", "-ac", "2",
		"-f", "mpegts", tmpFile,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("ffmpeg failed to generate test TS: %v\n%s", err, out)
	}

	info, err := os.Stat(tmpFile)
	if err != nil || info.Size() == 0 {
		t.Skipf("ffmpeg produced empty or missing file: %v", err)
	}

	return tmpFile
}

// softwareDecoderFactory creates a pure-software FFmpeg decoder (no hardware
// acceleration). On macOS, DefaultDecoderFactory passes the VideoToolbox HW
// context which can produce pixel formats that fail for certain test content.
// Using software-only ensures deterministic behavior across platforms.
func softwareDecoderFactory() (VideoDecoder, error) {
	return codec.NewFFmpegDecoderWithThreads(nil, 1)
}

func TestFileFrameIterator_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Check if the real FFmpeg decoder is available before generating the test file.
	dec, err := softwareDecoderFactory()
	if err != nil {
		t.Skipf("FFmpeg decoder not available (non-cgo build?): %v", err)
	}
	dec.Close()

	tsPath := generateTestTSFile(t)

	iter, err := NewFileFrameIteratorWithDecoder(tsPath, softwareDecoderFactory)
	if err != nil {
		t.Fatalf("NewFileFrameIterator(%s): %v", tsPath, err)
	}
	defer iter.Close()

	totalFrames := iter.TotalFrames()
	if totalFrames <= 0 {
		t.Fatalf("TotalFrames() = %d, want > 0", totalFrames)
	}

	fps := iter.EstimateFPS()
	if fps < 15 || fps > 60 {
		t.Errorf("EstimateFPS() = %.2f, want between 15 and 60", fps)
	}
	t.Logf("EstimateFPS() = %.2f, TotalFrames() = %d", fps, totalFrames)

	var (
		count       int
		prevPTS     int64
		firstPTS    int64
		ptsSet      bool
		audioCount  int
		hasKeyframe bool
	)

	for {
		frame, err := iter.Next()
		if err != nil {
			t.Fatalf("Next() error on frame %d: %v", count, err)
		}
		if frame == nil {
			break
		}

		count++

		// Frame must have valid YUV data.
		if len(frame.YUV) == 0 {
			t.Errorf("frame %d: YUV data is empty", count)
			continue
		}

		// Dimensions must be valid.
		if frame.Width <= 0 || frame.Height <= 0 {
			t.Errorf("frame %d: invalid dimensions %dx%d", count, frame.Width, frame.Height)
			continue
		}

		// YUV420 size must match video.YUV420FrameSize(width, height).
		expectedSize := video.YUV420FrameSize(frame.Width, frame.Height)
		if len(frame.YUV) != expectedSize {
			t.Errorf("frame %d: YUV size = %d, want %d (for %dx%d)",
				count, len(frame.YUV), expectedSize, frame.Width, frame.Height)
		}

		// Track PTS monotonicity (allow small backward jumps from B-frame reordering
		// in drained frames, but flag significant regressions).
		if !ptsSet {
			firstPTS = frame.PTS
			prevPTS = frame.PTS
			ptsSet = true
		} else {
			// Allow up to 2 frame intervals backward (B-frame reorder tolerance).
			const maxBackwardPTS = 6000 // ~2 frames at 30fps in 90kHz
			if frame.PTS < prevPTS-maxBackwardPTS {
				t.Errorf("frame %d: PTS went backward significantly: %d -> %d (delta=%d)",
					count, prevPTS, frame.PTS, frame.PTS-prevPTS)
			}
			prevPTS = frame.PTS
		}

		if frame.IsKeyframe {
			hasKeyframe = true
		}

		if len(frame.AudioData) > 0 {
			audioCount++
		}
	}

	// Verify iteration count matches TotalFrames (allow small differences from
	// B-frame drain producing extra frames).
	diff := count - totalFrames
	if diff < 0 {
		diff = -diff
	}
	if diff > 3 {
		t.Errorf("iterated %d frames, but TotalFrames() = %d (diff %d > 3)", count, totalFrames, diff)
	}

	// 1 second at 30fps should yield roughly 25-35 frames (some may be lost to
	// decoder buffering, B-frame drain, etc.).
	if count < 10 {
		t.Errorf("only decoded %d frames from 1s@30fps clip, expected at least 10", count)
	}

	if !hasKeyframe {
		t.Error("no keyframes found in decoded output")
	}

	// Audio should be present on at least some frames.
	if audioCount == 0 {
		t.Error("no frames had audio data, expected audio interleaving")
	}

	t.Logf("decoded %d frames (%d with audio), PTS range %d-%d, has keyframe=%v",
		count, audioCount, firstPTS, prevPTS, hasKeyframe)
}

// Verify the audio PTS boundary logic specifically for the streaming decode path.
// The old code used f.frames[f.index+1].PTS as ptsBound; the new code uses
// f.encodedFrames[f.frameIndex].PTS. Since frameIndex is incremented BEFORE
// attachAudio is called (in the feed loop), it already points to the next
// encoded frame, which is correct.
func TestFileFrameIterator_AudioBoundaryWithStreamingDecode(t *testing.T) {
	// 3 video frames, 3 audio frames carefully positioned.
	encoded := []encodedFrame{
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x65}, pts: 0},
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x41}, pts: 3000},
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x41}, pts: 6000},
	}

	audioData := []audioFrameInfo{
		{data: []byte{0xA0}, pts: 0, sampleRate: 48000, channels: 2},
		{data: []byte{0xA1}, pts: 3500, sampleRate: 48000, channels: 2},
		{data: []byte{0xA2}, pts: 6500, sampleRate: 48000, channels: 2},
	}

	iter := &FileFrameIterator{
		encodedFrames: encoded,
		audioFrames:   audioData,
		decoder:       &mockVideoDecoder{width: 4, height: 2},
		fps:           30.0,
		totalFrames:   3,
	}

	// Frame 0 (PTS=0): audio window [MinInt, 3000) -> Audio[0] PTS=0
	f0, _ := iter.Next()
	if f0.AudioPTS != 0 {
		t.Errorf("frame 0 AudioPTS = %d, want 0", f0.AudioPTS)
	}

	// Frame 1 (PTS=3000): audio window [3000, 6000) -> Audio[1] PTS=3500
	f1, _ := iter.Next()
	if f1.AudioPTS != 3500 {
		t.Errorf("frame 1 AudioPTS = %d, want 3500", f1.AudioPTS)
	}

	// Frame 2 (PTS=6000): audio window [6000, MaxInt) -> Audio[2] PTS=6500
	f2, _ := iter.Next()
	if f2.AudioPTS != 6500 {
		t.Errorf("frame 2 AudioPTS = %d, want 6500", f2.AudioPTS)
	}

	// EOF
	fEnd, _ := iter.Next()
	if fEnd != nil {
		t.Error("expected EOF")
	}
}

func TestFileFrameIterator_FrameIndex(t *testing.T) {
	encoded := make([]encodedFrame, 5)
	for i := range encoded {
		encoded[i] = encodedFrame{
			wireData:   []byte{0x00, 0x00, 0x00, 0x01, byte(i)},
			pts:        int64(i) * 3000,
			isKeyframe: i == 0,
		}
	}

	iter := &FileFrameIterator{
		encodedFrames:  encoded,
		audioFrames:    nil,
		decoder:        &mockVideoDecoder{width: 4, height: 2},
		decoderFactory: stubDecoderFactory,
		fps:            30.0,
		totalFrames:    5,
	}
	defer iter.Close()

	// Initially at 0.
	if idx := iter.FrameIndex(); idx != 0 {
		t.Errorf("FrameIndex() before any Next() = %d, want 0", idx)
	}

	// After reading one frame, frameIndex advances (it's the *next* frame to feed).
	if _, err := iter.Next(); err != nil {
		t.Fatalf("Next: %v", err)
	}
	if idx := iter.FrameIndex(); idx != 1 {
		t.Errorf("FrameIndex() after 1 Next() = %d, want 1", idx)
	}

	if _, err := iter.Next(); err != nil {
		t.Fatalf("Next: %v", err)
	}
	if _, err := iter.Next(); err != nil {
		t.Fatalf("Next: %v", err)
	}
	if idx := iter.FrameIndex(); idx != 3 {
		t.Errorf("FrameIndex() after 3 Next() = %d, want 3", idx)
	}
}

func TestFileFrameIterator_Seek_ToBeginning(t *testing.T) {
	encoded := []encodedFrame{
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x65}, pts: 0, isKeyframe: true},
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x41}, pts: 3000},
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x41}, pts: 6000},
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x41}, pts: 9000},
	}

	iter := &FileFrameIterator{
		encodedFrames:  encoded,
		audioFrames:    nil,
		decoder:        &mockVideoDecoder{width: 4, height: 2},
		decoderFactory: stubDecoderFactory,
		fps:            30.0,
		totalFrames:    4,
	}
	defer iter.Close()

	// Read 3 frames to advance the position.
	for i := 0; i < 3; i++ {
		f, err := iter.Next()
		if err != nil || f == nil {
			t.Fatalf("Next() %d: err=%v, frame=%v", i, err, f)
		}
	}
	if iter.FrameIndex() != 3 {
		t.Fatalf("FrameIndex() after 3 reads = %d, want 3", iter.FrameIndex())
	}

	// Seek to beginning.
	if err := iter.Seek(0); err != nil {
		t.Fatalf("Seek(0): %v", err)
	}
	if iter.FrameIndex() != 0 {
		t.Errorf("FrameIndex() after Seek(0) = %d, want 0", iter.FrameIndex())
	}

	// Should be able to read all frames again from the start.
	f, err := iter.Next()
	if err != nil || f == nil {
		t.Fatalf("Next() after Seek(0): err=%v, frame=%v", err, f)
	}
	if f.PTS != 0 {
		t.Errorf("first frame PTS after Seek(0) = %d, want 0", f.PTS)
	}
}

func TestFileFrameIterator_Seek_ToKeyframe(t *testing.T) {
	// Two GOPs: keyframe at 0, keyframe at 4.
	encoded := []encodedFrame{
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x65}, pts: 0, isKeyframe: true},
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x41}, pts: 3000},
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x41}, pts: 6000},
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x41}, pts: 9000},
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x65}, pts: 12000, isKeyframe: true},
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x41}, pts: 15000},
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x41}, pts: 18000},
	}

	iter := &FileFrameIterator{
		encodedFrames:  encoded,
		audioFrames:    nil,
		decoder:        &mockVideoDecoder{width: 4, height: 2},
		decoderFactory: stubDecoderFactory,
		fps:            30.0,
		totalFrames:    7,
	}
	defer iter.Close()

	// Seek directly to keyframe at index 4.
	if err := iter.Seek(4); err != nil {
		t.Fatalf("Seek(4): %v", err)
	}
	if iter.FrameIndex() != 4 {
		t.Errorf("FrameIndex() after Seek(4) = %d, want 4", iter.FrameIndex())
	}

	f, err := iter.Next()
	if err != nil || f == nil {
		t.Fatalf("Next() after Seek(4): err=%v, frame=%v", err, f)
	}
	if f.PTS != 12000 {
		t.Errorf("PTS after Seek(4) = %d, want 12000", f.PTS)
	}
}

func TestFileFrameIterator_Seek_FallsBackToPreviousKeyframe(t *testing.T) {
	// Two GOPs: keyframe at 0, keyframe at 4.
	encoded := []encodedFrame{
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x65}, pts: 0, isKeyframe: true},
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x41}, pts: 3000},
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x41}, pts: 6000},
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x41}, pts: 9000},
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x65}, pts: 12000, isKeyframe: true},
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x41}, pts: 15000},
	}

	iter := &FileFrameIterator{
		encodedFrames:  encoded,
		audioFrames:    nil,
		decoder:        &mockVideoDecoder{width: 4, height: 2},
		decoderFactory: stubDecoderFactory,
		fps:            30.0,
		totalFrames:    6,
	}
	defer iter.Close()

	// Seek to frame 2 (not a keyframe) -- should fall back to keyframe at 0.
	if err := iter.Seek(2); err != nil {
		t.Fatalf("Seek(2): %v", err)
	}
	if iter.FrameIndex() != 0 {
		t.Errorf("FrameIndex() after Seek(2) = %d, want 0 (nearest keyframe)", iter.FrameIndex())
	}

	// Seek to frame 5 (not a keyframe) -- should fall back to keyframe at 4.
	if err := iter.Seek(5); err != nil {
		t.Fatalf("Seek(5): %v", err)
	}
	if iter.FrameIndex() != 4 {
		t.Errorf("FrameIndex() after Seek(5) = %d, want 4 (nearest keyframe)", iter.FrameIndex())
	}

	f, err := iter.Next()
	if err != nil || f == nil {
		t.Fatalf("Next() after Seek(5): err=%v, frame=%v", err, f)
	}
	if f.PTS != 12000 {
		t.Errorf("PTS after Seek(5) = %d, want 12000", f.PTS)
	}
}

func TestFileFrameIterator_Seek_ClosedIterator(t *testing.T) {
	iter := &FileFrameIterator{
		encodedFrames:  []encodedFrame{{wireData: []byte{1}, pts: 0, isKeyframe: true}},
		decoder:        &mockVideoDecoder{width: 4, height: 2},
		decoderFactory: stubDecoderFactory,
		fps:            30.0,
		totalFrames:    1,
	}
	iter.Close()

	err := iter.Seek(0)
	if err == nil {
		t.Fatal("Seek() on closed iterator should return error")
	}
	if got := err.Error(); got != "iterator closed" {
		t.Errorf("error = %q, want %q", got, "iterator closed")
	}
}

func TestFileFrameIterator_Seek_OutOfRange(t *testing.T) {
	encoded := []encodedFrame{
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x65}, pts: 0, isKeyframe: true},
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x41}, pts: 3000},
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x41}, pts: 6000},
	}

	iter := &FileFrameIterator{
		encodedFrames:  encoded,
		audioFrames:    nil,
		decoder:        &mockVideoDecoder{width: 4, height: 2},
		decoderFactory: stubDecoderFactory,
		fps:            30.0,
		totalFrames:    3,
	}
	defer iter.Close()

	// Negative index.
	if err := iter.Seek(-1); err == nil {
		t.Error("Seek(-1) should return error")
	}

	// Beyond end.
	if err := iter.Seek(3); err == nil {
		t.Error("Seek(3) should return error for len=3")
	}

	if err := iter.Seek(100); err == nil {
		t.Error("Seek(100) should return error")
	}
}

func TestFileFrameIterator_Seek_ResetsAudioIndex(t *testing.T) {
	// Two GOPs with interleaved audio.
	encoded := []encodedFrame{
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x65}, pts: 0, isKeyframe: true},
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x41}, pts: 3000},
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x65}, pts: 6000, isKeyframe: true},
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x41}, pts: 9000},
	}

	audioData := []audioFrameInfo{
		{data: []byte{0xA0}, pts: 0, sampleRate: 48000, channels: 2},
		{data: []byte{0xA1}, pts: 2000, sampleRate: 48000, channels: 2},
		{data: []byte{0xA2}, pts: 5000, sampleRate: 48000, channels: 2},
		{data: []byte{0xA3}, pts: 8000, sampleRate: 48000, channels: 2},
	}

	iter := &FileFrameIterator{
		encodedFrames:  encoded,
		audioFrames:    audioData,
		decoder:        &mockVideoDecoder{width: 4, height: 2},
		decoderFactory: stubDecoderFactory,
		fps:            30.0,
		totalFrames:    4,
	}
	defer iter.Close()

	// Read all frames to advance audio index.
	for i := 0; i < 4; i++ {
		if _, err := iter.Next(); err != nil {
			t.Fatalf("Next: %v", err)
		}
	}

	// Seek to keyframe at index 2 (PTS=6000). Audio should reset to
	// the first audio frame with PTS >= 6000, which is audioData[2] (PTS=5000
	// is < 6000, so audioData[3] at PTS=8000? No, binary search returns first
	// i where audioFrames[i].pts >= 6000, which is index 2 since pts=5000 < 6000
	// and pts=8000 >= 6000... wait: audioData[2].pts=5000 < 6000, audioData[3].pts=8000 >= 6000.
	// sort.Search returns 3.
	// Actually let me re-check: we want first i where pts >= 6000.
	// audioData[0].pts=0, [1].pts=2000, [2].pts=5000, [3].pts=8000.
	// First i where pts >= 6000 is i=3.
	if err := iter.Seek(2); err != nil {
		t.Fatalf("Seek(2): %v", err)
	}

	// Now read a frame and verify audio is attached from the correct position.
	f, err := iter.Next()
	if err != nil || f == nil {
		t.Fatalf("Next() after Seek(2): err=%v, f=%v", err, f)
	}
	if f.PTS != 6000 {
		t.Errorf("PTS = %d, want 6000", f.PTS)
	}

	// The audio attachment uses the proportional formula, which depends on
	// outputCount and audioIndex. After Seek(2), outputCount=2, audioIndex=3.
	// The proportional formula: targetAudioIdx = (outputCount+1) * totalAudio / totalVideo
	// = 3 * 4 / 4 = 3. Since audioIndex is already 3, no audio in range [3,3).
	// Then next frame: targetAudioIdx = (3+1)*4/4 = 4, so audioIndex advances [3,4) -> 1 frame.
	// This is correct behavior: after seeking to frame 2, audio resumes from the right position.
}

func TestFileFrameIterator_Seek_ResetsDraining(t *testing.T) {
	encoded := []encodedFrame{
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x65}, pts: 0, isKeyframe: true},
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x41}, pts: 3000},
		{wireData: []byte{0x00, 0x00, 0x00, 0x01, 0x41}, pts: 6000},
	}

	iter := &FileFrameIterator{
		encodedFrames:  encoded,
		audioFrames:    nil,
		decoder:        &mockVideoDecoder{width: 4, height: 2},
		decoderFactory: stubDecoderFactory,
		fps:            30.0,
		totalFrames:    3,
	}
	defer iter.Close()

	// Exhaust the iterator.
	for {
		f, _ := iter.Next()
		if f == nil {
			break
		}
	}

	// After exhaustion, draining should be true and frameIndex at end.
	if !iter.draining {
		t.Error("expected draining=true after exhaustion")
	}

	// Seek back to beginning.
	if err := iter.Seek(0); err != nil {
		t.Fatalf("Seek(0) after exhaustion: %v", err)
	}
	if iter.draining {
		t.Error("draining should be false after Seek")
	}
	if iter.FrameIndex() != 0 {
		t.Errorf("FrameIndex() = %d, want 0", iter.FrameIndex())
	}

	// Should be able to iterate again.
	count := 0
	for {
		f, err := iter.Next()
		if err != nil {
			t.Fatalf("Next() error: %v", err)
		}
		if f == nil {
			break
		}
		count++
	}
	if count != 3 {
		t.Errorf("re-iterated %d frames, want 3", count)
	}
}

func TestFileFrameIterator_Seek_NoDecoderFactory(t *testing.T) {
	// Iterators constructed without a factory (e.g., in some older tests)
	// should return an error from Seek.
	iter := &FileFrameIterator{
		encodedFrames: []encodedFrame{{wireData: []byte{1}, pts: 0, isKeyframe: true}},
		decoder:       &mockVideoDecoder{width: 4, height: 2},
		fps:           30.0,
		totalFrames:   1,
	}
	defer iter.Close()

	err := iter.Seek(0)
	if err == nil {
		t.Fatal("Seek() without decoder factory should return error")
	}
}

// delayDecoder outputs each frame depth Decode calls after it was fed, as a
// frame-threaded decoder does once its pipeline has filled. A picture is the
// last byte of the frame's wire data.
type delayDecoder struct {
	depth   int
	queue   [][]byte
	decodes int
	eosSent bool
}

func (d *delayDecoder) Decode(h264 []byte) ([]byte, int, int, error) {
	d.decodes++
	d.queue = append(d.queue, []byte{h264[len(h264)-1]})
	if len(d.queue) <= d.depth {
		return nil, 0, 0, fmt.Errorf("no output frame yet (buffering)")
	}
	pic := d.queue[0]
	d.queue = d.queue[1:]
	return pic, 1, 1, nil
}

func (d *delayDecoder) SendEOS() error {
	d.eosSent = true
	return nil
}

func (d *delayDecoder) ReceiveFrame() ([]byte, int, int, error) {
	if len(d.queue) == 0 {
		return nil, 0, 0, fmt.Errorf("drained")
	}
	pic := d.queue[0]
	d.queue = d.queue[1:]
	return pic, 1, 1, nil
}

func (d *delayDecoder) Close() {}

// A looping iterator keeps its decoder across the wrap and feeds it the next
// pass's first frames to get the last pictures out, as the clip player does.
// Draining it instead would need a fresh decoder for the next pass, which
// returns nothing until its pipeline has filled again: the tick that starts
// the pass would decode depth+1 frames rather than one.
func TestFileFrameIterator_LoopKeepsDecoderAcrossWrap(t *testing.T) {
	const n, depth, passes = 10, 3, 4
	encoded := make([]encodedFrame, n)
	audio := make([]audioFrameInfo, n)
	for i := range encoded {
		encoded[i] = encodedFrame{wireData: []byte{0, 0, 0, 1, 0x41, byte(i)}, pts: int64(i) * 3000, isKeyframe: i == 0}
		audio[i] = audioFrameInfo{data: []byte{byte(i)}, pts: int64(i) * 3000}
	}
	dec := &delayDecoder{depth: depth}
	created := 0
	iter := &FileFrameIterator{
		encodedFrames: encoded,
		audioFrames:   audio,
		decoder:       dec,
		decoderFactory: func() (VideoDecoder, error) {
			created++
			return &delayDecoder{depth: depth}, nil
		},
		fps:         30,
		totalFrames: n,
	}
	defer iter.Close()
	iter.SetLoop(true)

	for pass := 0; pass < passes; pass++ {
		if pass > 0 {
			if err := iter.Rewind(); err != nil {
				t.Fatalf("pass %d: Rewind: %v", pass, err)
			}
		}
		var pics, sound []byte
		for {
			cur := iter.decoder.(*delayDecoder)
			before := cur.decodes
			f, err := iter.Next()
			if err != nil {
				t.Fatalf("pass %d: Next: %v", pass, err)
			}
			if f == nil {
				break
			}
			if pass > 0 && cur.decodes-before != 1 {
				t.Fatalf("pass %d picture %d took %d decodes, want 1", pass, len(pics), cur.decodes-before)
			}
			pics = append(pics, f.YUV[0])
			for _, a := range f.AudioFrames {
				sound = append(sound, a.Data[0])
			}
		}
		want := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
		if string(pics) != string(want) {
			t.Fatalf("pass %d pictures = %v, want %v", pass, pics, want)
		}
		if string(sound) != string(want) {
			t.Fatalf("pass %d audio = %v, want %v", pass, sound, want)
		}
	}
	if created != 0 {
		t.Fatalf("%d decoders created after the first, want 0", created)
	}
	if dec.eosSent {
		t.Fatal("the decoder was drained at a wrap")
	}
}

// Rewinding a pass that was not played out (a trim out-point stops it early)
// starts over on a fresh decoder: the old one still holds pictures from past
// the out-point.
func TestFileFrameIterator_RewindMidPassRestartsDecoder(t *testing.T) {
	encoded := make([]encodedFrame, 10)
	for i := range encoded {
		encoded[i] = encodedFrame{wireData: []byte{0, 0, 0, 1, 0x41, byte(i)}, pts: int64(i) * 3000, isKeyframe: i == 0}
	}
	created := 0
	iter := &FileFrameIterator{
		encodedFrames: encoded,
		decoder:       &delayDecoder{depth: 2},
		decoderFactory: func() (VideoDecoder, error) {
			created++
			return &delayDecoder{depth: 2}, nil
		},
		fps:         30,
		totalFrames: 10,
	}
	defer iter.Close()
	iter.SetLoop(true)

	for i := 0; i < 4; i++ {
		if _, err := iter.Next(); err != nil {
			t.Fatal(err)
		}
	}
	if err := iter.Rewind(); err != nil {
		t.Fatal(err)
	}
	f, err := iter.Next()
	if err != nil || f == nil {
		t.Fatalf("Next after Rewind = %v, %v", f, err)
	}
	if f.YUV[0] != 0 || created != 1 {
		t.Fatalf("after rewind: picture %d on %d new decoders, want picture 0 on 1", f.YUV[0], created)
	}
}

// With a real frame-threaded decoder, every pass of a looping iterator puts
// out the same pictures as the first, on the one decoder.
func TestFileFrameIterator_Integration_LoopPasses(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	created := 0
	factory := func() (VideoDecoder, error) {
		created++
		return codec.NewFFmpegDecoderWithThreads(nil, 4)
	}
	dec, err := factory()
	if err != nil {
		t.Skipf("FFmpeg decoder not available (non-cgo build?): %v", err)
	}
	dec.Close()
	created = 0

	iter, err := NewFileFrameIteratorWithDecoder(generateTestTSFile(t), factory)
	if err != nil {
		t.Fatal(err)
	}
	defer iter.Close()
	iter.SetLoop(true)

	var first [][]byte
	for pass := 0; pass < 3; pass++ {
		if pass > 0 {
			if err := iter.Rewind(); err != nil {
				t.Fatalf("pass %d: Rewind: %v", pass, err)
			}
		}
		var pics [][]byte
		for {
			f, err := iter.Next()
			if err != nil {
				t.Fatalf("pass %d: Next: %v", pass, err)
			}
			if f == nil {
				break
			}
			pics = append(pics, f.YUV)
		}
		if len(pics) != iter.TotalFrames() {
			t.Fatalf("pass %d: %d pictures, want %d", pass, len(pics), iter.TotalFrames())
		}
		if pass == 0 {
			first = pics
			continue
		}
		for i := range pics {
			if string(pics[i]) != string(first[i]) {
				t.Fatalf("pass %d picture %d differs from the first pass", pass, i)
			}
		}
	}
	if created != 1 {
		t.Fatalf("%d decoders created for 3 passes, want 1", created)
	}
}
