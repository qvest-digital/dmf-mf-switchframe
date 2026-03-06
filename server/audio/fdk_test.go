//go:build cgo

package audio

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFDKDecoderCreation(t *testing.T) {
	dec, err := NewFDKDecoder(48000, 2)
	require.NoError(t, err)
	require.NotNil(t, dec)
	defer func() { _ = dec.Close() }()

	// Verify it implements AudioDecoder.
	var _ AudioDecoder = dec
}

func TestFDKDecoderCreationMono(t *testing.T) {
	dec, err := NewFDKDecoder(48000, 1)
	require.NoError(t, err)
	require.NotNil(t, dec)
	defer func() { _ = dec.Close() }()
}

func TestFDKDecoderInvalidParams(t *testing.T) {
	_, err := NewFDKDecoder(0, 2)
	require.Error(t, err)

	_, err = NewFDKDecoder(48000, 0)
	require.Error(t, err)

	_, err = NewFDKDecoder(48000, 9)
	require.Error(t, err)
}

func TestFDKEncoderCreation(t *testing.T) {
	enc, err := NewFDKEncoder(48000, 2)
	require.NoError(t, err)
	require.NotNil(t, enc)
	defer func() { _ = enc.Close() }()

	// Verify it implements AudioEncoder.
	var _ AudioEncoder = enc
}

func TestFDKEncoderCreationMono(t *testing.T) {
	enc, err := NewFDKEncoder(48000, 1)
	require.NoError(t, err)
	require.NotNil(t, enc)
	defer func() { _ = enc.Close() }()
}

func TestFDKEncoderInvalidParams(t *testing.T) {
	_, err := NewFDKEncoder(0, 2)
	require.Error(t, err)

	_, err = NewFDKEncoder(48000, 0)
	require.Error(t, err)

	_, err = NewFDKEncoder(48000, 9)
	require.Error(t, err)
}

func TestFDKRoundTrip(t *testing.T) {
	enc, err := NewFDKEncoder(48000, 2)
	require.NoError(t, err)
	defer func() { _ = enc.Close() }()

	dec, err := NewFDKDecoder(48000, 2)
	require.NoError(t, err)
	defer func() { _ = dec.Close() }()

	// Generate a frame of silence (1024 stereo samples).
	silence := make([]float32, 1024*2)
	aac, err := enc.Encode(silence)
	require.NoError(t, err)
	require.NotEmpty(t, aac)

	pcm, err := dec.Decode(aac)
	require.NoError(t, err)
	require.Equal(t, 1024*2, len(pcm), "decoded PCM should have 1024 stereo samples")

	// Silence should decode back to near-silence.
	for i, s := range pcm {
		require.LessOrEqual(t, float64(math.Abs(float64(s))), 0.01,
			"sample %d should be near zero, got %f", i, s)
	}
}

func TestFDKRoundTripMono(t *testing.T) {
	enc, err := NewFDKEncoder(48000, 1)
	require.NoError(t, err)
	defer func() { _ = enc.Close() }()

	dec, err := NewFDKDecoder(48000, 1)
	require.NoError(t, err)
	defer func() { _ = dec.Close() }()

	silence := make([]float32, 1024)
	aac, err := enc.Encode(silence)
	require.NoError(t, err)
	require.NotEmpty(t, aac)

	pcm, err := dec.Decode(aac)
	require.NoError(t, err)
	require.Equal(t, 1024, len(pcm), "decoded PCM should have 1024 mono samples")
}

func TestFDKRoundTripWithSignal(t *testing.T) {
	enc, err := NewFDKEncoder(48000, 2)
	require.NoError(t, err)
	defer func() { _ = enc.Close() }()

	dec, err := NewFDKDecoder(48000, 2)
	require.NoError(t, err)
	defer func() { _ = dec.Close() }()

	// Generate a 1kHz sine wave (1024 stereo samples).
	input := make([]float32, 1024*2)
	for i := 0; i < 1024; i++ {
		val := float32(0.5 * math.Sin(2*math.Pi*1000*float64(i)/48000))
		input[i*2] = val   // L
		input[i*2+1] = val // R
	}

	// Encode and decode multiple frames. The codec has encoder delay, so
	// we need several frames before the decoded output contains the signal.
	var lastPCM []float32
	for i := 0; i < 10; i++ {
		aac, encErr := enc.Encode(input)
		require.NoError(t, encErr)
		require.NotEmpty(t, aac)

		pcm, decErr := dec.Decode(aac)
		require.NoError(t, decErr)
		require.Equal(t, 1024*2, len(pcm))
		lastPCM = pcm
	}

	// After several frames, the decoded output should contain the signal.
	var maxAbs float32
	for _, s := range lastPCM {
		if abs := float32(math.Abs(float64(s))); abs > maxAbs {
			maxAbs = abs
		}
	}
	require.Greater(t, maxAbs, float32(0.01), "decoded signal should not be silent")
}

func TestFDKEncoderWrongFrameSize(t *testing.T) {
	enc, err := NewFDKEncoder(48000, 2)
	require.NoError(t, err)
	defer func() { _ = enc.Close() }()

	// Wrong number of samples: should be 1024*2=2048 for stereo.
	_, err = enc.Encode(make([]float32, 100))
	require.Error(t, err)
}

func TestFDKDecoderInvalidData(t *testing.T) {
	dec, err := NewFDKDecoder(48000, 2)
	require.NoError(t, err)
	defer func() { _ = dec.Close() }()

	// Garbage data should fail.
	_, err = dec.Decode([]byte{0xFF, 0xFF, 0x00, 0x00, 0x01, 0x02, 0x03, 0x04})
	require.Error(t, err)
}

func TestFDKDoubleClose(t *testing.T) {
	enc, err := NewFDKEncoder(48000, 2)
	require.NoError(t, err)
	require.NoError(t, enc.Close())
	require.NoError(t, enc.Close()) // should not panic

	dec, err := NewFDKDecoder(48000, 2)
	require.NoError(t, err)
	require.NoError(t, dec.Close())
	require.NoError(t, dec.Close()) // should not panic
}
