package replay

import "github.com/zsiec/switchframe/server/switcher"

// InterpolationMode selects the frame interpolation algorithm.
type InterpolationMode string

const (
	InterpolationNone          InterpolationMode = "none"           // frame duplication (current behavior)
	InterpolationBlend         InterpolationMode = "blend"          // alpha blend adjacent frames
	InterpolationMCFI          InterpolationMode = "mcfi"           // motion-compensated frame interpolation
	InterpolationHoldCrossfade InterpolationMode = "hold-crossfade" // hold clean frames, crossfade at transitions
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
	// Return a copy so callers can hold the result across calls without
	// it being overwritten by the next Interpolate() invocation. The replay
	// player passes interpolated frames to RawVideoOutput which may consume
	// them asynchronously.
	out := make([]byte, size)
	copy(out, b.buf[:size])
	return out
}

// holdCrossfadeInterpolator implements FrameInterpolator using a hold-and-crossfade
// strategy. Holds the current frame (frameA) for most alpha values and only
// crossfades to frameB in the last output slot. This produces clean, artifact-free
// frames with a brief dissolve at each transition — no motion estimation, no blocking.
type holdCrossfadeInterpolator struct {
	buf []byte
}

func (p *holdCrossfadeInterpolator) Interpolate(frameA, frameB []byte, width, height int, alpha float64) []byte {
	size := width * height * 3 / 2

	// Hold frameA for alpha < 0.625 (first 2-3 dups); crossfade above that.
	// For dupCount=4: alpha 0.25→A, 0.50→A, 0.75→crossfade(A,B,0.6)
	// For dupCount=2: alpha 0.50→A (hold, since only 1 dup slot)
	// For dupCount=3: alpha 0.33→A, 0.67→crossfade(A,B,0.5)
	const crossfadeThreshold = 0.625

	if alpha < crossfadeThreshold {
		out := make([]byte, size)
		copy(out, frameA[:size])
		return out
	}

	// Remap alpha from [threshold, 1.0] → [0, 1.0] for the crossfade region.
	t := (alpha - crossfadeThreshold) / (1.0 - crossfadeThreshold)
	// Ease-in for smoother transition onset.
	t = t * t

	if len(p.buf) < size {
		p.buf = make([]byte, size)
	}
	invT := 1.0 - t
	for i := 0; i < size && i < len(frameA) && i < len(frameB); i++ {
		p.buf[i] = byte(float64(frameA[i])*invT + float64(frameB[i])*t + 0.5)
	}
	out := make([]byte, size)
	copy(out, p.buf[:size])
	return out
}

// newInterpolator creates a FrameInterpolator for the given mode.
// Returns nil for InterpolationNone, meaning frame duplication will be used.
func newInterpolator(mode InterpolationMode) FrameInterpolator {
	switch mode {
	case InterpolationBlend:
		return &blendInterpolator{}
	case InterpolationMCFI:
		return switcher.NewMCFIState()
	case InterpolationHoldCrossfade:
		return &holdCrossfadeInterpolator{}
	default:
		return nil // nil means frame duplication
	}
}
