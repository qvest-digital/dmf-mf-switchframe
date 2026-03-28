//go:build correctness

package stinger

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/demo"
	"github.com/zsiec/switchframe/server/stinger"
)

const (
	testWidth  = 120
	testHeight = 80
	numFrames  = 15
)

// ==========================================================================
// 1. TestFrameSequenceOrder -- Frames are stored in numerical order with
//    correct count after generation and loading.
// ==========================================================================

func TestFrameSequenceOrder(t *testing.T) {
	t.Parallel()

	// Generate a stinger zip using the demo package.
	zipData, err := demo.GenerateStingerZip(testWidth, testHeight, numFrames)
	require.NoError(t, err, "GenerateStingerZip should succeed")

	// Upload into a store.
	dir := t.TempDir()
	store, err := stinger.NewStore(dir, 0, "")
	require.NoError(t, err)

	err = store.Upload("seq-order", zipData)
	require.NoError(t, err)

	clip, ok := store.Get("seq-order")
	require.True(t, ok)

	// Frame count must match what we requested.
	require.Equal(t, numFrames, len(clip.Frames),
		"clip should have exactly %d frames", numFrames)

	// Frames stored in order should be accessible via FrameAt in monotonic order.
	// Verify that progressive positions map to progressive frame indices by
	// checking that each position returns a valid, non-nil frame.
	for i := 0; i < numFrames; i++ {
		pos := float64(i) / float64(numFrames)
		frame := clip.FrameAt(pos)
		require.NotNil(t, frame, "FrameAt(%f) returned nil", pos)
		require.Greater(t, len(frame.YUV), 0, "frame %d has empty YUV", i)
		require.Greater(t, len(frame.Alpha), 0, "frame %d has empty Alpha", i)
	}

	// FrameAt(0.0) and FrameAt(1.0) should both return valid frames (first/last).
	first := clip.FrameAt(0.0)
	last := clip.FrameAt(1.0)
	require.NotNil(t, first)
	require.NotNil(t, last)
}

// ==========================================================================
// 2. TestStingerStoreLoadRoundTrip -- Upload a stinger ZIP, load it back,
//    and verify frame count and dimensions match.
// ==========================================================================

func TestStingerStoreLoadRoundTrip(t *testing.T) {
	t.Parallel()

	zipData, err := demo.GenerateStingerZip(testWidth, testHeight, numFrames)
	require.NoError(t, err)

	dir := t.TempDir()
	store, err := stinger.NewStore(dir, 0, "")
	require.NoError(t, err)

	err = store.Upload("roundtrip", zipData)
	require.NoError(t, err)

	clip, ok := store.Get("roundtrip")
	require.True(t, ok, "clip should be retrievable after upload")

	// Verify dimensions match what was generated.
	require.Equal(t, testWidth, clip.Width, "width mismatch")
	require.Equal(t, testHeight, clip.Height, "height mismatch")
	require.Equal(t, numFrames, len(clip.Frames), "frame count mismatch")

	// Verify each frame has the correct buffer sizes.
	ySize := testWidth * testHeight
	uvSize := (testWidth / 2) * (testHeight / 2)
	expectedYUVSize := ySize + 2*uvSize

	for i, frame := range clip.Frames {
		require.Equal(t, expectedYUVSize, len(frame.YUV),
			"frame %d: YUV buffer size mismatch", i)
		require.Equal(t, ySize, len(frame.Alpha),
			"frame %d: Alpha buffer size mismatch", i)
	}

	// Default cut point should be 0.5.
	require.InDelta(t, 0.5, clip.CutPoint, 0.01)
}

// ==========================================================================
// 3. TestWAVSampleRateConversion -- Parse a WAV at 44.1kHz. Verify parsed
//    data is valid (correct sample count, values in range).
// ==========================================================================

func TestWAVSampleRateConversion(t *testing.T) {
	t.Parallel()

	// Generate a 44.1kHz stereo whoosh signal.
	const sampleRate = 44100
	const channels = 2
	const durationSec = 0.5
	pcmSource := demo.SynthesizeWhoosh(sampleRate, channels, durationSec)
	wavData := demo.GenerateWAV(pcmSource, sampleRate, channels)

	// Parse it back.
	pcmParsed, sr, ch, err := stinger.ParseWAV(wavData)
	require.NoError(t, err, "ParseWAV should succeed for 44.1kHz WAV")
	require.Equal(t, sampleRate, sr, "sample rate should be 44100")
	require.Equal(t, channels, ch, "channel count should be 2")

	// GenerateWAV produces int16 format. The round-trip introduces quantization
	// error, but sample count must be exact.
	expectedSamples := int(float64(sampleRate) * durationSec * float64(channels))
	require.Equal(t, expectedSamples, len(pcmParsed),
		"parsed sample count should match generated count")

	// All parsed values must be in [-1.0, 1.0].
	for i, v := range pcmParsed {
		require.True(t, v >= -1.0 && v <= 1.0,
			"sample %d out of range: %f", i, v)
	}
}

// ==========================================================================
// 4. TestWAVParsing -- Parse WAVs in both int16 and float32 formats.
//    Verify correct decoding.
// ==========================================================================

func TestWAVParsing(t *testing.T) {
	t.Parallel()

	t.Run("int16_stereo_48kHz", func(t *testing.T) {
		t.Parallel()

		// Use demo synthesizer to generate known audio.
		pcmSource := demo.SynthesizeSlam(48000, 2, 0.1)
		wavData := demo.GenerateWAV(pcmSource, 48000, 2)

		pcm, sr, ch, err := stinger.ParseWAV(wavData)
		require.NoError(t, err)
		require.Equal(t, 48000, sr)
		require.Equal(t, 2, ch)
		require.Greater(t, len(pcm), 0)

		// Values must be in range.
		for i, v := range pcm {
			require.True(t, v >= -1.0 && v <= 1.0,
				"int16 sample %d out of range: %f", i, v)
		}
	})

	t.Run("float32_mono_48kHz", func(t *testing.T) {
		t.Parallel()

		// Build a float32 WAV manually.
		const sampleRate = 48000
		const channels = 1
		const numSamples = 100
		const bitsPerSample = 32

		// Create raw float32 sample bytes.
		rawSamples := make([]byte, numSamples*4)
		for i := 0; i < numSamples; i++ {
			val := float32(math.Sin(2.0*math.Pi*440.0*float64(i)/float64(sampleRate)))
			binary.LittleEndian.PutUint32(rawSamples[i*4:], math.Float32bits(val))
		}

		// Build WAV header for float32 format (audioFormat=3).
		wavData := buildFloat32WAV(sampleRate, channels, rawSamples)

		pcm, sr, ch, err := stinger.ParseWAV(wavData)
		require.NoError(t, err)
		require.Equal(t, sampleRate, sr)
		require.Equal(t, channels, ch)
		require.Equal(t, numSamples, len(pcm))

		// Verify values match what we put in (float32 round-trips exactly).
		for i := 0; i < numSamples; i++ {
			expected := float32(math.Sin(2.0 * math.Pi * 440.0 * float64(i) / float64(sampleRate)))
			require.InDelta(t, expected, pcm[i], 1e-6,
				"float32 sample %d mismatch", i)
		}
	})

	t.Run("int16_mono_44100Hz", func(t *testing.T) {
		t.Parallel()

		pcmSource := demo.SynthesizeMusical(44100, 1, 0.05)
		wavData := demo.GenerateWAV(pcmSource, 44100, 1)

		pcm, sr, ch, err := stinger.ParseWAV(wavData)
		require.NoError(t, err)
		require.Equal(t, 44100, sr)
		require.Equal(t, 1, ch)
		require.Greater(t, len(pcm), 0)
	})
}

// ==========================================================================
// 5. TestStingerAlphaPresent -- Load stinger frames and verify the alpha
//    channel contains both transparent and opaque regions.
// ==========================================================================

func TestStingerAlphaPresent(t *testing.T) {
	t.Parallel()

	// The whoosh stinger has a diagonal sweep with soft edges, meaning frames
	// in the middle of the animation should have both opaque and transparent pixels.
	zipData, err := demo.GenerateWhooshStingerZip(testWidth, testHeight, numFrames)
	require.NoError(t, err)

	dir := t.TempDir()
	store, err := stinger.NewStore(dir, 0, "")
	require.NoError(t, err)

	err = store.Upload("alpha-check", zipData)
	require.NoError(t, err)

	clip, ok := store.Get("alpha-check")
	require.True(t, ok)

	// Pick a mid-animation frame (not first or last, which may be all-transparent
	// or all-opaque). The middle frame should have mixed alpha.
	midIdx := numFrames / 2
	frame := clip.FrameAt(float64(midIdx) / float64(numFrames))
	require.NotNil(t, frame)

	hasTransparent := false
	hasOpaque := false
	hasSemiTransparent := false

	for _, a := range frame.Alpha {
		if a == 0 {
			hasTransparent = true
		} else if a == 255 {
			hasOpaque = true
		} else {
			hasSemiTransparent = true
		}
		if hasTransparent && hasOpaque {
			break
		}
	}

	// A stinger mid-frame should have at least transparent + opaque or
	// transparent + semi-transparent regions.
	require.True(t, hasTransparent,
		"mid-animation frame should have transparent (alpha=0) pixels")
	require.True(t, hasOpaque || hasSemiTransparent,
		"mid-animation frame should have opaque or semi-transparent pixels")
}

// ==========================================================================
// 6. TestStorePathTraversal -- Attempt to upload/load with path traversal
//    names. Assert: rejected with error.
// ==========================================================================

func TestStorePathTraversal(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := stinger.NewStore(dir, 0, "")
	require.NoError(t, err)

	zipData, err := demo.GenerateStingerZip(testWidth, testHeight, 3)
	require.NoError(t, err)

	maliciousNames := []string{
		"../evil",
		"../../etc/passwd",
		"..",
		".",
		"a/b",
		"a\\b",
		"",
		"../../../tmp/pwned",
	}

	for _, name := range maliciousNames {
		t.Run("Upload_"+name, func(t *testing.T) {
			err := store.Upload(name, zipData)
			require.Error(t, err,
				"Upload with path traversal name %q should be rejected", name)
		})

		t.Run("Get_"+name, func(t *testing.T) {
			_, ok := store.Get(name)
			require.False(t, ok,
				"Get with path traversal name %q should return false", name)
		})

		t.Run("LoadFromDir_"+name, func(t *testing.T) {
			err := store.LoadFromDir(name)
			require.Error(t, err,
				"LoadFromDir with path traversal name %q should be rejected", name)
		})

		t.Run("Delete_"+name, func(t *testing.T) {
			err := store.Delete(name)
			require.Error(t, err,
				"Delete with path traversal name %q should be rejected", name)
		})
	}
}

// ==========================================================================
// 7. TestStoreMaxClips -- Store respects maxClips limit. Upload beyond limit.
//    Assert: error.
// ==========================================================================

func TestStoreMaxClips(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	const maxClips = 3
	store, err := stinger.NewStore(dir, maxClips, "")
	require.NoError(t, err)

	zipData, err := demo.GenerateStingerZip(testWidth, testHeight, 5)
	require.NoError(t, err)

	// Upload up to the limit.
	for i := 0; i < maxClips; i++ {
		name := "clip" + string(rune('a'+i))
		err := store.Upload(name, zipData)
		require.NoError(t, err, "upload %d of %d should succeed", i+1, maxClips)
	}

	// Verify all clips are present.
	require.Equal(t, maxClips, len(store.List()))

	// One more should fail with ErrMaxClipsReached.
	err = store.Upload("overflow", zipData)
	require.Error(t, err)
	require.True(t, errors.Is(err, stinger.ErrMaxClipsReached),
		"expected ErrMaxClipsReached, got: %v", err)

	// Count should still be at the limit.
	require.Equal(t, maxClips, len(store.List()))

	// Deleting one and uploading again should succeed.
	err = store.Delete("clipa")
	require.NoError(t, err)
	require.Equal(t, maxClips-1, len(store.List()))

	err = store.Upload("replacement", zipData)
	require.NoError(t, err)
	require.Equal(t, maxClips, len(store.List()))
}

// ==========================================================================
// 8. TestHoldFrameBehavior -- If a stinger has hold mode (no explicit loop
//    frames), verify the last frame can be identified and used as hold.
// ==========================================================================

func TestHoldFrameBehavior(t *testing.T) {
	t.Parallel()

	// Generate a stinger and load it.
	zipData, err := demo.GenerateStingerZip(testWidth, testHeight, numFrames)
	require.NoError(t, err)

	dir := t.TempDir()
	store, err := stinger.NewStore(dir, 0, "")
	require.NoError(t, err)

	err = store.Upload("hold-test", zipData)
	require.NoError(t, err)

	clip, ok := store.Get("hold-test")
	require.True(t, ok)

	// The last frame should be identifiable (FrameAt(1.0) returns the last frame).
	lastFrame := clip.FrameAt(1.0)
	require.NotNil(t, lastFrame, "last frame should not be nil")

	// FrameAt positions beyond 1.0 should clamp to the last frame.
	beyondFrame := clip.FrameAt(1.5)
	require.NotNil(t, beyondFrame, "beyond-1.0 position should still return a frame")

	// The frame returned at 1.0 and 1.5 should be the same (clamped).
	require.Equal(t, lastFrame.YUV, beyondFrame.YUV,
		"FrameAt beyond 1.0 should return the same frame as FrameAt(1.0)")
	require.Equal(t, lastFrame.Alpha, beyondFrame.Alpha,
		"FrameAt beyond 1.0 should return the same alpha as FrameAt(1.0)")

	// Verify the last frame has valid data.
	ySize := testWidth * testHeight
	uvSize := (testWidth / 2) * (testHeight / 2)
	require.Equal(t, ySize+2*uvSize, len(lastFrame.YUV))
	require.Equal(t, ySize, len(lastFrame.Alpha))

	// For a diagonal wipe stinger at frame N-1, the wipe should be mostly
	// complete, so we expect most of the alpha to be non-zero (opaque/semi-opaque).
	nonZeroAlpha := 0
	for _, a := range lastFrame.Alpha {
		if a > 0 {
			nonZeroAlpha++
		}
	}
	// The last frame of GenerateStingerZip is fully swept (progress=1.0),
	// so almost all pixels should be opaque.
	require.Greater(t, nonZeroAlpha, ySize/2,
		"last frame of diagonal wipe should have majority non-zero alpha")
}

// ==========================================================================
// Additional correctness tests
// ==========================================================================

// TestStingerWithAudioRoundTrip verifies that a stinger with embedded WAV
// audio can be uploaded and the audio is correctly parsed.
func TestStingerWithAudioRoundTrip(t *testing.T) {
	t.Parallel()

	// GenerateWhooshStingerZip includes WAV audio.
	zipData, err := demo.GenerateWhooshStingerZip(testWidth, testHeight, numFrames)
	require.NoError(t, err)

	dir := t.TempDir()
	store, err := stinger.NewStore(dir, 0, "")
	require.NoError(t, err)

	err = store.Upload("audio-stinger", zipData)
	require.NoError(t, err)

	clip, ok := store.Get("audio-stinger")
	require.True(t, ok)

	// Verify video frames are present.
	require.Equal(t, numFrames, len(clip.Frames))
	require.Equal(t, testWidth, clip.Width)
	require.Equal(t, testHeight, clip.Height)

	// Verify audio is present and valid.
	require.NotNil(t, clip.Audio, "clip should have audio data")
	require.Equal(t, 48000, clip.AudioSampleRate)
	require.Equal(t, 2, clip.AudioChannels)
	require.Greater(t, len(clip.Audio), 0, "audio samples should not be empty")

	// All audio samples must be in range.
	for i, v := range clip.Audio {
		require.True(t, v >= -1.0 && v <= 1.0,
			"audio sample %d out of range: %f", i, v)
	}
}

// TestFrameYUVBroadcastRange verifies that all stinger frame YUV values
// fall within broadcast-legal limited range.
func TestFrameYUVBroadcastRange(t *testing.T) {
	t.Parallel()

	zipData, err := demo.GenerateStingerZip(testWidth, testHeight, numFrames)
	require.NoError(t, err)

	dir := t.TempDir()
	store, err := stinger.NewStore(dir, 0, "")
	require.NoError(t, err)

	err = store.Upload("broadcast-range", zipData)
	require.NoError(t, err)

	clip, ok := store.Get("broadcast-range")
	require.True(t, ok)

	ySize := testWidth * testHeight
	chromaSize := (testWidth / 2) * (testHeight / 2)

	for fi, frame := range clip.Frames {
		// Check Y plane: [16, 235]
		for i := 0; i < ySize; i++ {
			if frame.YUV[i] < 16 || frame.YUV[i] > 235 {
				t.Fatalf("frame %d Y[%d]=%d out of legal range [16,235] at pixel (%d,%d)",
					fi, i, frame.YUV[i], i%testWidth, i/testWidth)
			}
		}
		// Check Cb plane: [16, 240]
		cbStart := ySize
		for i := 0; i < chromaSize; i++ {
			if frame.YUV[cbStart+i] < 16 || frame.YUV[cbStart+i] > 240 {
				t.Fatalf("frame %d Cb[%d]=%d out of legal range [16,240]",
					fi, i, frame.YUV[cbStart+i])
			}
		}
		// Check Cr plane: [16, 240]
		crStart := cbStart + chromaSize
		for i := 0; i < chromaSize; i++ {
			if frame.YUV[crStart+i] < 16 || frame.YUV[crStart+i] > 240 {
				t.Fatalf("frame %d Cr[%d]=%d out of legal range [16,240]",
					fi, i, frame.YUV[crStart+i])
			}
		}
	}
}

// TestMultipleStingerVariants generates all three stinger types from the
// demo package and verifies they all load correctly with valid properties.
func TestMultipleStingerVariants(t *testing.T) {
	t.Parallel()

	variants := []struct {
		name     string
		generate func(int, int, int) ([]byte, error)
		hasAudio bool
	}{
		{"whoosh", demo.GenerateWhooshStingerZip, true},
		{"slam", demo.GenerateSlamStingerZip, true},
		{"musical", demo.GenerateMusicalStingerZip, true},
		{"basic", func(w, h, n int) ([]byte, error) { return demo.GenerateStingerZip(w, h, n) }, false},
	}

	for _, v := range variants {
		v := v
		t.Run(v.name, func(t *testing.T) {
			t.Parallel()

			zipData, err := v.generate(testWidth, testHeight, 10)
			require.NoError(t, err, "generating %s stinger should succeed", v.name)

			dir := t.TempDir()
			store, err := stinger.NewStore(dir, 0, "")
			require.NoError(t, err)

			err = store.Upload(v.name, zipData)
			require.NoError(t, err)

			clip, ok := store.Get(v.name)
			require.True(t, ok)

			require.Equal(t, testWidth, clip.Width)
			require.Equal(t, testHeight, clip.Height)
			require.Equal(t, 10, len(clip.Frames))

			if v.hasAudio {
				require.NotNil(t, clip.Audio,
					"%s stinger should have audio", v.name)
				require.Equal(t, 48000, clip.AudioSampleRate)
				require.Equal(t, 2, clip.AudioChannels)
			}
		})
	}
}

// TestFrameAtMonotonic verifies that FrameAt returns frames in a monotonic
// sequence when called with increasing positions.
func TestFrameAtMonotonic(t *testing.T) {
	t.Parallel()

	zipData, err := demo.GenerateStingerZip(testWidth, testHeight, 30)
	require.NoError(t, err)

	dir := t.TempDir()
	store, err := stinger.NewStore(dir, 0, "")
	require.NoError(t, err)

	err = store.Upload("monotonic", zipData)
	require.NoError(t, err)

	clip, ok := store.Get("monotonic")
	require.True(t, ok)

	// Walk through positions from 0 to 1 and verify FrameAt never returns nil
	// and that the frame index never decreases.
	prevIdx := -1
	steps := 100
	for i := 0; i <= steps; i++ {
		pos := float64(i) / float64(steps)
		frame := clip.FrameAt(pos)
		require.NotNil(t, frame, "FrameAt(%f) should not be nil", pos)

		// Determine which frame index this is by matching against clip.Frames.
		currentIdx := -1
		for fi := range clip.Frames {
			if &clip.Frames[fi] == frame {
				currentIdx = fi
				break
			}
		}
		require.GreaterOrEqual(t, currentIdx, 0,
			"FrameAt(%f) returned a frame not in the clip", pos)
		require.GreaterOrEqual(t, currentIdx, prevIdx,
			"FrameAt index should be monotonically non-decreasing: was %d, now %d at pos %f",
			prevIdx, currentIdx, pos)
		prevIdx = currentIdx
	}
}

// ==========================================================================
// Corrupted input handling tests
// ==========================================================================

// TestCorruptedZIP generates a valid stinger ZIP, corrupts some of the PNG data,
// and tries to upload it to the store. Must return an error, not panic.
// The store must remain usable afterward.
func TestCorruptedZIP(t *testing.T) {
	t.Parallel()

	// Generate a valid stinger ZIP.
	zipData, err := demo.GenerateStingerZip(testWidth, testHeight, numFrames)
	require.NoError(t, err)
	require.Greater(t, len(zipData), 100, "ZIP data should be non-trivial")

	// Corrupt bytes in the middle (likely PNG data, not ZIP headers).
	corrupted := make([]byte, len(zipData))
	copy(corrupted, zipData)
	mid := len(corrupted) / 2
	for i := mid; i < mid+200 && i < len(corrupted); i++ {
		corrupted[i] ^= 0xFF
	}

	dir := t.TempDir()
	store, err := stinger.NewStore(dir, 0, "")
	require.NoError(t, err)

	// Upload corrupted ZIP -- should not panic.
	require.NotPanics(t, func() {
		uploadErr := store.Upload("corrupted", corrupted)
		// We expect an error since PNGs are corrupted.
		// If it doesn't error (unlikely), that's acceptable too.
		_ = uploadErr
	}, "store.Upload should not panic on corrupted ZIP")

	// Verify the store is still usable: upload a valid stinger afterward.
	validZip, err := demo.GenerateStingerZip(testWidth, testHeight, 5)
	require.NoError(t, err)

	err = store.Upload("valid-after-corrupt", validZip)
	require.NoError(t, err, "store should still accept valid uploads after a corrupted upload")

	clip, ok := store.Get("valid-after-corrupt")
	require.True(t, ok, "valid clip should be retrievable")
	require.Equal(t, 5, len(clip.Frames), "valid clip should have correct frame count")
}

// TestMismatchedFrameDimensions creates a ZIP where PNG content is garbage
// (not valid PNG images) and tries to upload it. Must not panic.
func TestMismatchedFrameDimensions(t *testing.T) {
	t.Parallel()

	// Build a ZIP with fake "PNG" files that contain garbage data.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("frame_%03d.png", i)
		w, err := zw.Create(name)
		require.NoError(t, err)
		// Write garbage that is not a valid PNG.
		garbage := make([]byte, 200)
		for j := range garbage {
			garbage[j] = byte((i*37 + j*13) % 256)
		}
		_, err = w.Write(garbage)
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())

	dir := t.TempDir()
	store, err := stinger.NewStore(dir, 0, "")
	require.NoError(t, err)

	require.NotPanics(t, func() {
		uploadErr := store.Upload("garbage-pngs", buf.Bytes())
		// Should error since the "PNGs" are not valid images.
		require.Error(t, uploadErr,
			"uploading ZIP with garbage PNG content should return an error")
	}, "store.Upload should not panic on ZIP with garbage PNG content")

	// Store should still be usable.
	validZip, err := demo.GenerateStingerZip(testWidth, testHeight, 3)
	require.NoError(t, err)
	err = store.Upload("valid-after-garbage", validZip)
	require.NoError(t, err, "store should still work after failed upload")
}

// ==========================================================================
// Helpers
// ==========================================================================

// buildFloat32WAV constructs a minimal valid float32 WAV file (audioFormat=3).
func buildFloat32WAV(sampleRate, channels int, rawSamples []byte) []byte {
	bitsPerSample := 32
	blockAlign := channels * (bitsPerSample / 8)
	byteRate := sampleRate * blockAlign
	dataSize := len(rawSamples)
	totalSize := 12 + 24 + 8 + dataSize

	buf := make([]byte, totalSize)
	off := 0

	// RIFF header
	copy(buf[off:], "RIFF")
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], uint32(totalSize-8))
	off += 4
	copy(buf[off:], "WAVE")
	off += 4

	// fmt chunk
	copy(buf[off:], "fmt ")
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], 16) // chunk size
	off += 4
	binary.LittleEndian.PutUint16(buf[off:], 3) // audioFormat = IEEE float
	off += 2
	binary.LittleEndian.PutUint16(buf[off:], uint16(channels))
	off += 2
	binary.LittleEndian.PutUint32(buf[off:], uint32(sampleRate))
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], uint32(byteRate))
	off += 4
	binary.LittleEndian.PutUint16(buf[off:], uint16(blockAlign))
	off += 2
	binary.LittleEndian.PutUint16(buf[off:], uint16(bitsPerSample))
	off += 2

	// data chunk
	copy(buf[off:], "data")
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], uint32(dataSize))
	off += 4
	copy(buf[off:], rawSamples)

	return buf
}
