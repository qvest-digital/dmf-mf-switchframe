package clip

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseSPSDimensions_422_CropUnits verifies that ParseSPSDimensions uses
// the correct crop unit for 4:2:2 content.
//
// H.264 spec (Table 6-1):
//   4:2:0 (chromaFormatIDC=1): SubWidthC=2, SubHeightC=2 → CropUnitY = 2
//   4:2:2 (chromaFormatIDC=2): SubWidthC=2, SubHeightC=1 → CropUnitY = 1
//
// Without this fix, 4:2:2 content with frame cropping gets wrong height because
// the crop offset is doubled (using 4:2:0 SubHeightC=2 instead of 4:2:2 SubHeightC=1).
func TestParseSPSDimensions_422_CropUnits(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg CLI not available")
	}

	// Generate a 1920x1080 H.264 High 4:2:2 clip. 1080 requires frame cropping
	// because the coded height is 1088 (68 macroblocks).
	dir := t.TempDir()
	outPath := filepath.Join(dir, "test_422_1080.h264")

	cmd := exec.Command("ffmpeg",
		"-f", "lavfi",
		"-i", "testsrc=size=1920x1080:rate=30",
		"-c:v", "libx264",
		"-pix_fmt", "yuv422p",
		"-profile:v", "high422",
		"-preset", "ultrafast",
		"-frames:v", "1",
		"-f", "h264",
		"-y", outPath,
	)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run())

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)

	// Extract SPS NALU from Annex B bitstream (NALU type 7).
	sps := extractSPSFromAnnexB(data)
	require.NotNil(t, sps, "should find SPS in 422 bitstream")

	w, h := ParseSPSDimensions(sps)
	assert.Equal(t, 1920, w, "width should be 1920")
	assert.Equal(t, 1080, h,
		"height should be 1080 (not 1072 — crop units must use SubHeightC=1 for 4:2:2)")
}

// TestParseSPSDimensions_422_NoCrop verifies that a resolution that doesn't
// need cropping (multiple of 16) parses correctly for 4:2:2.
func TestParseSPSDimensions_422_NoCrop(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg CLI not available")
	}

	dir := t.TempDir()
	outPath := filepath.Join(dir, "test_422_720.h264")

	// 1280x720 is exactly 80x45 macroblocks, no cropping needed.
	cmd := exec.Command("ffmpeg",
		"-f", "lavfi",
		"-i", "testsrc=size=1280x720:rate=30",
		"-c:v", "libx264",
		"-pix_fmt", "yuv422p",
		"-profile:v", "high422",
		"-preset", "ultrafast",
		"-frames:v", "1",
		"-f", "h264",
		"-y", outPath,
	)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run())

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)

	sps := extractSPSFromAnnexB(data)
	require.NotNil(t, sps)

	w, h := ParseSPSDimensions(sps)
	assert.Equal(t, 1280, w, "width should be 1280")
	assert.Equal(t, 720, h, "height should be 720")
}

// TestParseSPSDimensions_422_480p verifies 640x480 (4:2:2), which is
// exactly 40x30 macroblocks with no cropping.
func TestParseSPSDimensions_422_480p(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg CLI not available")
	}

	dir := t.TempDir()
	outPath := filepath.Join(dir, "test_422_480.h264")

	cmd := exec.Command("ffmpeg",
		"-f", "lavfi",
		"-i", "testsrc=size=640x480:rate=30",
		"-c:v", "libx264",
		"-pix_fmt", "yuv422p",
		"-profile:v", "high422",
		"-preset", "ultrafast",
		"-frames:v", "1",
		"-f", "h264",
		"-y", outPath,
	)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run())

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)

	sps := extractSPSFromAnnexB(data)
	require.NotNil(t, sps)

	w, h := ParseSPSDimensions(sps)
	assert.Equal(t, 640, w, "width should be 640")
	assert.Equal(t, 480, h, "height should be 480")
}

// TestValidate_422_TS validates that a 4:2:2 H.264 clip in MPEG-TS can be
// validated end-to-end (demux, SPS parse, test decode).
func TestValidate_422_TS(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg CLI not available")
	}

	dir := t.TempDir()
	outPath := filepath.Join(dir, "test_422.ts")

	cmd := exec.Command("ffmpeg",
		"-f", "lavfi",
		"-i", "testsrc=size=320x240:rate=30",
		"-f", "lavfi",
		"-i", "sine=frequency=1000:sample_rate=48000",
		"-c:v", "libx264",
		"-pix_fmt", "yuv422p",
		"-profile:v", "high422",
		"-preset", "ultrafast",
		"-c:a", "aac",
		"-shortest",
		"-frames:v", "30",
		"-f", "mpegts",
		"-y", outPath,
	)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run())

	result, err := Validate(outPath)
	require.NoError(t, err, "Validate should succeed for 422 TS clip")
	assert.Equal(t, "h264", result.Codec)
	assert.Equal(t, 320, result.Width)
	assert.Equal(t, 240, result.Height)
	assert.Greater(t, result.FrameCount, 0)
	assert.Greater(t, result.DurationMs, int64(0))
}

// TestValidate_422_1080p_TS validates that 1920x1080 4:2:2 content (which
// requires frame cropping) parses with correct dimensions.
func TestValidate_422_1080p_TS(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg CLI not available")
	}

	dir := t.TempDir()
	outPath := filepath.Join(dir, "test_422_1080.ts")

	cmd := exec.Command("ffmpeg",
		"-f", "lavfi",
		"-i", "testsrc=size=1920x1080:rate=30",
		"-c:v", "libx264",
		"-pix_fmt", "yuv422p",
		"-profile:v", "high422",
		"-preset", "ultrafast",
		"-frames:v", "10",
		"-f", "mpegts",
		"-y", outPath,
	)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run())

	result, err := Validate(outPath)
	require.NoError(t, err, "Validate should succeed for 1080p 422 TS clip")
	assert.Equal(t, "h264", result.Codec)
	assert.Equal(t, 1920, result.Width)
	assert.Equal(t, 1080, result.Height,
		"height should be 1080, not 1072 (crop units must be correct for 4:2:2)")
}

// extractSPSFromAnnexB finds the first SPS NALU (type 7) in an Annex B bitstream.
func extractSPSFromAnnexB(data []byte) []byte {
	for i := 0; i < len(data)-4; i++ {
		// Look for 4-byte start code
		if data[i] == 0 && data[i+1] == 0 && data[i+2] == 0 && data[i+3] == 1 {
			if i+4 < len(data) {
				naluType := data[i+4] & 0x1F
				if naluType == 7 { // SPS
					// Find end of this NALU
					end := len(data)
					for j := i + 4; j < len(data)-3; j++ {
						if data[j] == 0 && data[j+1] == 0 &&
							(data[j+2] == 1 || (data[j+2] == 0 && j+3 < len(data) && data[j+3] == 1)) {
							end = j
							break
						}
					}
					return data[i+4 : end]
				}
			}
		}
		// Also check 3-byte start code
		if data[i] == 0 && data[i+1] == 0 && data[i+2] == 1 {
			if i+3 < len(data) {
				naluType := data[i+3] & 0x1F
				if naluType == 7 { // SPS
					end := len(data)
					for j := i + 3; j < len(data)-3; j++ {
						if data[j] == 0 && data[j+1] == 0 &&
							(data[j+2] == 1 || (data[j+2] == 0 && j+3 < len(data) && data[j+3] == 1)) {
							end = j
							break
						}
					}
					return data[i+3 : end]
				}
			}
		}
	}
	return nil
}
