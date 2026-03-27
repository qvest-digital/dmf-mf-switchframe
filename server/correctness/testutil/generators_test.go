package testutil

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerateSolidYUV420(t *testing.T) {
	w, h := 64, 48
	y, cb, cr := byte(128), byte(64), byte(200)
	frame := GenerateSolidYUV420(w, h, y, cb, cr)

	// Check total size: Y + Cb + Cr = w*h + (w/2)*(h/2) + (w/2)*(h/2) = w*h*3/2
	expectedSize := w * h * 3 / 2
	require.Len(t, frame, expectedSize)

	// Check Y plane: all bytes should be y
	yPlane := frame[:w*h]
	for i, val := range yPlane {
		require.Equal(t, y, val, "Y plane mismatch at index %d", i)
	}

	// Check Cb plane
	chromaW := w / 2
	chromaH := h / 2
	cbPlane := frame[w*h : w*h+chromaW*chromaH]
	for i, val := range cbPlane {
		require.Equal(t, cb, val, "Cb plane mismatch at index %d", i)
	}

	// Check Cr plane
	crPlane := frame[w*h+chromaW*chromaH:]
	for i, val := range crPlane {
		require.Equal(t, cr, val, "Cr plane mismatch at index %d", i)
	}
}

func TestGenerateGradientYUV420(t *testing.T) {
	w, h := 64, 48
	frame := GenerateGradientYUV420(w, h)

	expectedSize := w * h * 3 / 2
	require.Len(t, frame, expectedSize)

	// Check Y plane: horizontal gradient from 16 to 235
	// First column should be 16, last column should be 235
	require.Equal(t, byte(16), frame[0], "first pixel Y should be 16")
	require.Equal(t, byte(235), frame[w-1], "last pixel in row Y should be 235")

	// All rows should have the same gradient
	for row := 1; row < h; row++ {
		require.Equal(t, frame[0:w], frame[row*w:(row+1)*w], "row %d should match row 0", row)
	}

	// Check chroma planes are neutral (128)
	chromaW := w / 2
	chromaH := h / 2
	chromaStart := w * h
	cbPlane := frame[chromaStart : chromaStart+chromaW*chromaH]
	for i, val := range cbPlane {
		require.Equal(t, byte(128), val, "Cb should be 128 at index %d", i)
	}
	crPlane := frame[chromaStart+chromaW*chromaH:]
	for i, val := range crPlane {
		require.Equal(t, byte(128), val, "Cr should be 128 at index %d", i)
	}
}

func TestGenerateCheckerYUV420(t *testing.T) {
	w, h := 64, 48
	blockSize := 8
	frame := GenerateCheckerYUV420(w, h, blockSize)

	expectedSize := w * h * 3 / 2
	require.Len(t, frame, expectedSize)

	// Check pattern: alternating blocks of Y=16 and Y=235
	// Top-left block should be Y=16
	require.Equal(t, byte(16), frame[0], "top-left pixel should be Y=16")

	// Next block right should be Y=235
	require.Equal(t, byte(235), frame[blockSize], "second block should be Y=235")

	// Verify chroma is neutral
	chromaW := w / 2
	chromaH := h / 2
	cbStart := w * h
	for i := 0; i < chromaW*chromaH; i++ {
		require.Equal(t, byte(128), frame[cbStart+i], "Cb should be 128 at index %d", i)
	}
}

func TestGenerateGreenScreenYUV420(t *testing.T) {
	w, h := 64, 48
	frame := GenerateGreenScreenYUV420(w, h)

	expectedSize := w * h * 3 / 2
	require.Len(t, frame, expectedSize)

	// BT.709 pure green (R=0, G=255, B=0) in limited range:
	// Y≈145, Cb≈54, Cr≈26
	yVal, cb, cr := YUVPixelAt(frame, w, h, 0, 0)
	// Allow some tolerance for BT.709 green conversion
	require.InDelta(t, 145, int(yVal), 5, "Y for green")
	require.InDelta(t, 54, int(cb), 5, "Cb for green")
	require.InDelta(t, 26, int(cr), 5, "Cr for green")
}

func TestGenerateSineWavePCM(t *testing.T) {
	sampleRate := 48000
	duration := 0.1 // 100ms
	freq := 1000.0
	level := -6.0

	pcm := GenerateSineWavePCM(freq, level, sampleRate, duration)

	expectedSamples := int(float64(sampleRate) * duration)
	require.Len(t, pcm, expectedSamples)

	// Check peak level is approximately -6 dBFS
	peak := PeakDBFS(pcm)
	require.InDelta(t, level, peak, 0.5, "peak level should be approximately %f dBFS", level)

	// Check it's not silence
	hasNonZero := false
	for _, s := range pcm {
		if s != 0 {
			hasNonZero = true
			break
		}
	}
	require.True(t, hasNonZero, "sine wave should not be silence")
}

func TestGenerateSineWavePCM_FullScale(t *testing.T) {
	pcm := GenerateSineWavePCM(1000.0, 0.0, 48000, 0.1)
	peak := PeakDBFS(pcm)
	require.InDelta(t, 0.0, peak, 0.5, "0 dBFS sine should peak near 0 dBFS")
}

func TestGenerateSilencePCM(t *testing.T) {
	sampleRate := 48000
	duration := 0.05

	pcm := GenerateSilencePCM(sampleRate, duration)

	expectedSamples := int(float64(sampleRate) * duration)
	require.Len(t, pcm, expectedSamples)

	for i, s := range pcm {
		require.Equal(t, float32(0), s, "sample %d should be zero", i)
	}
}

func TestGenerateKnownPTSSequence(t *testing.T) {
	startPTS := int64(90000)
	count := 10
	delta := int64(3000) // 30fps at 90kHz

	pts := GenerateKnownPTSSequence(startPTS, count, delta)

	require.Len(t, pts, count)
	require.Equal(t, startPTS, pts[0])
	require.Equal(t, startPTS+int64(count-1)*delta, pts[count-1])

	// Check monotonic and correct delta
	for i := 1; i < len(pts); i++ {
		require.Equal(t, delta, pts[i]-pts[i-1], "PTS delta at index %d", i)
	}
}

func TestWriteTempFile(t *testing.T) {
	data := []byte("test data content")
	path := WriteTempFile(t, data, ".ts")

	require.FileExists(t, path)
	require.Contains(t, path, ".ts")
}

func TestLinearToDBFS(t *testing.T) {
	// Full scale
	require.InDelta(t, 0.0, LinearToDBFS(1.0), 0.01)

	// Half amplitude = -6.02 dBFS
	require.InDelta(t, -6.02, LinearToDBFS(0.5), 0.1)

	// Zero should return -inf
	result := LinearToDBFS(0.0)
	require.True(t, math.IsInf(result, -1), "LinearToDBFS(0) should be -inf")

	// Negative values should use absolute value
	require.InDelta(t, 0.0, LinearToDBFS(-1.0), 0.01)
}
