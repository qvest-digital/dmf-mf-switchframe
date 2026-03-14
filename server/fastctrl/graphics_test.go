package fastctrl

import (
	"encoding/binary"
	"image"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseGraphicsLayerPosition_Valid(t *testing.T) {
	data := make([]byte, 10)
	binary.BigEndian.PutUint16(data[0:2], 3)    // layer ID
	binary.BigEndian.PutUint16(data[2:4], 100)  // X
	binary.BigEndian.PutUint16(data[4:6], 200)  // Y
	binary.BigEndian.PutUint16(data[6:8], 400)  // W
	binary.BigEndian.PutUint16(data[8:10], 300) // H

	layerID, rect, err := ParseGraphicsLayerPosition(data)
	require.NoError(t, err)
	require.Equal(t, 3, layerID)
	require.Equal(t, image.Rect(100, 200, 500, 500), rect)
}

func TestParseGraphicsLayerPosition_TooShort(t *testing.T) {
	_, _, err := ParseGraphicsLayerPosition(make([]byte, 9))
	require.Error(t, err)
	require.Contains(t, err.Error(), "need 10 bytes")
}

func TestParseGraphicsLayerPosition_OddAlignment(t *testing.T) {
	data := make([]byte, 10)
	binary.BigEndian.PutUint16(data[0:2], 0)
	binary.BigEndian.PutUint16(data[2:4], 101) // odd X
	binary.BigEndian.PutUint16(data[4:6], 200)
	binary.BigEndian.PutUint16(data[6:8], 400)
	binary.BigEndian.PutUint16(data[8:10], 300)

	_, _, err := ParseGraphicsLayerPosition(data)
	require.Error(t, err)
	require.Contains(t, err.Error(), "even-aligned")
}

func TestParseGraphicsLayerPosition_ZeroDimension(t *testing.T) {
	data := make([]byte, 10)
	binary.BigEndian.PutUint16(data[0:2], 0)
	binary.BigEndian.PutUint16(data[2:4], 100)
	binary.BigEndian.PutUint16(data[4:6], 200)
	binary.BigEndian.PutUint16(data[6:8], 0) // zero width
	binary.BigEndian.PutUint16(data[8:10], 300)

	_, _, err := ParseGraphicsLayerPosition(data)
	require.Error(t, err)
	require.Contains(t, err.Error(), "zero dimension")
}
