package codec

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsADTS_ValidHeader(t *testing.T) {
	t.Parallel()
	data := []byte{0xFF, 0xF1, 0x50, 0x80, 0x02, 0x1F, 0xFC, 0xDE, 0x04}
	require.True(t, IsADTS(data))
}

func TestIsADTS_NoSyncWord(t *testing.T) {
	t.Parallel()
	data := []byte{0x00, 0x01, 0x02, 0x03, 0x04}
	require.False(t, IsADTS(data))
}

func TestIsADTS_TooShort(t *testing.T) {
	t.Parallel()
	require.False(t, IsADTS(nil))
	require.False(t, IsADTS([]byte{0xFF}))
}

func TestBuildADTS_48kHz_Stereo(t *testing.T) {
	t.Parallel()
	frameLen := 100
	header := BuildADTS(48000, 2, frameLen)
	require.Len(t, header, 7)
	require.Equal(t, byte(0xFF), header[0])
	require.Equal(t, byte(0xF1), header[1])
	totalLen := (int(header[3]&0x03) << 11) | (int(header[4]) << 3) | (int(header[5]) >> 5)
	require.Equal(t, 107, totalLen)
}

func TestBuildADTS_44100Hz_Mono(t *testing.T) {
	t.Parallel()
	header := BuildADTS(44100, 1, 50)
	require.Len(t, header, 7)
	require.Equal(t, byte(0xFF), header[0])
}

func TestEnsureADTS_AlreadyHasHeader(t *testing.T) {
	t.Parallel()
	original := []byte{0xFF, 0xF1, 0x50, 0x80, 0x02, 0x1F, 0xFC, 0xDE, 0x04}
	result := EnsureADTS(original, 48000, 2)
	require.Equal(t, original, result)
}

func TestEnsureADTS_NoHeader(t *testing.T) {
	t.Parallel()
	raw := []byte{0xDE, 0x04, 0x00, 0x26, 0x20}
	result := EnsureADTS(raw, 48000, 2)
	require.True(t, IsADTS(result))
	require.Len(t, result, 7+len(raw))
	require.Equal(t, raw, result[7:])
}

func TestADTSFrameLen(t *testing.T) {
	t.Parallel()
	// Build a header for a 100-byte payload, total = 107 (100 + 7 header)
	header := BuildADTS(48000, 2, 100)
	require.Equal(t, 107, ADTSFrameLen(header))

	// Too short
	require.Equal(t, 0, ADTSFrameLen(nil))
	require.Equal(t, 0, ADTSFrameLen([]byte{0xFF}))

	// Not ADTS
	require.Equal(t, 0, ADTSFrameLen([]byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06}))
}

func TestSplitADTSFrames(t *testing.T) {
	t.Parallel()
	// Build 3 concatenated ADTS frames with distinct payloads
	payload1 := []byte{0x01, 0x02, 0x03}
	payload2 := []byte{0x04, 0x05, 0x06, 0x07}
	payload3 := []byte{0x08, 0x09}

	frame1 := append(BuildADTS(48000, 2, len(payload1)), payload1...)
	frame2 := append(BuildADTS(48000, 2, len(payload2)), payload2...)
	frame3 := append(BuildADTS(48000, 2, len(payload3)), payload3...)

	concat := make([]byte, 0, len(frame1)+len(frame2)+len(frame3))
	concat = append(concat, frame1...)
	concat = append(concat, frame2...)
	concat = append(concat, frame3...)

	result := SplitADTSFrames(concat)
	require.Len(t, result, 3)
	require.Equal(t, payload1, result[0])
	require.Equal(t, payload2, result[1])
	require.Equal(t, payload3, result[2])
}

func TestSplitADTSFrames_SingleFrame(t *testing.T) {
	t.Parallel()
	payload := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	frame := append(BuildADTS(48000, 2, len(payload)), payload...)

	result := SplitADTSFrames(frame)
	require.Len(t, result, 1)
	require.Equal(t, payload, result[0])
}

func TestSplitADTSFrames_NotADTS(t *testing.T) {
	t.Parallel()
	// Non-ADTS data returned as single raw payload
	raw := []byte{0xDE, 0x04, 0x00, 0x26, 0x20}
	result := SplitADTSFrames(raw)
	require.Len(t, result, 1)
	require.Equal(t, raw, result[0])
}

func TestSampleRateIndex(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		rate  int
		index int
	}{
		{"96000", 96000, 0}, {"88200", 88200, 1}, {"48000", 48000, 3}, {"44100", 44100, 4},
		{"32000", 32000, 5}, {"24000", 24000, 6}, {"16000", 16000, 8}, {"8000", 8000, 11},
		{"unknown", 12345, 15},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.index, sampleRateIndex(tt.rate), "rate=%d", tt.rate)
		})
	}
}
