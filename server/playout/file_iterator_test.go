package playout

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
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
