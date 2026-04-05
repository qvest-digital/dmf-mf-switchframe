package fastctrl

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// ---------- 0x06 DVE Slot Source ----------

func buildDVESlotSourcePayload(slotID byte, key string) []byte {
	buf := make([]byte, 2+len(key))
	buf[0] = slotID
	buf[1] = byte(len(key))
	copy(buf[2:], key)
	return buf
}

func TestParseDVESlotSource_Valid(t *testing.T) {
	payload := buildDVESlotSourcePayload(3, "camera-1")
	result, err := ParseDVESlotSource(payload)
	require.NoError(t, err)
	require.Equal(t, 3, result.SlotID)
	require.Equal(t, "camera-1", result.SourceKey)
}

func TestParseDVESlotSource_EmptyKey(t *testing.T) {
	// Empty key is valid — means "unassign source".
	payload := buildDVESlotSourcePayload(0, "")
	result, err := ParseDVESlotSource(payload)
	require.NoError(t, err)
	require.Equal(t, 0, result.SlotID)
	require.Equal(t, "", result.SourceKey)
}

func TestParseDVESlotSource_MaxSlot(t *testing.T) {
	payload := buildDVESlotSourcePayload(7, "src")
	result, err := ParseDVESlotSource(payload)
	require.NoError(t, err)
	require.Equal(t, 7, result.SlotID)
}

func TestParseDVESlotSource_TooShort(t *testing.T) {
	_, err := ParseDVESlotSource([]byte{0x03})
	require.Error(t, err)
	require.Contains(t, err.Error(), "need at least 2 bytes")
}

func TestParseDVESlotSource_SlotOutOfRange(t *testing.T) {
	payload := buildDVESlotSourcePayload(8, "cam")
	_, err := ParseDVESlotSource(payload)
	require.Error(t, err)
	require.Contains(t, err.Error(), "out of range")
}

func TestParseDVESlotSource_TruncatedKey(t *testing.T) {
	// keyLen says 10 but only 3 bytes of key data follow.
	buf := []byte{0x00, 10, 'a', 'b', 'c'}
	_, err := ParseDVESlotSource(buf)
	require.Error(t, err)
	require.Contains(t, err.Error(), "need")
}

func TestParseDVESlotSource_LongKey(t *testing.T) {
	key := make([]byte, 255) // max representable by uint8
	for i := range key {
		key[i] = 'x'
	}
	payload := buildDVESlotSourcePayload(0, string(key))
	result, err := ParseDVESlotSource(payload)
	require.NoError(t, err)
	require.Len(t, result.SourceKey, 255)
}

// ---------- 0x07 DVE Slot Enable ----------

func buildDVESlotEnablePayload(slotID byte, enabled byte) []byte {
	return []byte{slotID, enabled}
}

func TestParseDVESlotEnable_Enabled(t *testing.T) {
	payload := buildDVESlotEnablePayload(2, 1)
	result, err := ParseDVESlotEnable(payload)
	require.NoError(t, err)
	require.Equal(t, 2, result.SlotID)
	require.True(t, result.Enabled)
}

func TestParseDVESlotEnable_Disabled(t *testing.T) {
	payload := buildDVESlotEnablePayload(5, 0)
	result, err := ParseDVESlotEnable(payload)
	require.NoError(t, err)
	require.Equal(t, 5, result.SlotID)
	require.False(t, result.Enabled)
}

func TestParseDVESlotEnable_MaxSlot(t *testing.T) {
	payload := buildDVESlotEnablePayload(7, 1)
	result, err := ParseDVESlotEnable(payload)
	require.NoError(t, err)
	require.Equal(t, 7, result.SlotID)
}

func TestParseDVESlotEnable_TooShort(t *testing.T) {
	_, err := ParseDVESlotEnable([]byte{0x02})
	require.Error(t, err)
	require.Contains(t, err.Error(), "need 2 bytes")
}

func TestParseDVESlotEnable_SlotOutOfRange(t *testing.T) {
	payload := buildDVESlotEnablePayload(8, 1)
	_, err := ParseDVESlotEnable(payload)
	require.Error(t, err)
	require.Contains(t, err.Error(), "out of range")
}

func TestParseDVESlotEnable_InvalidEnabled(t *testing.T) {
	payload := buildDVESlotEnablePayload(0, 2) // not 0 or 1
	_, err := ParseDVESlotEnable(payload)
	require.Error(t, err)
	require.Contains(t, err.Error(), "enabled must be 0 or 1")
}

// ---------- 0x08 DVE Scale Mode ----------

func buildDVEScaleModePayload(slotID, mode byte, anchorX, anchorY float32) []byte {
	buf := make([]byte, 10)
	buf[0] = slotID
	buf[1] = mode
	binary.BigEndian.PutUint32(buf[2:6], math.Float32bits(anchorX))
	binary.BigEndian.PutUint32(buf[6:10], math.Float32bits(anchorY))
	return buf
}

func TestParseDVEScaleMode_Stretch(t *testing.T) {
	payload := buildDVEScaleModePayload(0, 0, 0.5, 0.5)
	result, err := ParseDVEScaleMode(payload)
	require.NoError(t, err)
	require.Equal(t, 0, result.SlotID)
	require.Equal(t, "stretch", result.Mode)
	require.InDelta(t, 0.5, float64(result.AnchorX), 0.001)
	require.InDelta(t, 0.5, float64(result.AnchorY), 0.001)
}

func TestParseDVEScaleMode_Fill(t *testing.T) {
	payload := buildDVEScaleModePayload(1, 1, 0.0, 1.0)
	result, err := ParseDVEScaleMode(payload)
	require.NoError(t, err)
	require.Equal(t, "fill", result.Mode)
	require.InDelta(t, 0.0, float64(result.AnchorX), 0.001)
	require.InDelta(t, 1.0, float64(result.AnchorY), 0.001)
}

func TestParseDVEScaleMode_Fit(t *testing.T) {
	payload := buildDVEScaleModePayload(2, 2, 0.25, 0.75)
	result, err := ParseDVEScaleMode(payload)
	require.NoError(t, err)
	require.Equal(t, "fit", result.Mode)
}

func TestParseDVEScaleMode_MaxSlot(t *testing.T) {
	payload := buildDVEScaleModePayload(7, 0, 0.5, 0.5)
	result, err := ParseDVEScaleMode(payload)
	require.NoError(t, err)
	require.Equal(t, 7, result.SlotID)
}

func TestParseDVEScaleMode_TooShort(t *testing.T) {
	_, err := ParseDVEScaleMode([]byte{0x00, 0x01, 0x00})
	require.Error(t, err)
	require.Contains(t, err.Error(), "need 10 bytes")
}

func TestParseDVEScaleMode_SlotOutOfRange(t *testing.T) {
	payload := buildDVEScaleModePayload(8, 0, 0.5, 0.5)
	_, err := ParseDVEScaleMode(payload)
	require.Error(t, err)
	require.Contains(t, err.Error(), "out of range")
}

func TestParseDVEScaleMode_InvalidMode(t *testing.T) {
	payload := buildDVEScaleModePayload(0, 3, 0.5, 0.5) // mode 3 is invalid
	_, err := ParseDVEScaleMode(payload)
	require.Error(t, err)
	require.Contains(t, err.Error(), "mode must be 0-2")
}

func TestParseDVEScaleMode_AnchorXTooHigh(t *testing.T) {
	payload := buildDVEScaleModePayload(0, 0, 1.5, 0.5)
	_, err := ParseDVEScaleMode(payload)
	require.Error(t, err)
	require.Contains(t, err.Error(), "anchorX must be in [0, 1]")
}

func TestParseDVEScaleMode_AnchorYNegative(t *testing.T) {
	payload := buildDVEScaleModePayload(0, 0, 0.5, -0.1)
	_, err := ParseDVEScaleMode(payload)
	require.Error(t, err)
	require.Contains(t, err.Error(), "anchorY must be in [0, 1]")
}

func TestParseDVEScaleMode_NaN(t *testing.T) {
	payload := buildDVEScaleModePayload(0, 0, float32(math.NaN()), 0.5)
	_, err := ParseDVEScaleMode(payload)
	require.Error(t, err)
	require.Contains(t, err.Error(), "NaN")
}

func TestParseDVEScaleMode_Inf(t *testing.T) {
	payload := buildDVEScaleModePayload(0, 0, 0.5, float32(math.Inf(1)))
	_, err := ParseDVEScaleMode(payload)
	require.Error(t, err)
	require.Contains(t, err.Error(), "Inf")
}

// ---------- 0x09 DVE Border ----------

func buildDVEBorderPayload(slotID byte, width, radius float32, y, cb, cr byte) []byte {
	buf := make([]byte, 12)
	buf[0] = slotID
	binary.BigEndian.PutUint32(buf[1:5], math.Float32bits(width))
	binary.BigEndian.PutUint32(buf[5:9], math.Float32bits(radius))
	buf[9] = y
	buf[10] = cb
	buf[11] = cr
	return buf
}

func TestParseDVEBorder_Valid(t *testing.T) {
	payload := buildDVEBorderPayload(4, 3.0, 8.0, 235, 128, 128)
	result, err := ParseDVEBorder(payload)
	require.NoError(t, err)
	require.Equal(t, 4, result.SlotID)
	require.InDelta(t, 3.0, float64(result.Width), 0.001)
	require.InDelta(t, 8.0, float64(result.Radius), 0.001)
	require.Equal(t, byte(235), result.ColorY)
	require.Equal(t, byte(128), result.ColorCb)
	require.Equal(t, byte(128), result.ColorCr)
}

func TestParseDVEBorder_ZeroWidthRadius(t *testing.T) {
	// Zero width and radius are valid (means no border).
	payload := buildDVEBorderPayload(0, 0.0, 0.0, 16, 128, 128)
	result, err := ParseDVEBorder(payload)
	require.NoError(t, err)
	require.InDelta(t, 0.0, float64(result.Width), 0.001)
	require.InDelta(t, 0.0, float64(result.Radius), 0.001)
}

func TestParseDVEBorder_MaxSlot(t *testing.T) {
	payload := buildDVEBorderPayload(7, 1.0, 1.0, 128, 128, 128)
	result, err := ParseDVEBorder(payload)
	require.NoError(t, err)
	require.Equal(t, 7, result.SlotID)
}

func TestParseDVEBorder_TooShort(t *testing.T) {
	_, err := ParseDVEBorder([]byte{0x00, 0x01, 0x02, 0x03})
	require.Error(t, err)
	require.Contains(t, err.Error(), "need 12 bytes")
}

func TestParseDVEBorder_SlotOutOfRange(t *testing.T) {
	payload := buildDVEBorderPayload(8, 1.0, 1.0, 128, 128, 128)
	_, err := ParseDVEBorder(payload)
	require.Error(t, err)
	require.Contains(t, err.Error(), "out of range")
}

func TestParseDVEBorder_NegativeWidth(t *testing.T) {
	payload := buildDVEBorderPayload(0, -1.0, 1.0, 128, 128, 128)
	_, err := ParseDVEBorder(payload)
	require.Error(t, err)
	require.Contains(t, err.Error(), "width must be >= 0")
}

func TestParseDVEBorder_NegativeRadius(t *testing.T) {
	payload := buildDVEBorderPayload(0, 1.0, -1.0, 128, 128, 128)
	_, err := ParseDVEBorder(payload)
	require.Error(t, err)
	require.Contains(t, err.Error(), "radius must be >= 0")
}

func TestParseDVEBorder_NaN(t *testing.T) {
	payload := buildDVEBorderPayload(0, float32(math.NaN()), 1.0, 128, 128, 128)
	_, err := ParseDVEBorder(payload)
	require.Error(t, err)
	require.Contains(t, err.Error(), "NaN")
}

func TestParseDVEBorder_Inf(t *testing.T) {
	payload := buildDVEBorderPayload(0, 1.0, float32(math.Inf(1)), 128, 128, 128)
	_, err := ParseDVEBorder(payload)
	require.Error(t, err)
	require.Contains(t, err.Error(), "Inf")
}

func TestParseDVEBorder_RoundTrip(t *testing.T) {
	width := float32(5.5)
	radius := float32(12.25)
	payload := buildDVEBorderPayload(6, width, radius, 200, 100, 150)
	result, err := ParseDVEBorder(payload)
	require.NoError(t, err)
	require.Equal(t, 6, result.SlotID)
	require.Equal(t, width, result.Width)
	require.Equal(t, radius, result.Radius)
	require.Equal(t, byte(200), result.ColorY)
	require.Equal(t, byte(100), result.ColorCb)
	require.Equal(t, byte(150), result.ColorCr)
}

func TestParseDVEScaleMode_RoundTrip(t *testing.T) {
	anchorX := float32(0.333)
	anchorY := float32(0.667)
	payload := buildDVEScaleModePayload(4, 2, anchorX, anchorY)
	result, err := ParseDVEScaleMode(payload)
	require.NoError(t, err)
	require.Equal(t, 4, result.SlotID)
	require.Equal(t, "fit", result.Mode)
	require.Equal(t, anchorX, result.AnchorX)
	require.Equal(t, anchorY, result.AnchorY)
}

func TestParseDVESlotSource_RoundTrip(t *testing.T) {
	payload := buildDVESlotSourcePayload(6, "srt-input-1")
	result, err := ParseDVESlotSource(payload)
	require.NoError(t, err)
	require.Equal(t, 6, result.SlotID)
	require.Equal(t, "srt-input-1", result.SourceKey)
}
