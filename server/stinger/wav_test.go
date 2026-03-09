package stinger

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// putInt16LE writes a signed int16 in little-endian format.
func putInt16LE(buf []byte, v int16) {
	binary.LittleEndian.PutUint16(buf, uint16(v))
}

// buildWAV constructs a minimal valid WAV file from the given parameters.
// samples contains raw sample bytes (int16 LE or float32 LE depending on bitsPerSample).
func buildWAV(t *testing.T, sampleRate, channels, bitsPerSample int, samples []byte) []byte {
	t.Helper()

	var audioFormat uint16
	switch bitsPerSample {
	case 16:
		audioFormat = 1 // PCM
	case 32:
		audioFormat = 3 // IEEE float
	default:
		t.Fatalf("unsupported bitsPerSample: %d", bitsPerSample)
	}

	blockAlign := channels * (bitsPerSample / 8)
	byteRate := sampleRate * blockAlign
	dataSize := len(samples)

	// fmt chunk: 24 bytes (8 header + 16 body)
	// data chunk: 8 header + dataSize
	// RIFF header: 12 bytes
	totalSize := 12 + 24 + 8 + dataSize

	buf := make([]byte, totalSize)
	off := 0

	// RIFF header
	copy(buf[off:], "RIFF")
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], uint32(totalSize-8))
	off += 4
	copy(buf[off:], "WAVE")
	off += 4

	// fmt chunk
	copy(buf[off:], "fmt ")
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], 16) // chunk size
	off += 4
	binary.LittleEndian.PutUint16(buf[off:], audioFormat)
	off += 2
	binary.LittleEndian.PutUint16(buf[off:], uint16(channels))
	off += 2
	binary.LittleEndian.PutUint32(buf[off:], uint32(sampleRate))
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], uint32(byteRate))
	off += 4
	binary.LittleEndian.PutUint16(buf[off:], uint16(blockAlign))
	off += 2
	binary.LittleEndian.PutUint16(buf[off:], uint16(bitsPerSample))
	off += 2

	// data chunk
	copy(buf[off:], "data")
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], uint32(dataSize))
	off += 4
	copy(buf[off:], samples)

	return buf
}

func TestParseWAV_Int16Stereo(t *testing.T) {
	// Build stereo 48kHz int16 WAV with known samples.
	// 4 samples: L=1000, R=-1000, L=16384, R=-16384
	rawSamples := make([]byte, 8) // 4 samples * 2 bytes each
	putInt16LE(rawSamples[0:], 1000)
	putInt16LE(rawSamples[2:], -1000)
	putInt16LE(rawSamples[4:], 16384)
	putInt16LE(rawSamples[6:], -16384)

	wav := buildWAV(t, 48000, 2, 16, rawSamples)

	pcm, sampleRate, chans, err := ParseWAV(wav)
	require.NoError(t, err)
	require.Equal(t, 48000, sampleRate)
	require.Equal(t, 2, chans)
	require.Len(t, pcm, 4)

	// int16 -> float32: divide by 32768.0
	require.InDelta(t, 1000.0/32768.0, pcm[0], 1e-6)
	require.InDelta(t, -1000.0/32768.0, pcm[1], 1e-6)
	require.InDelta(t, 16384.0/32768.0, pcm[2], 1e-6)
	require.InDelta(t, -16384.0/32768.0, pcm[3], 1e-6)
}

func TestParseWAV_Float32Mono(t *testing.T) {
	// Build mono 48kHz float32 WAV with known samples.
	// 3 samples: 0.5, -0.25, 1.0
	rawSamples := make([]byte, 12) // 3 samples * 4 bytes each
	binary.LittleEndian.PutUint32(rawSamples[0:], math.Float32bits(0.5))
	binary.LittleEndian.PutUint32(rawSamples[4:], math.Float32bits(-0.25))
	binary.LittleEndian.PutUint32(rawSamples[8:], math.Float32bits(1.0))

	wav := buildWAV(t, 48000, 1, 32, rawSamples)

	pcm, sampleRate, chans, err := ParseWAV(wav)
	require.NoError(t, err)
	require.Equal(t, 48000, sampleRate)
	require.Equal(t, 1, chans)
	require.Len(t, pcm, 3)

	require.InDelta(t, 0.5, pcm[0], 1e-7)
	require.InDelta(t, -0.25, pcm[1], 1e-7)
	require.InDelta(t, 1.0, pcm[2], 1e-7)
}

func TestParseWAV_Invalid(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"too short", []byte{0, 1, 2}},
		{"not RIFF", []byte("XXXX\x00\x00\x00\x00WAVE")},
		{"not WAVE", []byte("RIFF\x00\x00\x00\x00XXXX")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, err := ParseWAV(tt.data)
			require.Error(t, err)
		})
	}
}

func TestParseWAV_UnsupportedFormat(t *testing.T) {
	// Build a WAV with audioFormat=2 (ADPCM), which should be rejected.
	wav := buildWAV(t, 48000, 1, 16, make([]byte, 4))
	// Patch audioFormat to 2 (offset 20 in the file: 12 RIFF header + 8 fmt header = 20)
	binary.LittleEndian.PutUint16(wav[20:], 2)

	_, _, _, err := ParseWAV(wav)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported")
}

func TestParseWAV_NoFmtChunk(t *testing.T) {
	// RIFF/WAVE header with a "junk" chunk but no "fmt " chunk
	buf := make([]byte, 12+8+4) // RIFF header + junk chunk (4 bytes data)
	copy(buf[0:], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:], uint32(len(buf)-8))
	copy(buf[8:], "WAVE")
	copy(buf[12:], "junk")
	binary.LittleEndian.PutUint32(buf[16:], 4) // chunk size
	// 4 bytes of junk data at [20:24]

	_, _, _, err := ParseWAV(buf)
	require.Error(t, err)
	require.Contains(t, err.Error(), "fmt")
}

func TestParseWAV_NoDataChunk(t *testing.T) {
	// RIFF/WAVE with fmt chunk but no data chunk
	buf := make([]byte, 12+24) // RIFF header + fmt chunk only
	copy(buf[0:], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:], uint32(len(buf)-8))
	copy(buf[8:], "WAVE")
	// fmt chunk
	copy(buf[12:], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:], 16)
	binary.LittleEndian.PutUint16(buf[20:], 1)  // PCM
	binary.LittleEndian.PutUint16(buf[22:], 2)  // stereo
	binary.LittleEndian.PutUint32(buf[24:], 48000)
	binary.LittleEndian.PutUint32(buf[28:], 192000)
	binary.LittleEndian.PutUint16(buf[32:], 4) // blockAlign
	binary.LittleEndian.PutUint16(buf[34:], 16)

	_, _, _, err := ParseWAV(buf)
	require.Error(t, err)
	require.Contains(t, err.Error(), "data")
}

func TestParseWAV_ChunkAlignment(t *testing.T) {
	// Test that the parser handles odd-sized chunks correctly.
	// Insert a 5-byte "junk" chunk before the data chunk. The parser must skip
	// to the next word-aligned boundary (6 bytes) to find the data chunk.
	sampleRate := 44100
	channels := 1
	bitsPerSample := 16

	rawSamples := make([]byte, 2) // 1 sample
	putInt16LE(rawSamples[0:], 12345)

	// Build manually: RIFF header + fmt chunk + junk chunk (odd size) + data chunk
	fmtSize := 24 // 8 header + 16 body
	junkBodySize := 5
	junkChunkSize := 8 + junkBodySize  // 13
	junkPadded := junkChunkSize + 1    // 14 (word-aligned)
	dataChunkSize := 8 + len(rawSamples)
	totalSize := 12 + fmtSize + junkPadded + dataChunkSize

	buf := make([]byte, totalSize)
	off := 0

	// RIFF header
	copy(buf[off:], "RIFF")
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], uint32(totalSize-8))
	off += 4
	copy(buf[off:], "WAVE")
	off += 4

	// fmt chunk
	copy(buf[off:], "fmt ")
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], 16)
	off += 4
	binary.LittleEndian.PutUint16(buf[off:], 1)  // PCM
	off += 2
	binary.LittleEndian.PutUint16(buf[off:], uint16(channels))
	off += 2
	binary.LittleEndian.PutUint32(buf[off:], uint32(sampleRate))
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], uint32(sampleRate*channels*bitsPerSample/8))
	off += 4
	binary.LittleEndian.PutUint16(buf[off:], uint16(channels*bitsPerSample/8))
	off += 2
	binary.LittleEndian.PutUint16(buf[off:], uint16(bitsPerSample))
	off += 2

	// junk chunk with odd size
	copy(buf[off:], "junk")
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], uint32(junkBodySize))
	off += 4
	off += junkBodySize
	off++ // padding byte for word alignment

	// data chunk
	copy(buf[off:], "data")
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], uint32(len(rawSamples)))
	off += 4
	copy(buf[off:], rawSamples)

	pcm, sr, ch, err := ParseWAV(buf)
	require.NoError(t, err)
	require.Equal(t, 44100, sr)
	require.Equal(t, 1, ch)
	require.Len(t, pcm, 1)
	require.InDelta(t, 12345.0/32768.0, pcm[0], 1e-6)
}

func TestParseWAV_44100Hz(t *testing.T) {
	// Verify a different sample rate works.
	rawSamples := make([]byte, 4) // 2 int16 samples
	putInt16LE(rawSamples[0:], 100)
	putInt16LE(rawSamples[2:], -100)

	wav := buildWAV(t, 44100, 1, 16, rawSamples)

	pcm, sampleRate, chans, err := ParseWAV(wav)
	require.NoError(t, err)
	require.Equal(t, 44100, sampleRate)
	require.Equal(t, 1, chans)
	require.Len(t, pcm, 2)
}
