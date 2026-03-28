//go:build !cgo || (!cuda && !darwin)

package gpu

// GPUFrame is a stub for non-GPU builds.
type GPUFrame struct {
	Width  int
	Height int
	Pitch  int
	PTS    int64
	Format GPUFrameFormat
}

// Release is a no-op on non-GPU builds.
func (f *GPUFrame) Release() {}

// Ref is a no-op on non-GPU builds.
func (f *GPUFrame) Ref() {}

// YPlaneOffset returns the byte offset of the Y plane (always 0).
func (f *GPUFrame) YPlaneOffset() int { return 0 }

// UVPlaneOffset returns the byte offset of the UV plane.
func (f *GPUFrame) UVPlaneOffset() int { return f.Pitch * f.Height }

// NV12Size returns the total byte size assuming NV12 layout.
// Deprecated: Use FrameSize() which is format-aware.
func (f *GPUFrame) NV12Size() int { return f.Pitch * f.Height * 3 / 2 }

// FrameSize returns the total byte size of the frame based on its format.
func (f *GPUFrame) FrameSize() int {
	switch f.Format {
	case GPUFormatP210:
		return f.Pitch * f.Height * 2
	default:
		return f.Pitch * f.Height * 3 / 2
	}
}

// UVPlaneHeight returns the height of the UV/chroma plane.
func (f *GPUFrame) UVPlaneHeight() int {
	switch f.Format {
	case GPUFormatP210:
		return f.Height
	default:
		return f.Height / 2
	}
}

// FramePool is a stub for non-GPU builds.
type FramePool struct{}

// NewFramePool returns ErrGPUNotAvailable on non-GPU builds.
func NewFramePool(ctx *Context, width, height, initialSize int) (*FramePool, error) {
	return nil, ErrGPUNotAvailable
}

// NewP210FramePool returns ErrGPUNotAvailable on non-GPU builds.
func NewP210FramePool(ctx *Context, width, height, initialSize int) (*FramePool, error) {
	return nil, ErrGPUNotAvailable
}

// Acquire returns ErrGPUNotAvailable on non-GPU builds.
func (p *FramePool) Acquire() (*GPUFrame, error) { return nil, ErrGPUNotAvailable }

// Close is a no-op on non-GPU builds.
func (p *FramePool) Close() {}

// Stats returns zero stats on non-GPU builds.
func (p *FramePool) Stats() (hits, misses uint64) { return 0, 0 }

// Pitch returns 0 on non-GPU builds.
func (p *FramePool) Pitch() int { return 0 }

// AllDevPtrs returns nil on non-GPU builds.
func (p *FramePool) AllDevPtrs() []uintptr { return nil }

// AllFrames returns nil on non-GPU builds.
func (p *FramePool) AllFrames() []*GPUFrame { return nil }

// NewGPUFrameFromDevPtr creates a GPUFrame wrapping an external device pointer.
// On non-GPU builds this returns a frame with the given dimensions but no
// usable device pointer.
// Format returns the pixel format of frames in this pool.
func (p *FramePool) Format() GPUFrameFormat { return GPUFormatNV12 }

func NewGPUFrameFromDevPtr(_ uintptr, width, height, pitch int, pts int64, format GPUFrameFormat) *GPUFrame {
	return &GPUFrame{
		Width:  width,
		Height: height,
		Pitch:  pitch,
		PTS:    pts,
		Format: format,
	}
}
