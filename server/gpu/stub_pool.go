//go:build (!cgo || !cuda) && !darwin

package gpu

// GPUFrame is a stub for non-GPU builds.
type GPUFrame struct {
	Width  int
	Height int
	Pitch  int
	PTS    int64
}

// Release is a no-op on non-GPU builds.
func (f *GPUFrame) Release() {}

// Ref is a no-op on non-GPU builds.
func (f *GPUFrame) Ref() {}

// FramePool is a stub for non-GPU builds.
type FramePool struct{}

// NewFramePool returns ErrGPUNotAvailable on non-GPU builds.
func NewFramePool(ctx *Context, width, height, initialSize int) (*FramePool, error) {
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
