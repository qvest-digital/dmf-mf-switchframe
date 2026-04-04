package playout

import (
	"strings"

	"github.com/zsiec/switchframe/server/transition"
	"github.com/zsiec/switchframe/server/transition/wipemap"
)

// GPUBlendFunc is the signature for GPU-accelerated blending.
// Will be wired later — nil means CPU-only.
type GPUBlendFunc func(a, b []byte, dst []byte, w, h int, progress float32, blendType string)

// Compositor blends two YUV420 frames during playout item transitions.
// It delegates to the existing transition.FrameBlender for CPU SIMD blending
// and accepts an optional GPUBlendFunc for GPU-accelerated blending.
//
// The Compositor is stateless with respect to transition timing — it only
// produces a single blended frame given two inputs and a progress value.
// The Scheduler owns transition progress and timing.
type Compositor struct {
	blender      *transition.FrameBlender
	gpuBlend     GPUBlendFunc
	wipeMapStore *wipemap.WipeMapStore
	width        int
	height       int
}

// NewCompositor creates a Compositor for the given frame dimensions.
// If gpuBlend is nil, all blending uses the CPU SIMD path.
func NewCompositor(w, h int, gpuBlend GPUBlendFunc) *Compositor {
	blender, err := transition.NewFrameBlender(w, h)
	if err != nil {
		// Dimensions must be even for YUV420. If not, create with smallest
		// valid dimensions and let Resize fix it later.
		blender, _ = transition.NewFrameBlender(2, 2)
		w, h = 2, 2
	}
	return &Compositor{
		blender:  blender,
		gpuBlend: gpuBlend,
		width:    w,
		height:   h,
	}
}

// Blend produces one output frame from two inputs at the given progress (0.0-1.0).
// Uses GPU blend if available, falls back to CPU SIMD.
//
// The output frame's PTS is taken from frame b (the incoming frame).
// The result is always marked as a keyframe since the blend output is
// a complete frame not dependent on prior decode state.
//
// The returned frame owns its own YUV buffer — it is safe to hold across
// successive Blend calls (the FrameBlender's internal buffer is copied).
func (c *Compositor) Blend(a, b *DecodedFrame, progress float64, config TransitionConfig) *DecodedFrame {
	// Clamp progress to [0.0, 1.0].
	if progress < 0 {
		progress = 0
	}
	if progress > 1.0 {
		progress = 1.0
	}

	if c.gpuBlend != nil {
		dst := make([]byte, len(a.YUV))
		c.gpuBlend(a.YUV, b.YUV, dst, c.width, c.height, float32(progress), config.Type)
		return &DecodedFrame{
			YUV:        dst,
			Width:      c.width,
			Height:     c.height,
			PTS:        b.PTS,
			IsKeyframe: true,
		}
	}

	// CPU path: delegate to FrameBlender.
	var blended []byte

	switch config.Type {
	case "dissolve", "mix":
		blended = c.blender.BlendMix(a.YUV, b.YUV, progress)

	case "dip_black", "dip":
		blended = c.blender.BlendDip(a.YUV, b.YUV, progress)

	case "dip_white":
		// FrameBlender dips through blackY. For dip-to-white we'd need Y=235,
		// which isn't supported yet. Use standard dip (through black) as fallback.
		blended = c.blender.BlendDip(a.YUV, b.YUV, progress)

	case "wipe":
		if config.WipeConfig != nil && c.wipeMapStore != nil {
			gm, err := c.wipeMapStore.GetMap(config.WipeConfig.PatternID, c.width, c.height)
			if err == nil {
				blended = c.blender.BlendWipeMap(a.YUV, b.YUV, progress, gm, *config.WipeConfig)
			} else {
				// Fall back to directional wipe on gradient map resolve failure.
				blended = c.blender.BlendWipe(a.YUV, b.YUV, progress, transition.WipeHLeft)
			}
		} else {
			blended = c.blender.BlendWipe(a.YUV, b.YUV, progress, transition.WipeHLeft)
		}

	default:
		if strings.HasPrefix(config.Type, "dve-") {
			// DVE spatial transitions require the GPU pipeline. In the playout
			// CPU compositor, fall back to a mix (dissolve) for visual continuity.
			blended = c.blender.BlendMix(a.YUV, b.YUV, progress)
		} else {
			// Unknown transition type — hard cut to B.
			return c.Cut(b)
		}
	}

	// Copy the blended output. The FrameBlender reuses its internal yuvBufOut
	// buffer, so we must copy to avoid corruption on the next Blend call.
	result := make([]byte, len(blended))
	copy(result, blended)

	return &DecodedFrame{
		YUV:        result,
		Width:      c.width,
		Height:     c.height,
		PTS:        b.PTS,
		IsKeyframe: true,
	}
}

// Cut returns frame b directly (hard cut, no blend).
// No copy is made — the caller receives the same YUV slice.
func (c *Compositor) Cut(b *DecodedFrame) *DecodedFrame {
	return b
}

// SetWipeMapStore sets the wipe map store for gradient-map wipe transitions.
func (c *Compositor) SetWipeMapStore(store *wipemap.WipeMapStore) {
	c.wipeMapStore = store
}

// Resize updates the compositor for a new frame size.
// A new FrameBlender is allocated for the new dimensions.
func (c *Compositor) Resize(w, h int) {
	blender, err := transition.NewFrameBlender(w, h)
	if err != nil {
		return // refuse to resize to invalid dimensions
	}
	c.blender = blender
	c.width = w
	c.height = h
}
