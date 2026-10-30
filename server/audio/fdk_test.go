//go:build cgo

package audio

import (
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFDKDecoderCreation(t *testing.T) {
	dec, err := NewFDKDecoder(48000, 2)
	require.NoError(t, err)
	require.NotNil(t, dec)
	defer func() { _ = dec.Close() }()

	// Verify it implements Decoder.
	var _ Decoder = dec
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

	// Verify it implements Encoder.
	var _ Encoder = enc
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
		// Copy to avoid aliasing — Decode() reuses its internal buffer,
		// so pcm is only valid until the next Decode() call.
		lastPCM = append([]float32{}, pcm...)
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

func TestFDKDecoderConcurrentClose(t *testing.T) {
	// Verify that calling Close from multiple goroutines concurrently
	// does not panic or double-free the C handle.
	dec, err := NewFDKDecoder(48000, 2)
	require.NoError(t, err)

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_ = dec.Close()
		}()
	}
	wg.Wait()
}

func TestFDKEncoderConcurrentClose(t *testing.T) {
	// Verify that calling Close from multiple goroutines concurrently
	// does not panic or double-free the C handle.
	enc, err := NewFDKEncoder(48000, 2)
	require.NoError(t, err)

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_ = enc.Close()
		}()
	}
	wg.Wait()
}

func TestFDKDecoderDecodeAfterClose(t *testing.T) {
	// Create encoder to produce valid AAC data for the decoder.
	enc, err := NewFDKEncoder(48000, 2)
	require.NoError(t, err)
	defer func() { _ = enc.Close() }()

	silence := make([]float32, 1024*2)
	aac, err := enc.Encode(silence)
	require.NoError(t, err)

	dec, err := NewFDKDecoder(48000, 2)
	require.NoError(t, err)
	require.NoError(t, dec.Close())

	_, err = dec.Decode(aac)
	require.Error(t, err)
	require.Contains(t, err.Error(), "closed")
}

func TestFDKEncoderEncodeAfterClose(t *testing.T) {
	enc, err := NewFDKEncoder(48000, 2)
	require.NoError(t, err)
	require.NoError(t, enc.Close())

	silence := make([]float32, 1024*2)
	_, err = enc.Encode(silence)
	require.Error(t, err)
	require.Contains(t, err.Error(), "closed")
}

// The encoder delay FDK reports is how far its frames lag their PCM: a tone
// burst encoded by FDK and decoded by FFmpeg, whose AAC decoder adds no
// delay of its own, comes out exactly Delay samples later than it went in.
// Uncompensated, it put every output's program audio 42.7 ms behind video.
func TestFDKEncoderDelayMatchesDecodedStream(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not found")
	}
	enc, err := NewFDKEncoder(48000, 2)
	require.NoError(t, err)
	defer func() { _ = enc.Close() }()

	const onset = 10 * 1024
	var adts []byte
	for f := 0; f < 30; f++ {
		pcm := make([]float32, 2048)
		for i := 0; i < 1024; i++ {
			if s := f*1024 + i; s >= onset && s < onset+480 {
				v := float32(0.8 * math.Sin(2*math.Pi*1000*float64(s-onset)/48000))
				pcm[2*i], pcm[2*i+1] = v, v
			}
		}
		aac, err := enc.Encode(pcm)
		require.NoError(t, err)
		adts = append(adts, aac...)
	}
	require.Positive(t, enc.Delay())

	path := filepath.Join(t.TempDir(), "burst.aac")
	require.NoError(t, os.WriteFile(path, adts, 0o644))
	raw, err := exec.Command(ffmpegPath, "-v", "error", "-i", path, "-ac", "1", "-f", "s16le", "-").Output()
	require.NoError(t, err)

	first := -1
	for i := 0; i+1 < len(raw); i += 2 {
		if v := int16(binary.LittleEndian.Uint16(raw[i:])); v > 1600 || v < -1600 {
			first = i / 2
			break
		}
	}
	require.GreaterOrEqual(t, first, 0, "the burst decodes")
	// The burst rises from zero, so its first sample above the threshold
	// sits a few samples in.
	require.InDelta(t, onset+enc.Delay(), first, 8)
}
