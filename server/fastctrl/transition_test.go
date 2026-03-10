package fastctrl

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseTransitionPosition(t *testing.T) {
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, math.Float32bits(0.75))
	pos, err := ParseTransitionPosition(payload)
	require.NoError(t, err)
	require.InDelta(t, 0.75, pos, 1e-6)
}

func TestParseTransitionPosition_TooShort(t *testing.T) {
	_, err := ParseTransitionPosition([]byte{0x00, 0x01})
	require.Error(t, err)
}

func TestParseTransitionPosition_OutOfRange(t *testing.T) {
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, math.Float32bits(1.5))
	_, err := ParseTransitionPosition(payload)
	require.Error(t, err)
}

func TestParseTransitionPosition_Negative(t *testing.T) {
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, math.Float32bits(-0.1))
	_, err := ParseTransitionPosition(payload)
	require.Error(t, err)
}

func TestParseTransitionPosition_Boundaries(t *testing.T) {
	for _, val := range []float32{0.0, 1.0} {
		payload := make([]byte, 4)
		binary.BigEndian.PutUint32(payload, math.Float32bits(val))
		pos, err := ParseTransitionPosition(payload)
		require.NoError(t, err)
		require.InDelta(t, float64(val), pos, 1e-6)
	}
}
