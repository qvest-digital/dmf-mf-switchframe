package clip

import (
	"bytes"
	"encoding/binary"
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

	_, err := buildSampleTable(track, 1<<20)
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

	entries, err := buildSampleTable(track, 1<<20)
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
	entries, err := buildSampleTable(track, 1<<20)
	require.NoError(t, err)
	assert.Nil(t, entries)
}

func TestReadVideoSamples_NegativeCTTSOffset(t *testing.T) {
	// CTTS v1 with signed offset that produces negative composition time.
	// DTS=0, CTTS offset=-200 -> PTS -200, a picture before the stream
	// starts. Clamped to 0 it would read as "no PTS" and be kept as shown.
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

	frames, err := readVideoSamples(f, track, 1<<20)
	require.NoError(t, err)
	require.Len(t, frames, 1)

	assert.Equal(t, int64(-200), frames[0].pts)
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

// TestMP4KeyframeSPSPPSIndependence verifies that each keyframe's SPS/PPS
// slices are independent copies, not shared references to the same track-level
// buffer. If they share the same underlying array, mutating one keyframe's
// SPS/PPS would corrupt all others. The TS demux path deep-copies SPS/PPS
// per keyframe; the MP4 path must do the same.
func TestMP4KeyframeSPSPPSIndependence(t *testing.T) {
	sps := []byte{0x67, 0x42, 0xC0, 0x1E, 0xD9, 0x00, 0xA0, 0x47, 0xFE, 0x88}
	pps := []byte{0x68, 0xCE, 0x38, 0x80}

	track := &mp4Track{
		handlerType:    "vide",
		timescale:      90000,
		naluLengthSize: 4,
		sps:            make([]byte, len(sps)),
		pps:            make([]byte, len(pps)),
		stts:           []mp4.SttsEntry{{SampleCount: 3, SampleDelta: 3000}},
		stss:           []uint32{1, 3}, // samples 1 and 3 are keyframes
		stsz:           []uint32{8, 8, 8},
		stsc:           []mp4.StscEntry{{FirstChunk: 1, SamplesPerChunk: 1}},
		stco:           []uint64{0, 8, 16},
	}
	copy(track.sps, sps)
	copy(track.pps, pps)

	// Create a temp file with 3 samples of 8 bytes each (4-byte NALU length + 4 bytes payload).
	var fileData []byte
	for i := 0; i < 3; i++ {
		sample := make([]byte, 8)
		sample[0] = 0x00
		sample[1] = 0x00
		sample[2] = 0x00
		sample[3] = 0x04 // NALU length = 4
		if i == 0 || i == 2 {
			sample[4] = 0x65 // IDR NALU type
		} else {
			sample[4] = 0x41 // non-IDR NALU type
		}
		sample[5] = byte(i)
		fileData = append(fileData, sample...)
	}

	tmpFile := writeTemp(t, fileData, ".raw")
	f, err := os.Open(tmpFile)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	frames, err := readVideoSamples(f, track, 1<<20)
	require.NoError(t, err)
	require.Len(t, frames, 3)

	// Frames 0 and 2 are keyframes and should have SPS/PPS.
	require.NotNil(t, frames[0].sps, "keyframe 0 should have SPS")
	require.NotNil(t, frames[0].pps, "keyframe 0 should have PPS")
	require.NotNil(t, frames[2].sps, "keyframe 2 should have SPS")
	require.NotNil(t, frames[2].pps, "keyframe 2 should have PPS")

	// Frame 1 is not a keyframe — should not have SPS/PPS.
	assert.Nil(t, frames[1].sps, "non-keyframe should not have SPS")
	assert.Nil(t, frames[1].pps, "non-keyframe should not have PPS")

	// Critical check: mutating keyframe 0's SPS should NOT affect keyframe 2's SPS.
	// If they share the same underlying array, this mutation corrupts all keyframes.
	originalSPS2 := make([]byte, len(frames[2].sps))
	copy(originalSPS2, frames[2].sps)

	frames[0].sps[0] = 0xFF // mutate keyframe 0's SPS

	assert.Equal(t, originalSPS2, frames[2].sps,
		"keyframe 2's SPS should be unaffected by mutating keyframe 0's SPS (must be independent copies)")

	// Same check for PPS.
	originalPPS2 := make([]byte, len(frames[2].pps))
	copy(originalPPS2, frames[2].pps)

	frames[0].pps[0] = 0xFF // mutate keyframe 0's PPS

	assert.Equal(t, originalPPS2, frames[2].pps,
		"keyframe 2's PPS should be unaffected by mutating keyframe 0's PPS (must be independent copies)")
}

// mp4box builds a raw ISOBMFF box: [4-byte size][4-byte type][payload].
func mp4box(boxType string, payload []byte) []byte {
	out := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(out[0:4], uint32(8+len(payload)))
	copy(out[4:8], boxType)
	copy(out[8:], payload)
	return out
}

// TestParseMP4Tracks_MaliciousStszSampleCount verifies that a crafted stsz box
// with a uniform SampleSize and an enormous SampleCount is rejected instead of
// triggering a multi-gigabyte allocation (OOM). go-mp4 reads SampleCount
// verbatim when SampleSize != 0 and leaves EntrySize empty, so without a
// file-size bound the demuxer would do make([]uint32, 0xFFFFFFFF) (~16 GiB).
func TestParseMP4Tracks_MaliciousStszSampleCount(t *testing.T) {
	// Build a minimal box tree the parser descends into:
	// moov -> trak -> mdia -> minf -> stbl -> stsz.
	// The stsz FullBox payload is: version(1) + flags(3) + SampleSize(4) +
	// SampleCount(4). With SampleSize=1, go-mp4 returns SampleCount verbatim
	// and reads no EntrySize array.
	stszPayload := make([]byte, 12)
	// version=0, flags=0 (bytes 0..3 stay zero).
	binary.BigEndian.PutUint32(stszPayload[4:8], 1)           // SampleSize = 1
	binary.BigEndian.PutUint32(stszPayload[8:12], 0xFFFFFFFF) // SampleCount = ~4 billion

	stsz := mp4box("stsz", stszPayload)
	stbl := mp4box("stbl", stsz)
	minf := mp4box("minf", stbl)
	mdia := mp4box("mdia", minf)
	trak := mp4box("trak", mdia)
	moov := mp4box("moov", trak)

	r := bytes.NewReader(moov)

	// The crafted file is tiny; pass its real size as the bound. This must be
	// rejected with an error rather than attempting the huge allocation.
	_, err := parseMP4Tracks(r, int64(len(moov)))
	require.Error(t, err, "crafted stsz with huge SampleCount must be rejected")
	assert.Contains(t, err.Error(), "stsz",
		"error should identify the offending stsz box")
}

// TestParseMP4Tracks_UniformStszWithinBounds verifies the uniform-size stsz
// path still works for a legitimate box whose SampleCount*SampleSize fits the
// file size (the fix must not break the non-malicious path).
func TestParseMP4Tracks_UniformStszWithinBounds(t *testing.T) {
	// SampleSize=4, SampleCount=3 => 12 bytes of media, well within the file.
	stszPayload := make([]byte, 12)
	binary.BigEndian.PutUint32(stszPayload[4:8], 4)  // SampleSize = 4
	binary.BigEndian.PutUint32(stszPayload[8:12], 3) // SampleCount = 3

	stsz := mp4box("stsz", stszPayload)
	stbl := mp4box("stbl", stsz)
	minf := mp4box("minf", stbl)
	mdia := mp4box("mdia", minf)
	trak := mp4box("trak", mdia)
	moov := mp4box("moov", trak)

	r := bytes.NewReader(moov)

	tracks, err := parseMP4Tracks(r, 1<<20) // 1 MiB nominal file
	require.NoError(t, err)
	require.Len(t, tracks, 1)
	assert.Equal(t, []uint32{4, 4, 4}, tracks[0].stsz,
		"uniform stsz within bounds should expand to per-sample sizes")
}

// TestBuildSampleTable_SampleSizeExceedsFileSize verifies that a non-uniform
// stsz entry whose size extends past the end of the file is rejected before
// readVideoSamples can attempt a huge allocation. A single crafted EntrySize
// of 0xFFFFFFFF in a tiny file would otherwise force a ~4 GiB make([]byte).
func TestBuildSampleTable_SampleSizeExceedsFileSize(t *testing.T) {
	track := &mp4Track{
		stsz: []uint32{0xFFFFFFFF}, // attacker-controlled per-sample size
		stsc: []mp4.StscEntry{{FirstChunk: 1, SamplesPerChunk: 1}},
		stco: []uint64{0},
	}

	_, err := buildSampleTable(track, 1024) // tiny file
	require.Error(t, err, "sample extending past end of file must be rejected")
}

// TestBuildSampleTable_SampleOffsetPastEOF verifies that a chunk offset
// pointing past the end of the file is rejected.
func TestBuildSampleTable_SampleOffsetPastEOF(t *testing.T) {
	track := &mp4Track{
		stsz: []uint32{16},
		stsc: []mp4.StscEntry{{FirstChunk: 1, SamplesPerChunk: 1}},
		stco: []uint64{1 << 40}, // offset far beyond the file
	}

	_, err := buildSampleTable(track, 1024)
	require.Error(t, err, "sample offset past EOF must be rejected")
}

// TestBuildSampleTable_OffsetSizeOverflow verifies that offset+size wrapping
// around uint64 does not bypass the bounds check.
func TestBuildSampleTable_OffsetSizeOverflow(t *testing.T) {
	track := &mp4Track{
		stsz: []uint32{0xFFFFFFFF},
		stsc: []mp4.StscEntry{{FirstChunk: 1, SamplesPerChunk: 1}},
		stco: []uint64{^uint64(0) - 100}, // offset+size wraps past zero
	}

	_, err := buildSampleTable(track, 1024)
	require.Error(t, err, "uint64 overflow in offset+size must be rejected")
}

// TestBuildSampleTable_OverlappingChunksAmplification verifies the memory
// amplification defense: stco chunk offsets may overlap (every chunk pointing
// at offset 0 with size==fileSize), in which case every individual sample fits
// within the file but the cumulative retained bytes are numSamples*fileSize.
// Legitimate samples cannot overlap, so cumulative size > fileSize must fail.
func TestBuildSampleTable_OverlappingChunksAmplification(t *testing.T) {
	const fileSize = 1024
	const numSamples = 64

	stsz := make([]uint32, numSamples)
	stco := make([]uint64, numSamples)
	for i := range stsz {
		stsz[i] = fileSize // each sample spans the whole file
		stco[i] = 0        // every chunk overlaps at offset 0
	}
	track := &mp4Track{
		stsz: stsz,
		stsc: []mp4.StscEntry{{FirstChunk: 1, SamplesPerChunk: 1}},
		stco: stco,
	}

	_, err := buildSampleTable(track, fileSize)
	require.Error(t, err, "cumulative sample bytes exceeding file size must be rejected")
}

// TestBuildSampleTable_CumulativeAtFileSizeOK verifies that a legitimate
// track whose samples exactly fill the file is still accepted.
func TestBuildSampleTable_CumulativeAtFileSizeOK(t *testing.T) {
	track := &mp4Track{
		stsz: []uint32{512, 512},
		stsc: []mp4.StscEntry{{FirstChunk: 1, SamplesPerChunk: 2}},
		stco: []uint64{0},
	}

	entries, err := buildSampleTable(track, 1024)
	require.NoError(t, err)
	require.Len(t, entries, 2)
}

// TestReadVideoSamples_ZeroTimescale verifies that a track with a missing or
// zero mdhd timescale is rejected with an error instead of panicking with an
// integer divide-by-zero when computing PTS (finding: crafted MP4 without
// mdhd, or with Timescale=0, panics the demux on the first sample).
func TestReadVideoSamples_ZeroTimescale(t *testing.T) {
	track := &mp4Track{
		handlerType:    "vide",
		timescale:      0, // mdhd missing or Timescale=0
		naluLengthSize: 4,
		sps:            []byte{0x67, 0x42},
		pps:            []byte{0x68, 0xce},
		stts:           []mp4.SttsEntry{{SampleCount: 1, SampleDelta: 100}},
		stss:           []uint32{1},
		stsz:           []uint32{8},
		stsc:           []mp4.StscEntry{{FirstChunk: 1, SamplesPerChunk: 1}},
		stco:           []uint64{0},
	}

	sampleData := []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x00, 0x00, 0x00}
	tmpFile := writeTemp(t, sampleData, ".raw")
	f, err := os.Open(tmpFile)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	_, err = readVideoSamples(f, track, int64(len(sampleData)))
	require.Error(t, err, "zero timescale must be a demux error, not a panic")
	assert.Contains(t, err.Error(), "timescale")
}

// TestReadAudioSamples_ZeroTimescale is the audio-track analogue of
// TestReadVideoSamples_ZeroTimescale.
func TestReadAudioSamples_ZeroTimescale(t *testing.T) {
	track := &mp4Track{
		handlerType:     "soun",
		timescale:       0,
		audioSampleRate: 48000,
		audioChannels:   2,
		stts:            []mp4.SttsEntry{{SampleCount: 1, SampleDelta: 1024}},
		stsz:            []uint32{4},
		stsc:            []mp4.StscEntry{{FirstChunk: 1, SamplesPerChunk: 1}},
		stco:            []uint64{0},
	}

	sampleData := []byte{0x01, 0x02, 0x03, 0x04}
	tmpFile := writeTemp(t, sampleData, ".raw")
	f, err := os.Open(tmpFile)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	_, err = readAudioSamples(f, track, int64(len(sampleData)))
	require.Error(t, err, "zero timescale must be a demux error, not a panic")
	assert.Contains(t, err.Error(), "timescale")
}
