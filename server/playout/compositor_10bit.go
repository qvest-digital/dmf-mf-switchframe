package playout

import (
	"github.com/zsiec/switchframe/server/transition"
)

// BlendPlayout10 blends two YUV422P10LE decoded frames using linear interpolation.
// pos256 ranges from 0 (100% A) to 256 (100% B).
// Delegates to transition.BlendMix10 for the actual 10-bit blending math.
func BlendPlayout10(dst, a, b []byte, width, height, pos256 int) error {
	return transition.BlendMix10(dst, a, b, width, height, pos256)
}
