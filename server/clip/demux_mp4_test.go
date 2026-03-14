package clip

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	mp4 "github.com/abema/go-mp4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requireFFmpeg checks that ffmpeg is available and skips the test if not.
func requireFFmpeg(t *testing.T) string {
	t.Helper()
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not available, skipping MP4 test")
	}
	return ffmpegPath
}

// generateTestMP4 uses ffmpeg to generate a valid MP4 file with H.264 video
// and AAC audio. numFrames determines the approximate number of video frames
// (via duration calculation at 30fps).
func generateTestMP4(t *testing.T, numFrames int) string {
	t.Helper()
	ffmpegPath := requireFFmpeg(t)

	duration := float64(numFrames) / 30.0
	if duration < 0.1 {
		duration = 0.1
	}

	mp4File := filepath.Join(t.TempDir(), "test.mp4")

	cmd := exec.Command(ffmpegPath,
		"-y",
		"-f", "lavfi", "-i", "color=c=red:size=320x240:rate=30:d="+formatFloat(duration),
		"-f", "lavfi", "-i", "sine=frequency=1000:sample_rate=48000:d="+formatFloat(duration),
		"-c:v", "libx264", "-profile:v", "baseline", "-level", "3.0",
		"-pix_fmt", "yuv420p", "-g", "30", "-bf", "0",
		"-c:a", "aac", "-b:a", "128k",
		mp4File,
	)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("ffmpeg MP4 generation failed: %v", err)
	}

	return mp4File
}

// generateTestMP4VideoOnly uses ffmpeg to generate a valid MP4 file with
// H.264 video only (no audio track).
func generateTestMP4VideoOnly(t *testing.T, numFrames int) string {
	t.Helper()
	ffmpegPath := requireFFmpeg(t)

	duration := float64(numFrames) / 30.0
	if duration < 0.1 {
		duration = 0.1
	}

	mp4File := filepath.Join(t.TempDir(), "test_video_only.mp4")

	cmd := exec.Command(ffmpegPath,
		"-y",
		"-f", "lavfi", "-i", "color=c=blue:size=320x240:rate=30:d="+formatFloat(duration),
		"-c:v", "libx264", "-profile:v", "baseline", "-level", "3.0",
		"-pix_fmt", "yuv420p", "-g", "30", "-bf", "0",
		"-an",
		mp4File,
	)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("ffmpeg MP4 generation failed: %v", err)
	}

	return mp4File
}

// formatFloat formats a float64 to a string with 2 decimal places.
func formatFloat(f float64) string {
	return fmt.Sprintf("%.2f", f)
}

func TestDemuxMP4(t *testing.T) {
	mp4File := generateTestMP4(t, 30)

	frames, audioFrames, err := DemuxFile(mp4File)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) == 0 {
		t.Fatal("expected video frames from MP4")
	}
	if !frames[0].isKeyframe {
		t.Error("first frame should be keyframe")
	}
	// Keyframe should have SPS/PPS extracted from avcC.
	assert.NotEmpty(t, frames[0].sps, "keyframe should have SPS")
	assert.NotEmpty(t, frames[0].pps, "keyframe should have PPS")

	// Wire data should be valid AVC1 format.
	require.True(t, len(frames[0].wireData) > 4, "wireData should contain NALU data")

	_ = audioFrames
}

func TestDemuxMP4NoAudio(t *testing.T) {
	mp4File := generateTestMP4VideoOnly(t, 10)

	frames, audioFrames, err := DemuxFile(mp4File)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) == 0 {
		t.Fatal("expected video frames")
	}
	if len(audioFrames) != 0 {
		t.Errorf("expected no audio, got %d frames", len(audioFrames))
	}
}

func TestDemuxMP4_PTSOrder(t *testing.T) {
	mp4File := generateTestMP4(t, 30)

	frames, audioFrames, err := DemuxFile(mp4File)
	require.NoError(t, err)
	require.True(t, len(frames) > 1, "need at least 2 frames to check order")

	for i := 1; i < len(frames); i++ {
		if frames[i].pts <= frames[i-1].pts {
			t.Errorf("Video PTS not monotonic: frame[%d].pts=%d <= frame[%d].pts=%d",
				i, frames[i].pts, i-1, frames[i-1].pts)
		}
	}

	if len(audioFrames) > 1 {
		for i := 1; i < len(audioFrames); i++ {
			if audioFrames[i].pts < audioFrames[i-1].pts {
				t.Errorf("Audio PTS not monotonic: frame[%d].pts=%d < frame[%d].pts=%d",
					i, audioFrames[i].pts, i-1, audioFrames[i-1].pts)
			}
		}
	}
}

func TestDemuxMP4_AudioFrames(t *testing.T) {
	mp4File := generateTestMP4(t, 10)

	_, audioFrames, err := DemuxFile(mp4File)
	require.NoError(t, err)
	require.NotEmpty(t, audioFrames, "expected audio frames")

	for i, af := range audioFrames {
		assert.Greater(t, af.sampleRate, 0, "frame %d: sampleRate should be > 0", i)
		assert.Greater(t, af.channels, 0, "frame %d: channels should be > 0", i)
		assert.NotEmpty(t, af.data, "frame %d: data should not be empty", i)
	}
}

func TestDemuxMP4_M4VExtension(t *testing.T) {
	mp4File := generateTestMP4(t, 5)

	// Rename to .m4v to test extension routing.
	m4vFile := filepath.Join(t.TempDir(), "test.m4v")
	data, err := os.ReadFile(mp4File)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(m4vFile, data, 0o644))

	frames, _, err := DemuxFile(m4vFile)
	require.NoError(t, err)
	require.NotEmpty(t, frames, "expected video frames from .m4v")
}

func TestDemuxMP4_MOVExtension(t *testing.T) {
	mp4File := generateTestMP4(t, 5)

	// Rename to .mov to test extension routing.
	movFile := filepath.Join(t.TempDir(), "test.mov")
	data, err := os.ReadFile(mp4File)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(movFile, data, 0o644))

	frames, _, err := DemuxFile(movFile)
	require.NoError(t, err)
	require.NotEmpty(t, frames, "expected video frames from .mov")
}

func TestDemuxMP4_InvalidFile(t *testing.T) {
	tmpFile := writeTemp(t, []byte("not an mp4 file"), ".mp4")
	_, _, err := DemuxFile(tmpFile)
	assert.Error(t, err, "expected error for invalid MP4 file")
}

func TestDemuxMP4_EmptyFile(t *testing.T) {
	tmpFile := writeTemp(t, []byte{}, ".mp4")
	_, _, err := DemuxFile(tmpFile)
	assert.Error(t, err, "expected error for empty MP4 file")
}

func TestBuildSampleTable_StscReferencesNonexistentChunk(t *testing.T) {
	// stsc references chunk 3 (FirstChunk=3) but stco has only 2 entries.
	// This is a structural inconsistency in the MP4 file.
	track := &mp4Track{
		stsz: []uint32{100, 200, 300}, // 3 samples
		stsc: []mp4.StscEntry{
			{FirstChunk: 1, SamplesPerChunk: 1}, // chunks 1-2: 1 sample each
			{FirstChunk: 3, SamplesPerChunk: 1}, // chunk 3: 1 sample — but chunk 3 doesn't exist!
		},
		stco: []uint64{0, 100}, // only 2 chunks
	}

	_, err := buildSampleTable(track)
	require.Error(t, err, "should error when stsc references chunks beyond stco")
	assert.Contains(t, err.Error(), "chunk")
}

func TestBuildSampleTable_ValidTrack(t *testing.T) {
	// All stsc references are within stco bounds — should succeed.
	track := &mp4Track{
		stsz: []uint32{100, 200, 300},
		stsc: []mp4.StscEntry{
			{FirstChunk: 1, SamplesPerChunk: 1},
			{FirstChunk: 3, SamplesPerChunk: 1},
		},
		stco: []uint64{0, 100, 300}, // 3 chunks
	}

	entries, err := buildSampleTable(track)
	require.NoError(t, err)
	require.Len(t, entries, 3)
	assert.Equal(t, uint64(0), entries[0].offset)
	assert.Equal(t, uint32(100), entries[0].size)
	assert.Equal(t, uint64(100), entries[1].offset)
	assert.Equal(t, uint32(200), entries[1].size)
	assert.Equal(t, uint64(300), entries[2].offset)
	assert.Equal(t, uint32(300), entries[2].size)
}

func TestBuildSampleTable_EmptyInputs(t *testing.T) {
	// Empty stsz/stsc/stco should return nil, no error.
	track := &mp4Track{}
	entries, err := buildSampleTable(track)
	require.NoError(t, err)
	assert.Nil(t, entries)
}

func TestReadVideoSamples_NegativeCTTSOffset(t *testing.T) {
	// CTTS v1 with signed offset that produces negative composition time.
	// DTS=100, CTTS offset=-200 → compositionTime should clamp to 0, not -100.
	track := &mp4Track{
		handlerType:    "vide",
		timescale:      90000,
		naluLengthSize: 4,
		sps:            []byte{0x67, 0x42},
		pps:            []byte{0x68, 0xce},
		stts:           []mp4.SttsEntry{{SampleCount: 1, SampleDelta: 100}},
		ctts:           []mp4.CttsEntry{{SampleCount: 1, SampleOffsetV1: -200}},
		cttsVersion:    1,
		stss:           []uint32{1}, // sample 1 is keyframe
		stsz:           []uint32{8}, // 8-byte sample
		stsc:           []mp4.StscEntry{{FirstChunk: 1, SamplesPerChunk: 1}},
		stco:           []uint64{0},
	}

	// Create a temp file with the sample data (4-byte NALU length + 4 bytes payload).
	sampleData := make([]byte, 8)
	sampleData[0] = 0x00
	sampleData[1] = 0x00
	sampleData[2] = 0x00
	sampleData[3] = 0x04 // NALU length = 4
	sampleData[4] = 0x65 // IDR NALU type
	sampleData[5] = 0x00
	sampleData[6] = 0x00
	sampleData[7] = 0x00

	tmpFile := writeTemp(t, sampleData, ".raw")
	f, err := os.Open(tmpFile)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	frames, err := readVideoSamples(f, track)
	require.NoError(t, err)
	require.Len(t, frames, 1)

	// PTS should be 0 (clamped), not negative.
	assert.GreaterOrEqual(t, frames[0].pts, int64(0),
		"PTS should be clamped to 0 when CTTS produces negative composition time")
}

// --- NALU length normalization edge case tests ---
// These verify that normalizeNALULengthSize handles truncated data and
// unsupported length sizes safely. The loop guard (pos+lengthSize <= len(data))
// prevents out-of-bounds access on truncated data, and the switch default
// case returns original data for unsupported sizes. No code changes needed.

func TestNormalizeNALULengthSize_TruncatedData(t *testing.T) {
	// 1 byte of data with lengthSize=2: loop guard prevents panic.
	data := []byte{0xFF}
	result := normalizeNALULengthSize(data, 2)
	assert.Equal(t, data, result, "should return original data when truncated")
}

func TestNormalizeNALULengthSize_UnsupportedLengthSize(t *testing.T) {
	// lengthSize=5 is not valid per AVC spec (only 1,2,3,4 allowed).
	data := []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x00, 0x00, 0x00}
	result := normalizeNALULengthSize(data, 5)
	assert.Equal(t, data, result, "should return original data for unsupported length size")
}

func TestNormalizeNALULengthSize_LengthSize1(t *testing.T) {
	// lengthSize=1: single byte length prefix → convert to 4-byte.
	data := []byte{0x03, 0x65, 0xAA, 0xBB}
	result := normalizeNALULengthSize(data, 1)
	expected := []byte{0x00, 0x00, 0x00, 0x03, 0x65, 0xAA, 0xBB}
	assert.Equal(t, expected, result)
}

func TestNormalizeNALULengthSize_LengthSize4Passthrough(t *testing.T) {
	// lengthSize=4: should return data unchanged (fast path).
	data := []byte{0x00, 0x00, 0x00, 0x03, 0x65, 0xAA, 0xBB}
	result := normalizeNALULengthSize(data, 4)
	assert.Equal(t, data, result)
}
