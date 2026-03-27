package testutil

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// GenerateSolidYUV420 creates a solid-color YUV420p frame.
// Layout: Y plane (w*h), then Cb plane (w/2 * h/2), then Cr plane (w/2 * h/2).
func GenerateSolidYUV420(w, h int, y, cb, cr byte) []byte {
	chromaW := w / 2
	chromaH := h / 2
	frame := make([]byte, w*h+chromaW*chromaH*2)

	// Y plane
	for i := 0; i < w*h; i++ {
		frame[i] = y
	}

	// Cb plane
	cbStart := w * h
	for i := 0; i < chromaW*chromaH; i++ {
		frame[cbStart+i] = cb
	}

	// Cr plane
	crStart := cbStart + chromaW*chromaH
	for i := 0; i < chromaW*chromaH; i++ {
		frame[crStart+i] = cr
	}

	return frame
}

// GenerateGradientYUV420 creates a horizontal Y gradient from 16 to 235
// with neutral chroma (Cb=128, Cr=128).
func GenerateGradientYUV420(w, h int) []byte {
	chromaW := w / 2
	chromaH := h / 2
	frame := make([]byte, w*h+chromaW*chromaH*2)

	// Y plane: horizontal gradient
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			// Map column 0..w-1 to Y 16..235
			yVal := 16 + byte(float64(col)/float64(w-1)*219.0+0.5)
			frame[row*w+col] = yVal
		}
	}

	// Cb plane: neutral 128
	cbStart := w * h
	for i := 0; i < chromaW*chromaH; i++ {
		frame[cbStart+i] = 128
	}

	// Cr plane: neutral 128
	crStart := cbStart + chromaW*chromaH
	for i := 0; i < chromaW*chromaH; i++ {
		frame[crStart+i] = 128
	}

	return frame
}

// GenerateCheckerYUV420 creates a checkerboard pattern with alternating
// Y=16 and Y=235 blocks, with neutral chroma (Cb=128, Cr=128).
func GenerateCheckerYUV420(w, h, blockSize int) []byte {
	chromaW := w / 2
	chromaH := h / 2
	frame := make([]byte, w*h+chromaW*chromaH*2)

	// Y plane: checkerboard
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			blockX := col / blockSize
			blockY := row / blockSize
			if (blockX+blockY)%2 == 0 {
				frame[row*w+col] = 16
			} else {
				frame[row*w+col] = 235
			}
		}
	}

	// Cb plane: neutral 128
	cbStart := w * h
	for i := 0; i < chromaW*chromaH; i++ {
		frame[cbStart+i] = 128
	}

	// Cr plane: neutral 128
	crStart := cbStart + chromaW*chromaH
	for i := 0; i < chromaW*chromaH; i++ {
		frame[crStart+i] = 128
	}

	return frame
}

// GenerateGreenScreenYUV420 creates a BT.709 pure green frame for keying tests.
// BT.709 green (R=0, G=255, B=0): Y~145, Cb~54, Cr~34
func GenerateGreenScreenYUV420(w, h int) []byte {
	// BT.709 conversion from RGB to YCbCr:
	// Y  =  0.2126*R + 0.7152*G + 0.0722*B
	// Cb = -0.1146*R - 0.3854*G + 0.5000*B + 128
	// Cr =  0.5000*R - 0.4542*G - 0.0458*B + 128
	//
	// For pure green (R=0, G=255, B=0):
	// Y  = 0.7152 * 255 = 182.4 → but in limited range: 16 + 182.4 * 219/255 ≈ 173
	// Actually, for video range (16-235):
	// Y  = 16 + 219 * (0.7152) ≈ 173
	// Cb = 128 + 224 * (-0.3854) / 2 ≈ 128 - 43.2 ≈ 85... hmm
	//
	// Let's use the standard BT.709 limited range formula:
	// Y  = 16  + 65.481*R/255 + 128.553*G/255 + 24.966*B/255
	// Cb = 128 - 37.797*R/255 -  74.203*G/255 + 112.0*B/255
	// Cr = 128 + 112.0*R/255  -  101.78*G/255 - 10.22*B/255
	//
	// Pure green (R=0, G=255, B=0):
	// Y  = 16 + 128.553 ≈ 145
	// Cb = 128 - 74.203 ≈ 54
	// Cr = 128 - 101.78 ≈ 26
	y := byte(145)
	cb := byte(54)
	cr := byte(26)
	return GenerateSolidYUV420(w, h, y, cb, cr)
}

// GenerateSineWavePCM generates a mono sine wave at the specified frequency
// and level (in dBFS). Returns float32 PCM samples.
func GenerateSineWavePCM(freqHz, levelDBFS float64, sampleRate int, durationSec float64) []float32 {
	numSamples := int(float64(sampleRate) * durationSec)
	pcm := make([]float32, numSamples)

	amplitude := math.Pow(10.0, levelDBFS/20.0)
	angularFreq := 2.0 * math.Pi * freqHz / float64(sampleRate)

	for i := 0; i < numSamples; i++ {
		pcm[i] = float32(amplitude * math.Sin(angularFreq*float64(i)))
	}

	return pcm
}

// GenerateSilencePCM generates a mono silence buffer of the specified duration.
func GenerateSilencePCM(sampleRate int, durationSec float64) []float32 {
	numSamples := int(float64(sampleRate) * durationSec)
	return make([]float32, numSamples)
}

// GenerateKnownPTSSequence generates an arithmetic PTS sequence.
func GenerateKnownPTSSequence(startPTS int64, count int, delta int64) []int64 {
	pts := make([]int64, count)
	for i := 0; i < count; i++ {
		pts[i] = startPTS + int64(i)*delta
	}
	return pts
}

// WriteTempFile writes data to a temporary file with the given extension
// and returns the path. The file is automatically cleaned up when the test finishes.
func WriteTempFile(t *testing.T, data []byte, ext string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "testfile"+ext)
	require.NoError(t, os.WriteFile(path, data, 0o644))

	return path
}
