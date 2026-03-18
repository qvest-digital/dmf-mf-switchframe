package comms

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpusAvailable(t *testing.T) {
	if !opusAvailable {
		t.Skip("opus not available (built without cgo or with noopus tag)")
	}
	assert.True(t, opusAvailable)
}

func TestOpusRoundTrip(t *testing.T) {
	if !opusAvailable {
		t.Skip("opus not available (built without cgo or with noopus tag)")
	}

	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960 // 20ms at 48kHz
	)

	// Create encoder and decoder.
	enc, err := newOpusEncoder(sampleRate, channels)
	require.NoError(t, err)

	dec, err := newOpusDecoder(sampleRate, channels)
	require.NoError(t, err)

	// Encode silence (960 samples = 20ms at 48kHz).
	pcm := make([]int16, frameSize*channels)
	buf := make([]byte, 4096)

	n, err := enc.Encode(pcm, frameSize, buf)
	require.NoError(t, err)
	assert.Greater(t, n, 0, "encoded data should be non-empty")

	// Decode back.
	outPCM := make([]int16, frameSize*channels)
	samples, err := dec.Decode(buf[:n], outPCM, frameSize)
	require.NoError(t, err)
	assert.Equal(t, frameSize, samples, "decoded sample count should match frame size")
}

func TestOpusEncoderInvalidParams(t *testing.T) {
	if !opusAvailable {
		t.Skip("opus not available (built without cgo or with noopus tag)")
	}

	// Invalid sample rate.
	_, err := newOpusEncoder(0, 1)
	assert.Error(t, err)

	// Invalid channels.
	_, err = newOpusEncoder(48000, 0)
	assert.Error(t, err)
}

func TestOpusDecoderInvalidParams(t *testing.T) {
	if !opusAvailable {
		t.Skip("opus not available (built without cgo or with noopus tag)")
	}

	// Invalid sample rate.
	_, err := newOpusDecoder(0, 1)
	assert.Error(t, err)

	// Invalid channels.
	_, err = newOpusDecoder(48000, 0)
	assert.Error(t, err)
}

func TestOpusStubErrors(t *testing.T) {
	if opusAvailable {
		t.Skip("opus is available, stub test not applicable")
	}

	_, err := newOpusEncoder(48000, 1)
	assert.ErrorIs(t, err, errOpusNotAvailable)

	_, err = newOpusDecoder(48000, 1)
	assert.ErrorIs(t, err, errOpusNotAvailable)
}
