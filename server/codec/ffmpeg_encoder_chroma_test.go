//go:build cgo && !noffmpeg

package codec

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFFmpegEncoder_ChromaLocation_H264 verifies that the H.264 encoder signals
// chroma_location=left in the SPS VUI parameters. This is critical for correct
// chroma reconstruction on decoders — without it, decoders assume center-sited
// chroma which causes a half-pixel color shift.
func TestFFmpegEncoder_ChromaLocation_H264(t *testing.T) {
	// Try libx264 first, fall back to h264_videotoolbox on Mac.
	enc, err := NewFFmpegEncoder("libx264", 256, 256, 1_000_000, 30, 1, 2, nil)
	if err != nil {
		enc, err = NewFFmpegEncoder("h264_videotoolbox", 256, 256, 1_000_000, 30, 1, 2, nil)
		if err != nil {
			t.Skip("no H.264 encoder available")
		}
	}
	defer enc.Close()

	verifyChromaLocation(t, enc, 256, 256, false)
}

// TestFFmpegEncoder_ChromaLocation_HEVC verifies that the HEVC encoder signals
// chroma_location=left in the VPS/SPS VUI. Same rationale as the H.264 test.
func TestFFmpegEncoder_ChromaLocation_HEVC(t *testing.T) {
	// Try libx265 first, fall back to hevc_videotoolbox on Mac.
	enc, err := NewFFmpegEncoder("libx265", 256, 256, 1_000_000, 30, 1, 2, nil)
	if err != nil {
		enc, err = NewFFmpegEncoder("hevc_videotoolbox", 256, 256, 1_000_000, 30, 1, 2, nil)
		if err != nil {
			t.Skip("no HEVC encoder available")
		}
	}
	defer enc.Close()

	verifyChromaLocation(t, enc, 256, 256, false)
}

// TestFFmpegEncoder_ChromaLocation_Preview verifies that even the lightweight
// preview encoder (baseline profile) signals chroma_location=left.
func TestFFmpegEncoder_ChromaLocation_Preview(t *testing.T) {
	enc, err := NewFFmpegPreviewEncoder(256, 256, 500_000, 30, 1, 2, "ultrafast")
	if err != nil {
		t.Skipf("preview encoder not available: %v", err)
	}
	defer enc.Close()

	verifyChromaLocation(t, enc, 256, 256, false)
}

// verifyChromaLocation encodes frames, writes to a temp file, and uses ffprobe
// to verify chroma_location=left in the output stream.
func verifyChromaLocation(t *testing.T, enc *FFmpegEncoder, w, h int, is10bit bool) {
	t.Helper()

	// Generate YUV420 test data (mid-gray).
	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	totalSize := ySize + 2*uvSize
	yuv := make([]byte, totalSize)
	for i := 0; i < ySize; i++ {
		yuv[i] = 128 // Y = mid-gray
	}
	for i := ySize; i < totalSize; i++ {
		yuv[i] = 128 // U, V = neutral
	}

	// Write encoded data to a temp file.
	tmpFile, err := os.CreateTemp("", "chroma_test_*.h264")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	// Encode 60 frames to ensure we get past any warmup and have
	// multiple IDRs with SPS/PPS.
	framesWritten := 0
	for i := 0; i < 60; i++ {
		data, _, encErr := enc.Encode(yuv, int64(i*3000), i == 0)
		if encErr != nil {
			t.Fatalf("encode error on frame %d: %v", i, encErr)
		}
		if len(data) > 0 {
			_, writeErr := tmpFile.Write(data)
			require.NoError(t, writeErr)
			framesWritten++
		}
	}
	require.NoError(t, tmpFile.Close())
	require.Greater(t, framesWritten, 0, "encoder must produce at least one output frame")

	// Verify file has non-zero size.
	info, err := os.Stat(tmpFile.Name())
	require.NoError(t, err)
	require.Greater(t, info.Size(), int64(0), "encoded file must not be empty")

	// Use ffprobe to check chroma_location.
	out, err := exec.Command("ffprobe",
		"-v", "quiet",
		"-select_streams", "v:0",
		"-show_entries", "stream=chroma_location",
		"-of", "csv=p=0",
		tmpFile.Name(),
	).Output()
	if err != nil {
		t.Skipf("ffprobe not available or failed: %v", err)
	}

	result := strings.TrimSpace(string(out))
	if result == "" {
		t.Fatal("ffprobe returned empty chroma_location — VUI chroma_loc_info_present_flag is likely unset")
	}
	require.Equal(t, "left", result,
		"chroma_location must be 'left' (MPEG-2/4 convention for 4:2:0), got %q", result)
}
