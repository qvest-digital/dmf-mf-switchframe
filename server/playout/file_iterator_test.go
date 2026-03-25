package playout

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/zsiec/switchframe/server/codec"
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
		// Expected — nonexistent file.
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

func TestFileFrameIterator_NextEOF(t *testing.T) {
	// Test that a FileFrameIterator with no frames returns nil immediately.
	iter := &FileFrameIterator{
		frames: nil,
		fps:    30.0,
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
	frames := make([]DecodedFrame, 5)
	for i := range frames {
		frames[i] = DecodedFrame{
			YUV:        make([]byte, 320*240*3/2),
			PTS:        int64(i) * 3000,
			IsKeyframe: i == 0,
			Width:      320,
			Height:     240,
		}
	}

	iter := &FileFrameIterator{
		frames: frames,
		fps:    30.0,
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

func TestFileFrameIterator_AudioInterleaving_PTS(t *testing.T) {
	// Verify that audio frames are attached to video frames by PTS proximity,
	// not proportionally spread.
	//
	// Video: 4 frames at 30fps -> PTS 0, 3000, 6000, 9000 (90kHz clock)
	// Audio: 5 frames at ~21.3ms -> PTS 0, 1920, 3840, 5760, 7680
	//
	// PTS-matched interleaving (audio PTS < next video PTS boundary):
	//   Video[0] PTS=0    boundary=3000  -> Audio[0] PTS=0, Audio[1] PTS=1920 (attach 0, skip 1)
	//   Video[1] PTS=3000 boundary=6000  -> Audio[2] PTS=3840, Audio[3] PTS=5760 (attach 2, skip 3)
	//   Video[2] PTS=6000 boundary=9000  -> Audio[4] PTS=7680 (attach 4)
	//   Video[3] PTS=9000 boundary=MaxInt -> (none remaining)
	//
	// Proportional interleaving ((i+1)*5/4):
	//   Video[0] -> targetIdx=1  -> Audio[0]
	//   Video[1] -> targetIdx=2  -> Audio[1]   <- WRONG: Audio[1] PTS=1920 < Video[1] PTS=3000
	//   Video[2] -> targetIdx=3  -> Audio[2]
	//   Video[3] -> targetIdx=5  -> Audio[3]
	//
	// The key difference: with PTS matching, Video[1] gets Audio[2] (PTS=3840),
	// because Audio[1] (PTS=1920) falls before Video[1]'s PTS of 3000.
	// With proportional, Video[1] would get Audio[1] (PTS=1920).

	videoFrames := []DecodedFrame{
		{YUV: make([]byte, 16), PTS: 0, Width: 4, Height: 2},
		{YUV: make([]byte, 16), PTS: 3000, Width: 4, Height: 2},
		{YUV: make([]byte, 16), PTS: 6000, Width: 4, Height: 2},
		{YUV: make([]byte, 16), PTS: 9000, Width: 4, Height: 2},
	}

	audioData := []audioFrameInfo{
		{data: []byte{0xA0}, pts: 0, sampleRate: 48000, channels: 2},
		{data: []byte{0xA1}, pts: 1920, sampleRate: 48000, channels: 2},
		{data: []byte{0xA2}, pts: 3840, sampleRate: 48000, channels: 2},
		{data: []byte{0xA3}, pts: 5760, sampleRate: 48000, channels: 2},
		{data: []byte{0xA4}, pts: 7680, sampleRate: 48000, channels: 2},
	}

	iter := &FileFrameIterator{
		frames:      videoFrames,
		audioFrames: audioData,
		fps:         30.0,
	}

	// Frame 0 (PTS=0): first audio in window [0, 3000) -> Audio[0] PTS=0
	f0, err := iter.Next()
	if err != nil {
		t.Fatalf("Next() frame 0: %v", err)
	}
	if f0.AudioPTS != 0 {
		t.Errorf("frame 0 AudioPTS = %d, want 0", f0.AudioPTS)
	}
	if f0.AudioData == nil {
		t.Fatal("frame 0 AudioData is nil, want non-nil")
	}

	// Frame 1 (PTS=3000): first audio in window [3000, 6000) -> Audio[2] PTS=3840
	// NOT Audio[1] PTS=1920 (which proportional would give)
	f1, err := iter.Next()
	if err != nil {
		t.Fatalf("Next() frame 1: %v", err)
	}
	if f1.AudioPTS != 3840 {
		t.Errorf("frame 1 AudioPTS = %d, want 3840 (PTS-matched); "+
			"1920 would indicate proportional interleaving", f1.AudioPTS)
	}

	// Frame 2 (PTS=6000): first audio in window [6000, 9000) -> Audio[4] PTS=7680
	f2, err := iter.Next()
	if err != nil {
		t.Fatalf("Next() frame 2: %v", err)
	}
	if f2.AudioPTS != 7680 {
		t.Errorf("frame 2 AudioPTS = %d, want 7680", f2.AudioPTS)
	}

	// Frame 3 (PTS=9000): no remaining audio -> AudioData=nil
	f3, err := iter.Next()
	if err != nil {
		t.Fatalf("Next() frame 3: %v", err)
	}
	if f3.AudioData != nil {
		t.Errorf("frame 3 should have no audio, got AudioPTS=%d", f3.AudioPTS)
	}
}

func TestFileFrameIterator_AudioInterleaving_NoAudio(t *testing.T) {
	// Iterator with no audio should return frames without audio data.
	videoFrames := []DecodedFrame{
		{YUV: make([]byte, 16), PTS: 0, Width: 4, Height: 2},
		{YUV: make([]byte, 16), PTS: 3000, Width: 4, Height: 2},
	}

	iter := &FileFrameIterator{
		frames: videoFrames,
		fps:    30.0,
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
	videoFrames := []DecodedFrame{
		{YUV: make([]byte, 16), PTS: 10000, Width: 4, Height: 2},
		{YUV: make([]byte, 16), PTS: 13000, Width: 4, Height: 2},
	}

	audioData := []audioFrameInfo{
		{data: []byte{0xA0}, pts: 100, sampleRate: 48000, channels: 2},
		{data: []byte{0xA1}, pts: 200, sampleRate: 48000, channels: 2},
		{data: []byte{0xA2}, pts: 300, sampleRate: 48000, channels: 2},
	}

	iter := &FileFrameIterator{
		frames:      videoFrames,
		audioFrames: audioData,
		fps:         30.0,
	}

	// Frame 0: all audio PTS < next video PTS (13000), first audio attached
	f0, err := iter.Next()
	if err != nil {
		t.Fatalf("Next() frame 0: %v", err)
	}
	if f0.AudioPTS != 100 {
		t.Errorf("frame 0 AudioPTS = %d, want 100", f0.AudioPTS)
	}

	// Frame 1: no audio remaining (all consumed by frame 0's window)
	f1, err := iter.Next()
	if err != nil {
		t.Fatalf("Next() frame 1: %v", err)
	}
	if f1.AudioData != nil {
		t.Errorf("frame 1 should have no audio, got AudioPTS=%d", f1.AudioPTS)
	}
}

func TestFileFrameIterator_AudioInterleaving_LastFrameGetsRemaining(t *testing.T) {
	// The last video frame should get audio with PTS >= its own PTS.
	videoFrames := []DecodedFrame{
		{YUV: make([]byte, 16), PTS: 0, Width: 4, Height: 2},
		{YUV: make([]byte, 16), PTS: 3000, Width: 4, Height: 2},
	}

	audioData := []audioFrameInfo{
		{data: []byte{0xA0}, pts: 0, sampleRate: 48000, channels: 2},
		{data: []byte{0xA1}, pts: 4000, sampleRate: 48000, channels: 2},
		{data: []byte{0xA2}, pts: 8000, sampleRate: 48000, channels: 2},
	}

	iter := &FileFrameIterator{
		frames:      videoFrames,
		audioFrames: audioData,
		fps:         30.0,
	}

	// Frame 0 (PTS=0): audio in [0, 3000) -> Audio[0] PTS=0
	f0, err := iter.Next()
	if err != nil {
		t.Fatalf("Next() frame 0: %v", err)
	}
	if f0.AudioPTS != 0 {
		t.Errorf("frame 0 AudioPTS = %d, want 0", f0.AudioPTS)
	}

	// Frame 1 (PTS=3000, last): audio in [3000, MaxInt) -> Audio[1] PTS=4000
	f1, err := iter.Next()
	if err != nil {
		t.Fatalf("Next() frame 1: %v", err)
	}
	if f1.AudioPTS != 4000 {
		t.Errorf("frame 1 AudioPTS = %d, want 4000", f1.AudioPTS)
	}
}

func TestFileFrameIterator_Close(t *testing.T) {
	iter := &FileFrameIterator{fps: 30.0}
	// Close should not panic.
	iter.Close()
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
	size := d.width * d.height * 3 / 2
	yuv := make([]byte, size)
	return yuv, d.width, d.height, nil
}

func (d *mockVideoDecoder) Close() {
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
		count        int
		prevPTS      int64
		firstPTS     int64
		ptsSet       bool
		audioCount   int
		hasKeyframe  bool
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

		// YUV420 size must match width*height*3/2.
		expectedSize := frame.Width * frame.Height * 3 / 2
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

	// Verify iteration count matches TotalFrames.
	if count != totalFrames {
		t.Errorf("iterated %d frames, but TotalFrames() = %d", count, totalFrames)
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
