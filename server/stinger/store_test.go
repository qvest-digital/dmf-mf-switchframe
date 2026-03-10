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

func TestRGBAToStingerFrame_LimitedRangeWhite(t *testing.T) {
	// White (255,255,255) should produce Y=235 in limited-range BT.709, not Y=255.
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}

	frame := RGBAToStingerFrame(img, 4, 4)

	// Every luma sample should be 235 (limited-range white)
	for i := 0; i < 16; i++ {
		require.Equal(t, byte(235), frame.YUV[i],
			"pixel %d: Y should be 235 (limited-range white), got %d", i, frame.YUV[i])
	}
}

func TestRGBAToStingerFrame_LimitedRangeBlack(t *testing.T) {
	// Black (0,0,0) should produce Y=16 in limited-range BT.709, not Y=0.
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 0, G: 0, B: 0, A: 255})
		}
	}

	frame := RGBAToStingerFrame(img, 4, 4)

	// Every luma sample should be 16 (limited-range black)
	for i := 0; i < 16; i++ {
		require.Equal(t, byte(16), frame.YUV[i],
			"pixel %d: Y should be 16 (limited-range black), got %d", i, frame.YUV[i])
	}
}

func TestRGBAToStingerFrame_SemiTransparentUnpremultiply(t *testing.T) {
	// Regression test: Go's img.At(x,y).RGBA() returns pre-multiplied alpha.
	// If we don't un-premultiply before YUV conversion, semi-transparent pixels
	// get darkened YUV values, and then alpha is applied again during compositing,
	// causing double-darkening (dark halos on semi-transparent stinger edges).
	//
	// A pure red pixel (R=255) at alpha=128 should produce the same Y luma
	// as a pure red pixel at alpha=255. Only the alpha plane should differ.
	imgOpaque := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	imgSemiTrans := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			imgOpaque.SetNRGBA(x, y, color.NRGBA{R: 255, G: 0, B: 0, A: 255})
			imgSemiTrans.SetNRGBA(x, y, color.NRGBA{R: 255, G: 0, B: 0, A: 128})
		}
	}

	frameOpaque := RGBAToStingerFrame(imgOpaque, 4, 4)
	frameSemiTrans := RGBAToStingerFrame(imgSemiTrans, 4, 4)

	// Alpha planes must differ
	require.Equal(t, byte(255), frameOpaque.Alpha[0])
	require.Equal(t, byte(128), frameSemiTrans.Alpha[0])

	// Y (luma) values must be identical — the color is the same, only alpha differs.
	// With the bug (no un-premultiply), the semi-transparent Y would be ~35 instead of ~63.
	require.Equal(t, frameOpaque.YUV[0], frameSemiTrans.YUV[0],
		"Y luma must be identical regardless of alpha; opaque=%d semi=%d",
		frameOpaque.YUV[0], frameSemiTrans.YUV[0])

	// Chroma (Cb, Cr) must also be identical
	ySize := 4 * 4
	uvSize := 2 * 2
	require.Equal(t, frameOpaque.YUV[ySize], frameSemiTrans.YUV[ySize],
		"Cb must be identical regardless of alpha")
	require.Equal(t, frameOpaque.YUV[ySize+uvSize], frameSemiTrans.YUV[ySize+uvSize],
		"Cr must be identical regardless of alpha")
}

func TestRGBAToStingerFrame_FullyTransparentPixel(t *testing.T) {
	// Fully transparent pixels (alpha=0) should produce black YUV (Y=16 limited range).
	// This prevents division by zero in the un-premultiply path.
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 255, G: 128, B: 64, A: 0})
		}
	}

	frame := RGBAToStingerFrame(img, 4, 4)

	// Alpha should be 0
	require.Equal(t, byte(0), frame.Alpha[0])
	// Y should be limited-range black (16)
	require.Equal(t, byte(16), frame.YUV[0],
		"fully transparent pixel should have Y=16 (limited-range black)")
}

func BenchmarkRGBAToStingerFrame_1080p(b *testing.B) {
	w, h := 1920, 1080
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	// Fill with semi-transparent red to exercise the un-premultiply path
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 200, G: 50, B: 100, A: 180})
		}
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		RGBAToStingerFrame(img, w, h)
	}
}

func TestRGBAToStingerFrame_LimitedRangeRed(t *testing.T) {
	// Pure red (255,0,0) in limited-range BT.709:
	//   Y  = 16 + 219 * 0.2126 ≈ 62.6 → 63
	//   Cb = 128 + 224 * (0.0 - 0.2126) / 1.8556 ≈ 128 + 224*(-0.1146) ≈ 102.3 → 102
	//   Cr = 128 + 224 * (1.0 - 0.2126) / 1.5748 ≈ 128 + 224*(0.5000) ≈ 240.0 → 240
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 255, G: 0, B: 0, A: 255})
		}
	}

	frame := RGBAToStingerFrame(img, 4, 4)

	// Check luma
	require.InDelta(t, 63, int(frame.YUV[0]), 1,
		"Y for red should be ~63, got %d", frame.YUV[0])

	// Check chroma (Cb and Cr are at offsets ySize and ySize+uvSize)
	ySize := 4 * 4    // 16
	uvSize := 2 * 2   // 4
	cbOffset := ySize  // 16
	crOffset := ySize + uvSize // 20

	require.InDelta(t, 102, int(frame.YUV[cbOffset]), 1,
		"Cb for red should be ~102, got %d", frame.YUV[cbOffset])
	require.InDelta(t, 240, int(frame.YUV[crOffset]), 1,
		"Cr for red should be ~240, got %d", frame.YUV[crOffset])
}
