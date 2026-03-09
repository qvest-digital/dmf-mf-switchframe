package demo

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
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

// verifyZipContents checks a stinger zip has the expected PNGs and optionally a WAV.
func verifyZipContents(t *testing.T, data []byte, expectedPNGs int, expectWAV bool) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)

	pngCount := 0
	wavCount := 0
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		lower := strings.ToLower(f.Name)
		if strings.HasSuffix(lower, ".png") {
			pngCount++
		}
		if strings.HasSuffix(lower, ".wav") {
			wavCount++
		}
	}
	require.Equal(t, expectedPNGs, pngCount)
	if expectWAV {
		require.Equal(t, 1, wavCount)
	}
}

func TestGenerateWhooshStingerZip(t *testing.T) {
	data, err := GenerateWhooshStingerZip(320, 240, 30)
	require.NoError(t, err)
	require.True(t, len(data) > 0)
	verifyZipContents(t, data, 30, true)
}

func TestGenerateSlamStingerZip(t *testing.T) {
	data, err := GenerateSlamStingerZip(320, 240, 30)
	require.NoError(t, err)
	require.True(t, len(data) > 0)
	verifyZipContents(t, data, 30, true)
}

func TestGenerateMusicalStingerZip(t *testing.T) {
	data, err := GenerateMusicalStingerZip(320, 240, 30)
	require.NoError(t, err)
	require.True(t, len(data) > 0)
	verifyZipContents(t, data, 30, true)
}

func TestGenerateWhooshStingerZip_LoadsWithAudio(t *testing.T) {
	data, err := GenerateWhooshStingerZip(320, 240, 20)
	require.NoError(t, err)

	dir := t.TempDir()
	store, err := stinger.NewStingerStore(dir, 0)
	require.NoError(t, err)

	err = store.Upload("whoosh", data)
	require.NoError(t, err)

	clip, ok := store.Get("whoosh")
	require.True(t, ok)
	require.Equal(t, 20, len(clip.Frames))
	require.NotNil(t, clip.Audio)
	require.Equal(t, 48000, clip.AudioSampleRate)
	require.Equal(t, 2, clip.AudioChannels)
}

func TestGenerateWhooshStingerZip_InvalidArgs(t *testing.T) {
	_, err := GenerateWhooshStingerZip(0, 240, 20)
	require.Error(t, err)
	_, err = GenerateWhooshStingerZip(321, 240, 20) // odd
	require.Error(t, err)
}

func TestGenerateSlamStingerZip_LoadsWithAudio(t *testing.T) {
	data, err := GenerateSlamStingerZip(320, 240, 20)
	require.NoError(t, err)

	dir := t.TempDir()
	store, err := stinger.NewStingerStore(dir, 0)
	require.NoError(t, err)

	err = store.Upload("slam", data)
	require.NoError(t, err)

	clip, ok := store.Get("slam")
	require.True(t, ok)
	require.Equal(t, 20, len(clip.Frames))
	require.NotNil(t, clip.Audio)
	require.Equal(t, 48000, clip.AudioSampleRate)
	require.Equal(t, 2, clip.AudioChannels)
}

func TestGenerateMusicalStingerZip_LoadsWithAudio(t *testing.T) {
	data, err := GenerateMusicalStingerZip(320, 240, 20)
	require.NoError(t, err)

	dir := t.TempDir()
	store, err := stinger.NewStingerStore(dir, 0)
	require.NoError(t, err)

	err = store.Upload("musical", data)
	require.NoError(t, err)

	clip, ok := store.Get("musical")
	require.True(t, ok)
	require.Equal(t, 20, len(clip.Frames))
	require.NotNil(t, clip.Audio)
	require.Equal(t, 48000, clip.AudioSampleRate)
	require.Equal(t, 2, clip.AudioChannels)
}

func TestGenerateSlamStingerZip_InvalidArgs(t *testing.T) {
	_, err := GenerateSlamStingerZip(0, 240, 20)
	require.Error(t, err)
	_, err = GenerateSlamStingerZip(320, 241, 20) // odd height
	require.Error(t, err)
}

func TestGenerateMusicalStingerZip_InvalidArgs(t *testing.T) {
	_, err := GenerateMusicalStingerZip(320, 240, 0)
	require.Error(t, err)
	_, err = GenerateMusicalStingerZip(319, 240, 20) // odd width
	require.Error(t, err)
}
