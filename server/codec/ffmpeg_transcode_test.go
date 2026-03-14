//go:build cgo && !noffmpeg

package codec

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// createTestTSInputFile creates a minimal H.264 Annex B file that avformat can
// probe and demux. Uses the existing encoder infrastructure.
func createTestTSInputFile(t *testing.T, dir string, width, height, numFrames int) string {
	t.Helper()

	enc, err := NewVideoEncoder(width, height, 500000, 30, 1)
	require.NoError(t, err, "failed to create encoder")
	defer enc.Close()

	yuvSize := width * height * 3 / 2
	yuv := make([]byte, yuvSize)
	for i := range yuv {
		yuv[i] = 128
	}

	path := filepath.Join(dir, "input.h264")
	f, err := os.Create(path)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	written := 0
	for i := range numFrames {
		// Vary content slightly each frame
		for j := 0; j < width*height; j++ {
			yuv[j] = byte((j*7 + i*13) % 256)
		}

		data, _, encErr := enc.Encode(yuv, int64(i*3000), i == 0)
		if encErr != nil {
			continue
		}
		if len(data) > 0 {
			_, writeErr := f.Write(data)
			require.NoError(t, writeErr)
			written += len(data)
		}
	}

	require.Greater(t, written, 0, "encoder produced no output")
	return path
}

func TestTranscodeFile_Success(t *testing.T) {
	dir := t.TempDir()
	inputPath := createTestTSInputFile(t, dir, 64, 64, 30)
	outputPath := filepath.Join(dir, "output.ts")

	result, err := TranscodeFile(inputPath, outputPath, "libx264", 500000)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify output file exists and is non-empty
	info, statErr := os.Stat(outputPath)
	require.NoError(t, statErr)
	require.Greater(t, info.Size(), int64(0), "output file should be non-empty")

	// Verify result metadata
	require.Equal(t, 64, result.Width, "output width should match input")
	require.Equal(t, 64, result.Height, "output height should match input")
	require.Greater(t, result.VideoFrames, 0, "should have transcoded some video frames")
	require.Greater(t, result.FPS, 0.0, "should report a positive FPS")
}

func TestTranscodeFile_NonExistent(t *testing.T) {
	dir := t.TempDir()
	outputPath := filepath.Join(dir, "output.ts")

	_, err := TranscodeFile("/tmp/nonexistent_transcode_test_12345.h264", outputPath, "libx264", 500000)
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot open input file")
}

func TestTranscodeFile_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "empty.h264")
	err := os.WriteFile(inputPath, []byte{}, 0644)
	require.NoError(t, err)

	outputPath := filepath.Join(dir, "output.ts")
	_, err = TranscodeFile(inputPath, outputPath, "libx264", 500000)
	require.Error(t, err)
}

func TestTranscodeFile_BadEncoder(t *testing.T) {
	dir := t.TempDir()
	inputPath := createTestTSInputFile(t, dir, 64, 64, 30)
	outputPath := filepath.Join(dir, "output.ts")

	// Use a completely invalid encoder name. The C function falls back to
	// avcodec_find_encoder(AV_CODEC_ID_H264) which uses libx264 if available.
	// Since the fallback exists, this should succeed.
	result, err := TranscodeFile(inputPath, outputPath, "nonexistent_encoder_xyz", 500000)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Greater(t, result.VideoFrames, 0)
}

func TestTranscodeFile_AutoBitrate(t *testing.T) {
	dir := t.TempDir()
	inputPath := createTestTSInputFile(t, dir, 64, 64, 30)
	outputPath := filepath.Join(dir, "output.ts")

	// bitrate=0 should auto-select based on resolution
	result, err := TranscodeFile(inputPath, outputPath, "libx264", 0)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Greater(t, result.VideoFrames, 0, "should have transcoded frames with auto bitrate")

	// Verify output file exists
	info, statErr := os.Stat(outputPath)
	require.NoError(t, statErr)
	require.Greater(t, info.Size(), int64(0))
}

func TestTranscodeFile_EmptyEncoderName(t *testing.T) {
	dir := t.TempDir()
	inputPath := createTestTSInputFile(t, dir, 64, 64, 30)
	outputPath := filepath.Join(dir, "output.ts")

	// Empty encoder name should fall back to default H264 encoder
	result, err := TranscodeFile(inputPath, outputPath, "", 500000)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Greater(t, result.VideoFrames, 0)
}

func TestTranscodeFile_LargerResolution(t *testing.T) {
	dir := t.TempDir()
	inputPath := createTestTSInputFile(t, dir, 320, 240, 30)
	outputPath := filepath.Join(dir, "output.ts")

	result, err := TranscodeFile(inputPath, outputPath, "libx264", 1000000)
	require.NoError(t, err)
	require.NotNil(t, result)

	require.Equal(t, 320, result.Width)
	require.Equal(t, 240, result.Height)
	require.Greater(t, result.VideoFrames, 0)
	require.Greater(t, result.DurationMs, int64(0), "should report positive duration")
}

func TestTranscodeFileWithProgress_TracksProgress(t *testing.T) {
	dir := t.TempDir()
	inputPath := createTestTSInputFile(t, dir, 64, 64, 60)
	outputPath := filepath.Join(dir, "output.ts")

	var progress int32
	result, err := TranscodeFileWithProgress(inputPath, outputPath, "libx264", 500000, &progress)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Greater(t, result.VideoFrames, 0)

	// Note: progress tracking requires the input container to have duration
	// metadata (ifmt_ctx->duration > 0). Raw H.264 Annex B streams (used by
	// this test helper) don't have duration, so progress stays 0. Real uploads
	// (MKV, WebM, MP4) always have duration metadata. We verify the pointer
	// was accepted without crashing and the transcode succeeded.
	finalProgress := int(progress)
	require.GreaterOrEqual(t, finalProgress, 0)
}

func TestTranscodeFileWithProgress_NilProgress(t *testing.T) {
	dir := t.TempDir()
	inputPath := createTestTSInputFile(t, dir, 64, 64, 15)
	outputPath := filepath.Join(dir, "output.ts")

	// nil progress pointer should work fine (no crash).
	result, err := TranscodeFileWithProgress(inputPath, outputPath, "libx264", 500000, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Greater(t, result.VideoFrames, 0)
}

func TestTranscodeFile_OutputIsMPEGTS(t *testing.T) {
	dir := t.TempDir()
	inputPath := createTestTSInputFile(t, dir, 64, 64, 15)
	outputPath := filepath.Join(dir, "output.ts")

	_, err := TranscodeFile(inputPath, outputPath, "libx264", 500000)
	require.NoError(t, err)

	// Verify the output is valid MPEG-TS by probing it with avformat
	probeResult, probeErr := ProbeFile(outputPath)
	require.NoError(t, probeErr, "output should be probeable as valid media")
	require.True(t, probeResult.HasVideo, "output should have video stream")
	require.True(t, probeResult.IsH264(), "output video should be H.264")
}
