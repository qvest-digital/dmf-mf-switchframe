//go:build cgo && !noffmpeg

package codec

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generate422H264 uses ffmpeg CLI to generate an H.264 High 4:2:2 profile
// Annex B bitstream at the given resolution and frame count.
// Returns the path to the generated .h264 file.
func generate422H264(t *testing.T, w, h, numFrames int) string {
	t.Helper()

	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg CLI not available")
	}

	dir := t.TempDir()
	outPath := filepath.Join(dir, "test_422.h264")

	cmd := exec.Command("ffmpeg",
		"-f", "lavfi",
		"-i", "testsrc=size="+itoa(w)+"x"+itoa(h)+":rate=30",
		"-c:v", "libx264",
		"-pix_fmt", "yuv422p",
		"-profile:v", "high422",
		"-preset", "ultrafast",
		"-frames:v", itoa(numFrames),
		"-f", "h264",
		"-y", outPath,
	)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("ffmpeg 422 generation failed: %v", err)
	}

	info, err := os.Stat(outPath)
	require.NoError(t, err)
	require.Greater(t, info.Size(), int64(0), "generated file should not be empty")

	return outPath
}

// generate422H264TS generates an H.264 High 4:2:2 profile clip in MPEG-TS container.
func generate422H264TS(t *testing.T, w, h, numFrames int) string {
	t.Helper()

	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg CLI not available")
	}

	dir := t.TempDir()
	outPath := filepath.Join(dir, "test_422.ts")

	cmd := exec.Command("ffmpeg",
		"-f", "lavfi",
		"-i", "testsrc=size="+itoa(w)+"x"+itoa(h)+":rate=30",
		"-c:v", "libx264",
		"-pix_fmt", "yuv422p",
		"-profile:v", "high422",
		"-preset", "ultrafast",
		"-frames:v", itoa(numFrames),
		"-f", "mpegts",
		"-y", outPath,
	)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("ffmpeg 422 TS generation failed: %v", err)
	}

	info, err := os.Stat(outPath)
	require.NoError(t, err)
	require.Greater(t, info.Size(), int64(0))

	return outPath
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}

// TestFFmpegDecoder_422_Decode_ProducesYUV420 verifies that a 4:2:2 H.264
// bitstream is decoded and downsampled to YUV420P (w*h*3/2 bytes).
func TestFFmpegDecoder_422_Decode_ProducesYUV420(t *testing.T) {
	w, h := 320, 240
	path := generate422H264(t, w, h, 30)

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	dec, err := NewFFmpegDecoderWithThreads(nil, 1)
	require.NoError(t, err)
	defer dec.Close()

	// Feed the entire bitstream to the decoder and collect output frames.
	var decoded int
	yuv, dw, dh, decErr := dec.Decode(data)
	if decErr != nil {
		// May need to send more data. Try sending in chunks using
		// Annex B start code boundaries.
		frames := splitAnnexB(data)
		for _, frame := range frames {
			yuv, dw, dh, decErr = dec.Decode(frame)
			if decErr != nil {
				continue // buffering
			}
			decoded++
			break
		}
	} else {
		decoded++
	}

	require.Greater(t, decoded, 0, "decoder should produce at least one frame from 422 input")

	// Output should be YUV420P: w*h*3/2 bytes
	expected420 := yuvFrameSize(dw, dh)
	assert.Equal(t, expected420, len(yuv),
		"422 input should be downsampled to YUV420P (w*h*3/2)")
	assert.Equal(t, w, dw, "width should match input")
	assert.Equal(t, h, dh, "height should match input")
}

// TestFFmpegDecoder_422_Decode_CorrectDimensions verifies dimensions are
// preserved when decoding 4:2:2 content.
func TestFFmpegDecoder_422_Decode_CorrectDimensions(t *testing.T) {
	tests := []struct {
		w, h int
	}{
		{320, 240},
		{1920, 1080},
		{1280, 720},
	}

	for _, tc := range tests {
		t.Run(itoa(tc.w)+"x"+itoa(tc.h), func(t *testing.T) {
			path := generate422H264(t, tc.w, tc.h, 10)
			data, err := os.ReadFile(path)
			require.NoError(t, err)

			dec, err := NewFFmpegDecoderWithThreads(nil, 1)
			require.NoError(t, err)
			defer dec.Close()

			frames := splitAnnexB(data)
			for _, frame := range frames {
				yuv, dw, dh, decErr := dec.Decode(frame)
				if decErr != nil {
					continue
				}
				assert.Equal(t, tc.w, dw, "decoded width")
				assert.Equal(t, tc.h, dh, "decoded height")
				assert.Equal(t, yuvFrameSize(dw, dh), len(yuv),
					"output should be YUV420P size")
				return
			}
			t.Fatal("no frames decoded")
		})
	}
}

// TestFFmpegDecoder_422_ChromaDownsample verifies that 4:2:2 chroma planes
// are correctly downsampled to 4:2:0 (half-height chroma).
func TestFFmpegDecoder_422_ChromaDownsample(t *testing.T) {
	w, h := 320, 240
	path := generate422H264(t, w, h, 10)
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	dec, err := NewFFmpegDecoderWithThreads(nil, 1)
	require.NoError(t, err)
	defer dec.Close()

	frames := splitAnnexB(data)
	for _, frame := range frames {
		yuv, dw, dh, decErr := dec.Decode(frame)
		if decErr != nil {
			continue
		}

		// Verify YUV420P plane layout
		ySize := dw * dh
		uvSize := (dw / 2) * (dh / 2)
		assert.Equal(t, ySize+2*uvSize, len(yuv), "YUV420P total size")

		// Y plane should have non-trivial data (not all zeros)
		yPlane := yuv[:ySize]
		nonZero := 0
		for _, b := range yPlane {
			if b != 0 {
				nonZero++
			}
		}
		assert.Greater(t, nonZero, ySize/10,
			"Y plane should contain real image data, not zeros")

		// U and V planes should have valid chroma data
		uPlane := yuv[ySize : ySize+uvSize]
		vPlane := yuv[ySize+uvSize:]
		uNonCenter := 0
		vNonCenter := 0
		for _, b := range uPlane {
			if b < 120 || b > 136 { // not just centered at 128
				uNonCenter++
			}
		}
		for _, b := range vPlane {
			if b < 120 || b > 136 {
				vNonCenter++
			}
		}
		// testsrc generates colored content, so chroma should have variety
		assert.Greater(t, uNonCenter+vNonCenter, 0,
			"chroma planes should contain real data from 422 downsample")
		return
	}
	t.Fatal("no frames decoded")
}

// TestFFmpegDecoder_422_Native10bit_Promotion verifies that when the native
// 10-bit decoder flag is set, 8-bit 422 input is promoted to YUV422P10LE
// (w*h*4 bytes) output.
func TestFFmpegDecoder_422_Native10bit_Promotion(t *testing.T) {
	w, h := 320, 240
	path := generate422H264(t, w, h, 10)
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	// Create decoder with 10-bit 422 output mode.
	// We need an H.264 decoder (not HEVC) with the 10-bit flag set.
	dec, err := NewFFmpegDecoderNative10bitWithThreads(nil, 1)
	require.NoError(t, err)
	defer dec.Close()

	frames := splitAnnexB(data)
	for _, frame := range frames {
		yuv, dw, dh, decErr := dec.Decode(frame)
		if decErr != nil {
			continue
		}

		// With native 10-bit flag and 422 input, output should be YUV422P10LE
		expected10bit := yuv422p10leFrameSize(dw, dh) // w*h*4
		assert.Equal(t, expected10bit, len(yuv),
			"422 input with native 10-bit flag should produce YUV422P10LE output (w*h*4)")
		assert.Equal(t, w, dw)
		assert.Equal(t, h, dh)
		return
	}
	t.Fatal("no frames decoded")
}

// TestFFmpegDecoder_422_EncodeDecodeRoundTrip verifies the full encode→decode
// pipeline with 422 content: encode YUV420 with libx264 High 4:2:2 profile via
// ffmpeg CLI, then decode back and verify we get valid YUV420P output.
func TestFFmpegDecoder_422_EncodeDecodeRoundTrip(t *testing.T) {
	w, h := 256, 256
	path := generate422H264(t, w, h, 30)
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	dec, err := NewFFmpegDecoderWithThreads(nil, 1)
	require.NoError(t, err)
	defer dec.Close()

	frames := splitAnnexB(data)
	decodedCount := 0
	for _, frame := range frames {
		_, dw, dh, decErr := dec.Decode(frame)
		if decErr != nil {
			continue
		}
		decodedCount++
		assert.Equal(t, w, dw)
		assert.Equal(t, h, dh)
	}

	// Drain buffered frames
	if err := dec.SendEOS(); err == nil {
		for {
			_, _, _, recvErr := dec.ReceiveFrame()
			if recvErr != nil {
				break
			}
			decodedCount++
		}
	}

	assert.Greater(t, decodedCount, 10,
		"should decode most frames from 30-frame 422 clip")
}

// TestFFmpegDecoder_422_FlushAndReuse verifies that a decoder can be flushed
// and reused after decoding 422 content.
func TestFFmpegDecoder_422_FlushAndReuse(t *testing.T) {
	w, h := 320, 240
	path := generate422H264(t, w, h, 5)
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	dec, err := NewFFmpegDecoderWithThreads(nil, 1)
	require.NoError(t, err)
	defer dec.Close()

	frames := splitAnnexB(data)

	// First decode pass
	for _, frame := range frames {
		dec.Decode(frame)
	}

	// Flush
	dec.Flush()

	// Second decode pass should work
	decoded := false
	for _, frame := range frames {
		_, dw, dh, decErr := dec.Decode(frame)
		if decErr != nil {
			continue
		}
		assert.Equal(t, w, dw)
		assert.Equal(t, h, dh)
		decoded = true
		break
	}
	assert.True(t, decoded, "decoder should work after flush with 422 content")
}

// splitAnnexB splits an Annex B bitstream at start code boundaries.
func splitAnnexB(data []byte) [][]byte {
	var frames [][]byte
	var start int

	for i := 0; i < len(data)-3; i++ {
		isStartCode3 := data[i] == 0 && data[i+1] == 0 && data[i+2] == 1
		isStartCode4 := i+3 < len(data) && data[i] == 0 && data[i+1] == 0 && data[i+2] == 0 && data[i+3] == 1

		if (isStartCode3 || isStartCode4) && i > start {
			frames = append(frames, data[start:i])
			start = i
		}
	}
	if start < len(data) {
		frames = append(frames, data[start:])
	}

	// Group NALUs into access units (by finding SPS/IDR boundaries).
	// For simplicity, return the whole bitstream as a single chunk if
	// frame splitting is tricky.
	if len(frames) == 0 {
		return [][]byte{data}
	}

	// Actually, for the decoder, it's better to send larger chunks.
	// Group consecutive NALUs between IDR/non-IDR slice boundaries.
	var accessUnits [][]byte
	var current []byte
	for _, nalu := range frames {
		// Find the NALU type
		offset := 0
		if len(nalu) > 3 && nalu[0] == 0 && nalu[1] == 0 && nalu[2] == 0 && nalu[3] == 1 {
			offset = 4
		} else if len(nalu) > 2 && nalu[0] == 0 && nalu[1] == 0 && nalu[2] == 1 {
			offset = 3
		}
		if offset < len(nalu) {
			naluType := nalu[offset] & 0x1F
			// Slice types (1=non-IDR, 5=IDR) start new access units
			if (naluType == 1 || naluType == 5) && len(current) > 0 {
				accessUnits = append(accessUnits, current)
				current = nil
			}
		}
		current = append(current, nalu...)
	}
	if len(current) > 0 {
		accessUnits = append(accessUnits, current)
	}

	if len(accessUnits) == 0 {
		return [][]byte{data}
	}
	return accessUnits
}
