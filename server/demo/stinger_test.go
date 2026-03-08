package demo

import (
	"archive/zip"
	"bytes"
	"testing"

	"github.com/zsiec/switchframe/server/stinger"
)

func TestGenerateStingerZip(t *testing.T) {
	const (
		width     = 320
		height    = 240
		numFrames = 20
	)

	data, err := GenerateStingerZip(width, height, numFrames)
	if err != nil {
		t.Fatalf("GenerateStingerZip: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty zip data")
	}

	// Verify it's a valid zip with the correct number of PNGs.
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}

	pngCount := 0
	for _, f := range zr.File {
		if !f.FileInfo().IsDir() {
			pngCount++
		}
	}
	if pngCount != numFrames {
		t.Errorf("expected %d PNGs, got %d", numFrames, pngCount)
	}
}

func TestGenerateStingerZip_InvalidArgs(t *testing.T) {
	tests := []struct {
		name    string
		w, h, n int
	}{
		{"zero width", 0, 240, 20},
		{"zero height", 320, 0, 20},
		{"zero frames", 320, 240, 0},
		{"odd width", 321, 240, 20},
		{"odd height", 320, 241, 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := GenerateStingerZip(tt.w, tt.h, tt.n)
			if err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestGenerateStingerZip_LoadsIntoStore(t *testing.T) {
	const (
		width     = 320
		height    = 240
		numFrames = 20
	)

	data, err := GenerateStingerZip(width, height, numFrames)
	if err != nil {
		t.Fatalf("GenerateStingerZip: %v", err)
	}

	// Create a real StingerStore in a temp directory.
	dir := t.TempDir()
	store, err := stinger.NewStingerStore(dir, 0)
	if err != nil {
		t.Fatalf("NewStingerStore: %v", err)
	}

	// Upload the generated zip.
	if err := store.Upload("demo-wipe", data); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	// Verify clip exists with correct properties.
	clip, ok := store.Get("demo-wipe")
	if !ok {
		t.Fatal("expected clip to exist")
	}
	if clip.Width != width {
		t.Errorf("expected width %d, got %d", width, clip.Width)
	}
	if clip.Height != height {
		t.Errorf("expected height %d, got %d", height, clip.Height)
	}
	if len(clip.Frames) != numFrames {
		t.Errorf("expected %d frames, got %d", numFrames, len(clip.Frames))
	}

	// Verify alpha data is not all-zero (the wipe should have opaque pixels).
	hasNonZeroAlpha := false
	for _, frame := range clip.Frames {
		for _, a := range frame.Alpha {
			if a > 0 {
				hasNonZeroAlpha = true
				break
			}
		}
		if hasNonZeroAlpha {
			break
		}
	}
	if !hasNonZeroAlpha {
		t.Error("expected non-zero alpha data in stinger frames")
	}
}
