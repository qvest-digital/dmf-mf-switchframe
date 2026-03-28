package switcher

import (
	"github.com/zsiec/switchframe/server/transition"
)

// Interpolate10 generates an interpolated frame between two YUV422P10LE frames.
// t is the interpolation factor (0.0 = frameA, 1.0 = frameB).
// Uses linear blending as the Go fallback; MCFI warp can be layered on via SIMD later.
//
// The returned slice is a freshly allocated buffer, safe for async consumers.
func (s *MCFIState) Interpolate10(frameA, frameB []byte, width, height int, t float64) []byte {
	frameSize := ColorYUV422_10bit.FrameSize(width, height)
	result := make([]byte, frameSize)

	// Clamp t to [0, 1]
	if t <= 0 {
		copy(result, frameA[:frameSize])
		return result
	}
	if t >= 1 {
		copy(result, frameB[:frameSize])
		return result
	}

	// Convert t to 0-256 range for BlendMix10
	pos256 := int(t*256 + 0.5)
	if pos256 < 0 {
		pos256 = 0
	}
	if pos256 > 256 {
		pos256 = 256
	}

	// BlendMix10 handles all three planes (Y, Cb, Cr) with proper 10-bit arithmetic.
	_ = transition.BlendMix10(result, frameA, frameB, width, height, pos256)
	return result
}
