package replay

import "github.com/zsiec/switchframe/server/switcher"

// InterpolationMode selects the frame interpolation algorithm.
type InterpolationMode string

const (
	InterpolationNone  InterpolationMode = "none"  // frame duplication (current behavior)
	InterpolationBlend InterpolationMode = "blend" // alpha blend adjacent frames
	InterpolationMCFI  InterpolationMode = "mcfi"  // motion-compensated frame interpolation
)

// FrameInterpolator generates an interpolated frame between two YUV420 frames.
type FrameInterpolator interface {
	Interpolate(frameA, frameB []byte, width, height int, alpha float64) []byte
}

// blendInterpolator implements FrameInterpolator using linear YUV420 blending.
type blendInterpolator struct {
	buf []byte
}

func (b *blendInterpolator) Interpolate(frameA, frameB []byte, width, height int, alpha float64) []byte {
	size := width * height * 3 / 2
	if len(b.buf) < size {
		b.buf = make([]byte, size)
	}
	invAlpha := 1.0 - alpha
	for i := 0; i < size && i < len(frameA) && i < len(frameB); i++ {
		b.buf[i] = byte(float64(frameA[i])*invAlpha + float64(frameB[i])*alpha + 0.5)
	}
	return b.buf[:size]
}

// newInterpolator creates a FrameInterpolator for the given mode.
// Returns nil for InterpolationNone, meaning frame duplication will be used.
func newInterpolator(mode InterpolationMode) FrameInterpolator {
	switch mode {
	case InterpolationBlend:
		return &blendInterpolator{}
	case InterpolationMCFI:
		return switcher.NewMCFIState()
	default:
		return nil // nil means frame duplication
	}
}
