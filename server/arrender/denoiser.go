//go:build cgo && cuda && optix

package arrender

import (
	"fmt"
	"unsafe"

	"github.com/zsiec/switchframe/server/gpu"
)

// Denoiser wraps the OptiX AI denoiser for post-render noise reduction.
// It manages the denoiser lifecycle and provides a simple Denoise() call
// that takes raw ray-traced output and produces a clean result.
type Denoiser struct {
	denoiser *gpu.OptixDenoiser
	width    int
	height   int
	temporal bool
}

// NewDenoiser creates an OptiX AI denoiser for the given resolution.
// If temporal is true, the denoiser uses motion vectors for temporal
// stability across frames (reduces flickering in animations).
func NewDenoiser(ctx *gpu.OptixContext, width, height int, temporal bool) (*Denoiser, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: nil OptiX context", ErrRendererNotReady)
	}
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("%w: invalid denoiser dimensions %dx%d", ErrRendererNotReady, width, height)
	}

	d, err := gpu.NewOptixDenoiser(ctx, width, height, temporal)
	if err != nil {
		return nil, fmt.Errorf("create OptiX denoiser: %w", err)
	}

	return &Denoiser{
		denoiser: d,
		width:    width,
		height:   height,
		temporal: temporal,
	}, nil
}

// Denoise runs the AI denoiser on a noisy ray-traced image.
// noisyInput and cleanOutput are float4 RGBA device pointers.
// flowBuffer is a float2 motion vector device pointer (nil for
// non-temporal mode). stream is the CUDA stream for async execution.
func (d *Denoiser) Denoise(noisyInput, cleanOutput, flowBuffer, stream unsafe.Pointer) error {
	if d == nil || d.denoiser == nil {
		return fmt.Errorf("%w: denoiser not initialized", ErrRendererNotReady)
	}

	return d.denoiser.Denoise(noisyInput, cleanOutput, flowBuffer,
		d.width, d.height, stream)
}

// Width returns the denoiser's configured width.
func (d *Denoiser) Width() int {
	if d == nil {
		return 0
	}
	return d.width
}

// Height returns the denoiser's configured height.
func (d *Denoiser) Height() int {
	if d == nil {
		return 0
	}
	return d.height
}

// Temporal returns whether the denoiser uses temporal mode.
func (d *Denoiser) Temporal() bool {
	if d == nil {
		return false
	}
	return d.temporal
}

// Close releases the denoiser and its GPU scratch buffers.
func (d *Denoiser) Close() {
	if d == nil || d.denoiser == nil {
		return
	}
	d.denoiser.Close()
	d.denoiser = nil
}
