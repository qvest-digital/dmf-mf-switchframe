package fastctrl

import (
	"encoding/binary"
	"fmt"
	"math"
)

func ParseTransitionPosition(data []byte) (float64, error) {
	if len(data) < 4 {
		return 0, fmt.Errorf("transition position: need 4 bytes, got %d", len(data))
	}
	bits := binary.BigEndian.Uint32(data[:4])
	pos := float64(math.Float32frombits(bits))
	if math.IsNaN(pos) || pos < 0 || pos > 1 {
		return 0, fmt.Errorf("transition position %f out of range [0, 1]", pos)
	}
	return pos, nil
}
