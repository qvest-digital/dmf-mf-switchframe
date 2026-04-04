package fastctrl

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// Helper to build a DVE slot transform datagram payload (25 bytes, no msg type).
func buildDVESlotTransformPayload(slotID byte, posX, posY, w, h, rot, opacity float32) []byte {
	buf := make([]byte, 25)
	buf[0] = slotID
	binary.BigEndian.PutUint32(buf[1:5], math.Float32bits(posX))
	binary.BigEndian.PutUint32(buf[5:9], math.Float32bits(posY))
	binary.BigEndian.PutUint32(buf[9:13], math.Float32bits(w))
	binary.BigEndian.PutUint32(buf[13:17], math.Float32bits(h))
	binary.BigEndian.PutUint32(buf[17:21], math.Float32bits(rot))
	binary.BigEndian.PutUint32(buf[21:25], math.Float32bits(opacity))
	return buf
}

// Helper to build a DVE single param datagram payload (7 bytes, no msg type).
func buildDVESingleParamPayload(slotID byte, paramID uint16, value float32) []byte {
	buf := make([]byte, 7)
	buf[0] = slotID
	binary.BigEndian.PutUint16(buf[1:3], paramID)
	binary.BigEndian.PutUint32(buf[3:7], math.Float32bits(value))
	return buf
}

func TestParseDVESlotTransform_Valid(t *testing.T) {
	payload := buildDVESlotTransformPayload(3, 100.5, 200.25, 480.0, 270.0, 45.0, 0.75)

	result, err := ParseDVESlotTransform(payload)
	require.NoError(t, err)
	require.Equal(t, 3, result.SlotID)
	require.InDelta(t, 100.5, float64(result.PositionX), 0.01)
	require.InDelta(t, 200.25, float64(result.PositionY), 0.01)
	require.InDelta(t, 480.0, float64(result.Width), 0.01)
	require.InDelta(t, 270.0, float64(result.Height), 0.01)
	require.InDelta(t, 45.0, float64(result.Rotation), 0.01)
	require.InDelta(t, 0.75, float64(result.Opacity), 0.01)
}

func TestParseDVESlotTransform_TooShort(t *testing.T) {
	_, err := ParseDVESlotTransform([]byte{0x03, 0x00, 0x01})
	require.Error(t, err)
	require.Contains(t, err.Error(), "need 25 bytes")
}

func TestParseDVESlotTransform_SlotOutOfRange(t *testing.T) {
	payload := buildDVESlotTransformPayload(8, 100, 200, 480, 270, 0, 1.0) // slot 8 is out of range
	_, err := ParseDVESlotTransform(payload)
	require.Error(t, err)
	require.Contains(t, err.Error(), "out of range")
}

func TestParseDVESlotTransform_ZeroWidth(t *testing.T) {
	payload := buildDVESlotTransformPayload(0, 100, 200, 0, 270, 0, 1.0)
	_, err := ParseDVESlotTransform(payload)
	require.Error(t, err)
	require.Contains(t, err.Error(), "width must be > 0")
}

func TestParseDVESlotTransform_NegativeWidth(t *testing.T) {
	payload := buildDVESlotTransformPayload(0, 100, 200, -10, 270, 0, 1.0)
	_, err := ParseDVESlotTransform(payload)
	require.Error(t, err)
	require.Contains(t, err.Error(), "width must be > 0")
}

func TestParseDVESlotTransform_ZeroHeight(t *testing.T) {
	payload := buildDVESlotTransformPayload(0, 100, 200, 480, 0, 0, 1.0)
	_, err := ParseDVESlotTransform(payload)
	require.Error(t, err)
	require.Contains(t, err.Error(), "height must be > 0")
}

func TestParseDVESlotTransform_OpacityOutOfRange(t *testing.T) {
	payload := buildDVESlotTransformPayload(0, 100, 200, 480, 270, 0, 1.5)
	_, err := ParseDVESlotTransform(payload)
	require.Error(t, err)
	require.Contains(t, err.Error(), "opacity must be in [0, 1]")
}

func TestParseDVESlotTransform_NegativeOpacity(t *testing.T) {
	payload := buildDVESlotTransformPayload(0, 100, 200, 480, 270, 0, -0.1)
	_, err := ParseDVESlotTransform(payload)
	require.Error(t, err)
	require.Contains(t, err.Error(), "opacity must be in [0, 1]")
}

func TestParseDVESlotTransform_ZeroOpacity(t *testing.T) {
	// Opacity = 0 is valid (fully transparent).
	payload := buildDVESlotTransformPayload(0, 100, 200, 480, 270, 0, 0)
	result, err := ParseDVESlotTransform(payload)
	require.NoError(t, err)
	require.InDelta(t, 0.0, float64(result.Opacity), 0.001)
}

func TestParseDVESlotTransform_NaN(t *testing.T) {
	payload := make([]byte, 25)
	payload[0] = 0
	binary.BigEndian.PutUint32(payload[1:5], math.Float32bits(float32(math.NaN())))
	binary.BigEndian.PutUint32(payload[5:9], math.Float32bits(0))
	binary.BigEndian.PutUint32(payload[9:13], math.Float32bits(100))
	binary.BigEndian.PutUint32(payload[13:17], math.Float32bits(100))
	binary.BigEndian.PutUint32(payload[17:21], math.Float32bits(0))
	binary.BigEndian.PutUint32(payload[21:25], math.Float32bits(1.0))
	_, err := ParseDVESlotTransform(payload)
	require.Error(t, err)
	require.Contains(t, err.Error(), "NaN")
}

func TestParseDVESlotTransform_Inf(t *testing.T) {
	payload := make([]byte, 25)
	payload[0] = 0
	binary.BigEndian.PutUint32(payload[1:5], math.Float32bits(float32(math.Inf(1))))
	binary.BigEndian.PutUint32(payload[5:9], math.Float32bits(0))
	binary.BigEndian.PutUint32(payload[9:13], math.Float32bits(100))
	binary.BigEndian.PutUint32(payload[13:17], math.Float32bits(100))
	binary.BigEndian.PutUint32(payload[17:21], math.Float32bits(0))
	binary.BigEndian.PutUint32(payload[21:25], math.Float32bits(1.0))
	_, err := ParseDVESlotTransform(payload)
	require.Error(t, err)
	require.Contains(t, err.Error(), "Inf")
}

func TestParseDVESlotTransform_NegInf(t *testing.T) {
	payload := make([]byte, 25)
	payload[0] = 0
	binary.BigEndian.PutUint32(payload[1:5], math.Float32bits(0))
	binary.BigEndian.PutUint32(payload[5:9], math.Float32bits(float32(math.Inf(-1))))
	binary.BigEndian.PutUint32(payload[9:13], math.Float32bits(100))
	binary.BigEndian.PutUint32(payload[13:17], math.Float32bits(100))
	binary.BigEndian.PutUint32(payload[17:21], math.Float32bits(0))
	binary.BigEndian.PutUint32(payload[21:25], math.Float32bits(1.0))
	_, err := ParseDVESlotTransform(payload)
	require.Error(t, err)
	require.Contains(t, err.Error(), "Inf")
}

func TestParseDVESlotTransform_MaxSlot(t *testing.T) {
	// Slot 7 is the maximum valid slot (MaxSlots = 8).
	payload := buildDVESlotTransformPayload(7, 0, 0, 100, 100, 0, 1.0)
	result, err := ParseDVESlotTransform(payload)
	require.NoError(t, err)
	require.Equal(t, 7, result.SlotID)
}

func TestParseDVESingleParam_Valid(t *testing.T) {
	payload := buildDVESingleParamPayload(2, DVEParamRotationDeg, 90.0)

	result, err := ParseDVESingleParam(payload)
	require.NoError(t, err)
	require.Equal(t, 2, result.SlotID)
	require.Equal(t, DVEParamRotationDeg, result.ParamID)
	require.InDelta(t, 90.0, float64(result.Value), 0.01)
}

func TestParseDVESingleParam_TooShort(t *testing.T) {
	_, err := ParseDVESingleParam([]byte{0x02, 0x00})
	require.Error(t, err)
	require.Contains(t, err.Error(), "need 7 bytes")
}

func TestParseDVESingleParam_SlotOutOfRange(t *testing.T) {
	payload := buildDVESingleParamPayload(8, DVEParamOpacity, 0.5) // slot 8 out of range
	_, err := ParseDVESingleParam(payload)
	require.Error(t, err)
	require.Contains(t, err.Error(), "out of range")
}

func TestParseDVESingleParam_NaN(t *testing.T) {
	payload := make([]byte, 7)
	payload[0] = 0
	binary.BigEndian.PutUint16(payload[1:3], DVEParamOpacity)
	binary.BigEndian.PutUint32(payload[3:7], math.Float32bits(float32(math.NaN())))
	_, err := ParseDVESingleParam(payload)
	require.Error(t, err)
	require.Contains(t, err.Error(), "NaN")
}

func TestParseDVESingleParam_Inf(t *testing.T) {
	payload := make([]byte, 7)
	payload[0] = 0
	binary.BigEndian.PutUint16(payload[1:3], DVEParamOpacity)
	binary.BigEndian.PutUint32(payload[3:7], math.Float32bits(float32(math.Inf(1))))
	_, err := ParseDVESingleParam(payload)
	require.Error(t, err)
	require.Contains(t, err.Error(), "Inf")
}

func TestParseDVESingleParam_AllParams(t *testing.T) {
	params := []struct {
		id   uint16
		name string
	}{
		{DVEParamPositionX, "PositionX"},
		{DVEParamPositionY, "PositionY"},
		{DVEParamWidth, "Width"},
		{DVEParamHeight, "Height"},
		{DVEParamRotationDeg, "RotationDeg"},
		{DVEParamPerspectiveX, "PerspectiveX"},
		{DVEParamPerspectiveY, "PerspectiveY"},
		{DVEParamCropTop, "CropTop"},
		{DVEParamCropBottom, "CropBottom"},
		{DVEParamCropLeft, "CropLeft"},
		{DVEParamCropRight, "CropRight"},
		{DVEParamOpacity, "Opacity"},
		{DVEParamDefocusRadius, "DefocusRadius"},
		{DVEParamBorderWidth, "BorderWidth"},
		{DVEParamCornerRadius, "CornerRadius"},
	}
	for _, p := range params {
		t.Run(p.name, func(t *testing.T) {
			payload := buildDVESingleParamPayload(0, p.id, 42.5)
			result, err := ParseDVESingleParam(payload)
			require.NoError(t, err)
			require.Equal(t, p.id, result.ParamID)
			require.InDelta(t, 42.5, float64(result.Value), 0.01)
		})
	}
}

func TestDVESlotTransform_RoundTrip(t *testing.T) {
	// Encode values, parse, and verify they match.
	posX := float32(123.456)
	posY := float32(-78.9)
	w := float32(640.0)
	h := float32(360.0)
	rot := float32(-30.0)
	opacity := float32(0.85)

	payload := buildDVESlotTransformPayload(5, posX, posY, w, h, rot, opacity)
	result, err := ParseDVESlotTransform(payload)
	require.NoError(t, err)
	require.Equal(t, 5, result.SlotID)
	require.Equal(t, posX, result.PositionX)
	require.Equal(t, posY, result.PositionY)
	require.Equal(t, w, result.Width)
	require.Equal(t, h, result.Height)
	require.Equal(t, rot, result.Rotation)
	require.Equal(t, opacity, result.Opacity)
}

func TestDVESingleParam_RoundTrip(t *testing.T) {
	value := float32(0.12345)
	payload := buildDVESingleParamPayload(1, DVEParamOpacity, value)
	result, err := ParseDVESingleParam(payload)
	require.NoError(t, err)
	require.Equal(t, 1, result.SlotID)
	require.Equal(t, DVEParamOpacity, result.ParamID)
	require.Equal(t, value, result.Value)
}
