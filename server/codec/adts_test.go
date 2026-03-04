package codec

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsADTS_ValidHeader(t *testing.T) {
	data := []byte{0xFF, 0xF1, 0x50, 0x80, 0x02, 0x1F, 0xFC, 0xDE, 0x04}
	require.True(t, IsADTS(data))
}

func TestIsADTS_NoSyncWord(t *testing.T) {
	data := []byte{0x00, 0x01, 0x02, 0x03, 0x04}
	require.False(t, IsADTS(data))
}

func TestIsADTS_TooShort(t *testing.T) {
	require.False(t, IsADTS(nil))
	require.False(t, IsADTS([]byte{0xFF}))
}

func TestBuildADTS_48kHz_Stereo(t *testing.T) {
	frameLen := 100
	header := BuildADTS(48000, 2, frameLen)
	require.Len(t, header, 7)
	require.Equal(t, byte(0xFF), header[0])
	require.Equal(t, byte(0xF1), header[1])
	totalLen := (int(header[3]&0x03) << 11) | (int(header[4]) << 3) | (int(header[5]) >> 5)
	require.Equal(t, 107, totalLen)
}

func TestBuildADTS_44100Hz_Mono(t *testing.T) {
	header := BuildADTS(44100, 1, 50)
	require.Len(t, header, 7)
	require.Equal(t, byte(0xFF), header[0])
}

func TestEnsureADTS_AlreadyHasHeader(t *testing.T) {
	original := []byte{0xFF, 0xF1, 0x50, 0x80, 0x02, 0x1F, 0xFC, 0xDE, 0x04}
	result := EnsureADTS(original, 48000, 2)
	require.Equal(t, original, result)
}

func TestEnsureADTS_NoHeader(t *testing.T) {
	raw := []byte{0xDE, 0x04, 0x00, 0x26, 0x20}
	result := EnsureADTS(raw, 48000, 2)
	require.True(t, IsADTS(result))
	require.Len(t, result, 7+len(raw))
	require.Equal(t, raw, result[7:])
}

func TestSampleRateIndex(t *testing.T) {
	tests := []struct {
		rate  int
		index int
	}{
		{96000, 0}, {88200, 1}, {48000, 3}, {44100, 4},
		{32000, 5}, {24000, 6}, {16000, 8}, {8000, 11},
		{12345, 15},
	}
	for _, tt := range tests {
		require.Equal(t, tt.index, sampleRateIndex(tt.rate), "rate=%d", tt.rate)
	}
}
