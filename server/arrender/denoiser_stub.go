//go:build !cgo || !cuda || !optix

package arrender

import (
	"fmt"
	"unsafe"

	"github.com/zsiec/switchframe/server/gpu"
)

// Denoiser is a stub for builds without OptiX support.
type Denoiser struct{}

// NewDenoiser returns ErrRendererNotReady on non-OptiX builds.
func NewDenoiser(_ *gpu.OptixContext, _, _ int, _ bool) (*Denoiser, error) {
	return nil, fmt.Errorf("%w: OptiX denoiser not available", ErrRendererNotReady)
}

// Denoise returns ErrRendererNotReady on non-OptiX builds.
func (d *Denoiser) Denoise(_, _, _, _ unsafe.Pointer) error {
	return fmt.Errorf("%w: OptiX denoiser not available", ErrRendererNotReady)
}

// Width returns 0 on non-OptiX builds.
func (d *Denoiser) Width() int { return 0 }

// Height returns 0 on non-OptiX builds.
func (d *Denoiser) Height() int { return 0 }

// Temporal returns false on non-OptiX builds.
func (d *Denoiser) Temporal() bool { return false }

// Close is a no-op on non-OptiX builds.
func (d *Denoiser) Close() {}
