//go:build cgo && !noffmpeg

package codec

import (
	"os"
	"path/filepath"
	"testing"
)

// createTestH264File creates a minimal H.264 Annex B file using the FFmpeg encoder.
// Returns the path to the file. The file is cleaned up by t.Cleanup.
func createTestH264File(t *testing.T, dir string) string {
	t.Helper()

	enc, err := NewVideoEncoder(64, 64, 100000, 30, 1)
	if err != nil {
		t.Fatalf("failed to create encoder: %v", err)
	}
	defer enc.Close()

	yuvSize := 64 * 64 * 3 / 2
	yuv := make([]byte, yuvSize)
	// Fill with a green frame (Y=128, Cb=128, Cr=128 = neutral gray)
	for i := range yuv {
		yuv[i] = 128
	}

	path := filepath.Join(dir, "test.h264")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	defer func() { _ = f.Close() }()

	// Encode enough frames to get output (hardware encoders may buffer).
	for i := range 30 {
		data, _, encErr := enc.Encode(yuv, int64(i*3000), i == 0)
		if encErr != nil {
			continue
		}
		if len(data) > 0 {
			if _, writeErr := f.Write(data); writeErr != nil {
				t.Fatalf("failed to write encoded data: %v", writeErr)
			}
		}
	}

	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		t.Fatal("test H.264 file is empty — encoder produced no output")
	}

	return path
}

func TestProbeFile_H264(t *testing.T) {
	dir := t.TempDir()
	path := createTestH264File(t, dir)

	result, err := ProbeFile(path)
	if err != nil {
		t.Fatalf("ProbeFile returned error: %v", err)
	}

	if !result.HasVideo {
		t.Error("expected HasVideo to be true")
	}

	if !result.IsH264() {
		t.Errorf("expected IsH264() to be true, got VideoCodecID=%d", result.VideoCodecID)
	}

	if result.Width != 64 {
		t.Errorf("expected Width=64, got %d", result.Width)
	}
	if result.Height != 64 {
		t.Errorf("expected Height=64, got %d", result.Height)
	}
}

func TestProbeFile_NonExistent(t *testing.T) {
	_, err := ProbeFile("/tmp/nonexistent_test_file_12345.h264")
	if err == nil {
		t.Error("expected error for non-existent file, got nil")
	}
}

func TestProbeFile_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.h264")
	if err := os.WriteFile(path, []byte{}, 0644); err != nil {
		t.Fatalf("failed to create empty file: %v", err)
	}

	_, err := ProbeFile(path)
	if err == nil {
		t.Error("expected error for empty file, got nil")
	}
}

func TestProbeFile_IsH264(t *testing.T) {
	tests := []struct {
		name     string
		codecID  int
		expected bool
	}{
		{"H264", 27, true},
		{"HEVC", 173, false},
		{"zero", 0, false},
		{"AAC", 86018, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &FileProbeResult{VideoCodecID: tt.codecID}
			if got := r.IsH264(); got != tt.expected {
				t.Errorf("IsH264() = %v, want %v for codec ID %d", got, tt.expected, tt.codecID)
			}
		})
	}
}
