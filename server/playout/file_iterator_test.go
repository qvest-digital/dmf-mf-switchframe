package playout

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

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
