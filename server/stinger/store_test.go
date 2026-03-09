package stinger

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func createTestPNG(t *testing.T, dir, name string, w, h int, c color.NRGBA) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	f, err := os.Create(filepath.Join(dir, name))
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	require.NoError(t, png.Encode(f, img))
}

func TestStingerStore_LoadAndList(t *testing.T) {
	dir := t.TempDir()
	stingerDir := filepath.Join(dir, "wipe1")
	require.NoError(t, os.MkdirAll(stingerDir, 0755))

	// Create 3 test PNG frames with alpha
	createTestPNG(t, stingerDir, "001.png", 8, 8, color.NRGBA{R: 255, A: 128})
	createTestPNG(t, stingerDir, "002.png", 8, 8, color.NRGBA{R: 255, A: 255})
	createTestPNG(t, stingerDir, "003.png", 8, 8, color.NRGBA{R: 255, A: 0})

	store, err := NewStingerStore(dir, 0)
	require.NoError(t, err)

	names := store.List()
	require.Equal(t, []string{"wipe1"}, names)

	clip, ok := store.Get("wipe1")
	require.True(t, ok)
	require.Equal(t, "wipe1", clip.Name)
	require.Equal(t, 3, len(clip.Frames))
	require.Equal(t, 8, clip.Width)
	require.Equal(t, 8, clip.Height)
	require.InDelta(t, 0.5, clip.CutPoint, 0.01)
}

func TestStingerStore_Delete(t *testing.T) {
	dir := t.TempDir()
	stingerDir := filepath.Join(dir, "wipe1")
	require.NoError(t, os.MkdirAll(stingerDir, 0755))
	createTestPNG(t, stingerDir, "001.png", 4, 4, color.NRGBA{A: 255})

	store, err := NewStingerStore(dir, 0)
	require.NoError(t, err)
	require.Equal(t, 1, len(store.List()))

	err = store.Delete("wipe1")
	require.NoError(t, err)
	require.Equal(t, 0, len(store.List()))
	require.NoDirExists(t, stingerDir)
}

func TestStingerStore_DeleteNotFound(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStingerStore(dir, 0)
	require.NoError(t, err)

	err = store.Delete("nonexistent")
	require.Error(t, err)
}

func TestStingerStore_SetCutPoint(t *testing.T) {
	dir := t.TempDir()
	stingerDir := filepath.Join(dir, "wipe1")
	require.NoError(t, os.MkdirAll(stingerDir, 0755))
	createTestPNG(t, stingerDir, "001.png", 4, 4, color.NRGBA{A: 255})

	store, err := NewStingerStore(dir, 0)
	require.NoError(t, err)

	err = store.SetCutPoint("wipe1", 0.75)
	require.NoError(t, err)
	clip, _ := store.Get("wipe1")
	require.InDelta(t, 0.75, clip.CutPoint, 0.01)

	// Invalid range
	require.Error(t, store.SetCutPoint("wipe1", -0.1))
	require.Error(t, store.SetCutPoint("wipe1", 1.1))
}

func TestStingerClip_FrameAt(t *testing.T) {
	clip := &StingerClip{
		Frames: make([]StingerFrame, 10),
	}
	for i := range clip.Frames {
		clip.Frames[i] = StingerFrame{
			YUV:   []byte{byte(i)},
			Alpha: []byte{byte(i)},
		}
	}

	// Position 0.0 -> frame 0
	require.Equal(t, byte(0), clip.FrameAt(0.0).YUV[0])
	// Position 0.5 -> frame 5
	require.Equal(t, byte(5), clip.FrameAt(0.5).YUV[0])
	// Position 0.99 -> frame 9
	require.Equal(t, byte(9), clip.FrameAt(0.99).YUV[0])
	// Position 1.0 -> frame 9 (clamped)
	require.Equal(t, byte(9), clip.FrameAt(1.0).YUV[0])
}

func TestStingerClip_FrameAtEmpty(t *testing.T) {
	clip := &StingerClip{}
	require.Nil(t, clip.FrameAt(0.5))
}

func TestRGBAToStingerFrame(t *testing.T) {
	// Create a 4x4 red image with 50% alpha
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 255, G: 0, B: 0, A: 128})
		}
	}

	frame := RGBAToStingerFrame(img, 4, 4)
	require.Len(t, frame.Alpha, 16)   // 4x4
	require.Len(t, frame.YUV, 16+4+4) // Y(16) + Cb(4) + Cr(4)

	// Alpha should be 128 everywhere
	for i := 0; i < 16; i++ {
		require.Equal(t, byte(128), frame.Alpha[i])
	}

	// Y should be non-zero (red has luma)
	require.True(t, frame.YUV[0] > 0, "Y should be positive for red")
}

func TestStingerStore_MismatchedDimensions(t *testing.T) {
	dir := t.TempDir()
	stingerDir := filepath.Join(dir, "bad")
	require.NoError(t, os.MkdirAll(stingerDir, 0755))
	createTestPNG(t, stingerDir, "001.png", 8, 8, color.NRGBA{A: 255})
	createTestPNG(t, stingerDir, "002.png", 4, 4, color.NRGBA{A: 255})

	store, err := NewStingerStore(dir, 0)
	require.NoError(t, err)
	// Should not have loaded the bad stinger
	_, ok := store.Get("bad")
	require.False(t, ok)
}

func TestStingerStore_OddDimensions(t *testing.T) {
	dir := t.TempDir()
	stingerDir := filepath.Join(dir, "odd")
	require.NoError(t, os.MkdirAll(stingerDir, 0755))
	createTestPNG(t, stingerDir, "001.png", 3, 3, color.NRGBA{A: 255})

	store, err := NewStingerStore(dir, 0)
	require.NoError(t, err)
	// Should not have loaded (odd dimensions)
	_, ok := store.Get("odd")
	require.False(t, ok)
}

func TestStingerStore_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	stingerDir := filepath.Join(dir, "empty")
	require.NoError(t, os.MkdirAll(stingerDir, 0755))

	store, err := NewStingerStore(dir, 0)
	require.NoError(t, err)
	// Should not have loaded (no PNGs)
	_, ok := store.Get("empty")
	require.False(t, ok)
}

func TestStingerStore_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStingerStore(dir, 0)
	require.NoError(t, err)

	// These names should all be rejected
	badNames := []string{
		"../etc",
		"../../etc/passwd",
		"..",
		".",
		"a/b",
		"a\\b",
		"",
	}
	for _, name := range badNames {
		name := name // capture range variable
		t.Run("Delete_"+name, func(t *testing.T) {
			err := store.Delete(name)
			require.Error(t, err)
			require.True(t, errors.Is(err, ErrInvalidName) || errors.Is(err, ErrNotFound),
				"expected ErrInvalidName or ErrNotFound, got: %v", err)
		})
		t.Run("SetCutPoint_"+name, func(t *testing.T) {
			err := store.SetCutPoint(name, 0.5)
			require.Error(t, err)
		})
		t.Run("LoadFromDir_"+name, func(t *testing.T) {
			err := store.LoadFromDir(name)
			require.Error(t, err)
		})
	}
}

func TestStingerStore_SentinelErrors(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStingerStore(dir, 0)
	require.NoError(t, err)

	// Delete non-existent should return ErrNotFound
	err = store.Delete("nonexistent")
	require.ErrorIs(t, err, ErrNotFound)

	// SetCutPoint non-existent should return ErrNotFound
	err = store.SetCutPoint("nonexistent", 0.5)
	require.ErrorIs(t, err, ErrNotFound)

	// SetCutPoint invalid range should return ErrInvalidCutPoint
	// Need a clip first - we can't test this without a valid clip loaded
}

func TestStingerStore_Upload(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStingerStore(dir, 0)
	require.NoError(t, err)

	// Create a zip with a small 2x2 PNG
	zipData := createTestZip(t, 2, 2, 3) // 3 frames

	err = store.Upload("test-stinger", zipData)
	require.NoError(t, err)

	// Verify clip was loaded
	clip, ok := store.Get("test-stinger")
	require.True(t, ok)
	require.Equal(t, 3, len(clip.Frames))
	require.Equal(t, 2, clip.Width)
	require.Equal(t, 2, clip.Height)

	// Double upload should fail
	err = store.Upload("test-stinger", zipData)
	require.ErrorIs(t, err, ErrAlreadyExists)

	// Invalid name should fail
	err = store.Upload("../bad", zipData)
	require.Error(t, err)
}

// createTestZip creates a zip file containing numFrames PNG images of the given dimensions.
func createTestZip(t *testing.T, width, height, numFrames int) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	for i := 0; i < numFrames; i++ {
		img := image.NewRGBA(image.Rect(0, 0, width, height))
		// Fill with a color
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				img.SetRGBA(x, y, color.RGBA{R: uint8(i * 50), G: 128, B: 128, A: 255})
			}
		}

		name := fmt.Sprintf("frame_%03d.png", i)
		fw, err := zw.Create(name)
		require.NoError(t, err)
		require.NoError(t, png.Encode(fw, img))
	}

	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func TestStingerStore_MaxClipsLimit(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStingerStore(dir, 2) // limit to 2 clips
	require.NoError(t, err)

	// Create 3 stinger directories with small even-dimension PNGs
	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("clip%d", i)
		clipDir := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(clipDir, 0755))
		createTestPNG(t, clipDir, "frame_000.png", 2, 2, color.NRGBA{R: 128, A: 255})
	}

	// First two loads should succeed
	require.NoError(t, store.LoadFromDir("clip0"))
	require.NoError(t, store.LoadFromDir("clip1"))

	// Third should fail with ErrMaxClipsReached
	err = store.LoadFromDir("clip2")
	require.ErrorIs(t, err, ErrMaxClipsReached)

	// Reloading an existing clip (replacement) should succeed even at limit
	require.NoError(t, store.LoadFromDir("clip0"))

	// Verify we still have exactly 2 clips
	require.Equal(t, 2, len(store.List()))
}

func TestStingerStore_MaxClipsLimitUpload(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStingerStore(dir, 2) // limit to 2 clips
	require.NoError(t, err)

	zipData := createTestZip(t, 2, 2, 1) // 1 frame

	require.NoError(t, store.Upload("clip0", zipData))
	require.NoError(t, store.Upload("clip1", zipData))

	// Third upload should fail with ErrMaxClipsReached
	err = store.Upload("clip2", zipData)
	require.ErrorIs(t, err, ErrMaxClipsReached)

	// Verify we still have exactly 2 clips
	require.Equal(t, 2, len(store.List()))
}

func TestStingerStore_DefaultMaxClips(t *testing.T) {
	dir := t.TempDir()
	// maxClips <= 0 should default to 16
	store, err := NewStingerStore(dir, 0)
	require.NoError(t, err)
	require.Equal(t, 16, store.maxClips)
}

// createTestWAV creates a WAV file with silence (zero samples) in the given directory.
func createTestWAV(t *testing.T, dir, name string, sampleRate, channels, numSamples int) {
	t.Helper()
	// Create int16 silence samples
	samples := make([]byte, numSamples*channels*2) // int16 = 2 bytes per sample per channel
	wavData := buildWAV(t, sampleRate, channels, 16, samples)
	err := os.WriteFile(filepath.Join(dir, name), wavData, 0644)
	require.NoError(t, err)
}

// createTestZipWithAudio creates a ZIP with PNGs + a WAV file.
func createTestZipWithAudio(t *testing.T, width, height, numFrames, sampleRate, channels int) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// Add PNG frames
	for i := 0; i < numFrames; i++ {
		img := image.NewRGBA(image.Rect(0, 0, width, height))
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				img.SetRGBA(x, y, color.RGBA{R: uint8(i * 50), G: 128, B: 128, A: 255})
			}
		}
		name := fmt.Sprintf("frame_%03d.png", i)
		fw, err := zw.Create(name)
		require.NoError(t, err)
		require.NoError(t, png.Encode(fw, img))
	}

	// Add a WAV file with some silence
	numSamples := 480 // ~10ms at 48kHz
	samples := make([]byte, numSamples*channels*2)
	wavData := buildWAV(t, sampleRate, channels, 16, samples)
	fw, err := zw.Create("stinger.wav")
	require.NoError(t, err)
	_, err = fw.Write(wavData)
	require.NoError(t, err)

	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func TestStingerStore_UploadWithAudio(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStingerStore(dir, 0)
	require.NoError(t, err)

	// Create a zip with PNGs + WAV
	zipData := createTestZipWithAudio(t, 2, 2, 3, 48000, 2)

	err = store.Upload("audio-stinger", zipData)
	require.NoError(t, err)

	// Verify clip was loaded with audio fields populated
	clip, ok := store.Get("audio-stinger")
	require.True(t, ok)
	require.Equal(t, 3, len(clip.Frames))
	require.Equal(t, 2, clip.Width)
	require.Equal(t, 2, clip.Height)
	require.NotNil(t, clip.Audio, "clip should have audio data")
	require.Equal(t, 48000, clip.AudioSampleRate)
	require.Equal(t, 2, clip.AudioChannels)
	require.Greater(t, len(clip.Audio), 0, "audio samples should not be empty")

	// Verify WAV file was written to disk
	wavPath := filepath.Join(dir, "audio-stinger", "stinger.wav")
	require.FileExists(t, wavPath)
}

func TestStingerStore_UploadWithoutAudio(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStingerStore(dir, 0)
	require.NoError(t, err)

	// Create a zip with only PNGs (no WAV)
	zipData := createTestZip(t, 2, 2, 3)

	err = store.Upload("no-audio-stinger", zipData)
	require.NoError(t, err)

	// Verify clip was loaded without audio
	clip, ok := store.Get("no-audio-stinger")
	require.True(t, ok)
	require.Equal(t, 3, len(clip.Frames))
	require.Nil(t, clip.Audio, "clip should have no audio data")
	require.Equal(t, 0, clip.AudioSampleRate)
	require.Equal(t, 0, clip.AudioChannels)
}

func TestStingerStore_LoadFromDirWithAudio(t *testing.T) {
	dir := t.TempDir()
	stingerDir := filepath.Join(dir, "audio-clip")
	require.NoError(t, os.MkdirAll(stingerDir, 0755))

	// Create PNG frames on disk
	createTestPNG(t, stingerDir, "001.png", 4, 4, color.NRGBA{R: 255, A: 128})
	createTestPNG(t, stingerDir, "002.png", 4, 4, color.NRGBA{R: 255, A: 255})

	// Create a WAV file on disk
	createTestWAV(t, stingerDir, "audio.wav", 48000, 2, 960)

	// Create store - should auto-load the clip with audio
	store, err := NewStingerStore(dir, 0)
	require.NoError(t, err)

	clip, ok := store.Get("audio-clip")
	require.True(t, ok)
	require.Equal(t, 2, len(clip.Frames))
	require.Equal(t, 4, clip.Width)
	require.Equal(t, 4, clip.Height)

	// Audio should have been loaded
	require.NotNil(t, clip.Audio, "clip should have audio data from WAV")
	require.Equal(t, 48000, clip.AudioSampleRate)
	require.Equal(t, 2, clip.AudioChannels)
	// 960 samples * 2 channels = 1920 float32 values
	require.Equal(t, 960*2, len(clip.Audio))
}
