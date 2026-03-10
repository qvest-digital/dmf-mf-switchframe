package fastctrl

import (
	"encoding/binary"
	"image"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseLayoutSlotPosition(t *testing.T) {
	payload := make([]byte, 9)
	payload[0] = 2
	binary.BigEndian.PutUint16(payload[1:3], 100)
	binary.BigEndian.PutUint16(payload[3:5], 200)
	binary.BigEndian.PutUint16(payload[5:7], 480)
	binary.BigEndian.PutUint16(payload[7:9], 270)
	slotID, rect, err := ParseLayoutSlotPosition(payload)
	require.NoError(t, err)
	require.Equal(t, 2, slotID)
	require.Equal(t, image.Rect(100, 200, 580, 470), rect)
}

func TestParseLayoutSlotPosition_TooShort(t *testing.T) {
	_, _, err := ParseLayoutSlotPosition([]byte{0x00, 0x01})
	require.Error(t, err)
}

func TestParseLayoutSlotPosition_OddValues(t *testing.T) {
	payload := make([]byte, 9)
	payload[0] = 0
	binary.BigEndian.PutUint16(payload[1:3], 101)
	binary.BigEndian.PutUint16(payload[3:5], 200)
	binary.BigEndian.PutUint16(payload[5:7], 480)
	binary.BigEndian.PutUint16(payload[7:9], 270)
	_, _, err := ParseLayoutSlotPosition(payload)
	require.Error(t, err)
}

func TestParseLayoutSlotPosition_SlotOutOfRange(t *testing.T) {
	payload := make([]byte, 9)
	payload[0] = 5
	binary.BigEndian.PutUint16(payload[1:3], 100)
	binary.BigEndian.PutUint16(payload[3:5], 200)
	binary.BigEndian.PutUint16(payload[5:7], 480)
	binary.BigEndian.PutUint16(payload[7:9], 270)
	_, _, err := ParseLayoutSlotPosition(payload)
	require.Error(t, err)
}

func TestParseLayoutSlotPosition_ZeroDimension(t *testing.T) {
	payload := make([]byte, 9)
	payload[0] = 0
	binary.BigEndian.PutUint16(payload[1:3], 100)
	binary.BigEndian.PutUint16(payload[3:5], 200)
	binary.BigEndian.PutUint16(payload[5:7], 0)
	binary.BigEndian.PutUint16(payload[7:9], 270)
	_, _, err := ParseLayoutSlotPosition(payload)
	require.Error(t, err)
}
