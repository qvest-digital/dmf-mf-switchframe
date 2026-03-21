//go:build !cgo || !cuda

package gpu

// Context is a stub for non-CUDA builds.
type Context struct{}

// NewContext returns ErrGPUNotAvailable on non-CUDA builds.
func NewContext() (*Context, error) { return nil, ErrGPUNotAvailable }

// Close is a no-op on non-CUDA builds.
func (c *Context) Close() error { return nil }

// DeviceProperties returns a zero-value DeviceProperties.
func (c *Context) DeviceProperties() DeviceProperties { return DeviceProperties{} }

// Stream returns 0 (nil stream).
func (c *Context) Stream() uintptr { return 0 }

// EncStream returns 0 (nil stream).
func (c *Context) EncStream() uintptr { return 0 }

// MemoryStats returns zero stats on non-CUDA builds.
func (c *Context) MemoryStats() MemoryStats { return MemoryStats{} }

// Sync is a no-op on non-CUDA builds.
func (c *Context) Sync() error { return nil }

// SetPool is a no-op on non-CUDA builds.
func (c *Context) SetPool(pool *FramePool) {}

// Pool returns nil on non-CUDA builds.
func (c *Context) Pool() *FramePool { return nil }

// DeviceProperties holds GPU device information.
type DeviceProperties struct {
	Name               string
	ComputeCapability  [2]int
	TotalMemory        int64
	MultiprocessorCount int
	MaxThreadsPerBlock int
}

// MemoryStats holds GPU memory usage information.
type MemoryStats struct {
	TotalMB int
	FreeMB  int
	UsedMB  int
}

// GPUFrame is a stub for non-CUDA builds.
type GPUFrame struct {
	Width  int
	Height int
	Pitch  int
	PTS    int64
}

// Release is a no-op on non-CUDA builds.
func (f *GPUFrame) Release() {}

// Ref is a no-op on non-CUDA builds.
func (f *GPUFrame) Ref() {}

// FramePool is a stub for non-CUDA builds.
type FramePool struct{}

// NewFramePool returns ErrGPUNotAvailable on non-CUDA builds.
func NewFramePool(ctx *Context, width, height, initialSize int) (*FramePool, error) {
	return nil, ErrGPUNotAvailable
}

// Acquire returns ErrGPUNotAvailable on non-CUDA builds.
func (p *FramePool) Acquire() (*GPUFrame, error) { return nil, ErrGPUNotAvailable }

// Close is a no-op on non-CUDA builds.
func (p *FramePool) Close() {}

// Stats returns zero stats on non-CUDA builds.
func (p *FramePool) Stats() (hits, misses uint64) { return 0, 0 }

// Upload returns ErrGPUNotAvailable on non-CUDA builds.
func Upload(ctx *Context, frame *GPUFrame, yuv []byte, width, height int) error {
	return ErrGPUNotAvailable
}

// Download returns ErrGPUNotAvailable on non-CUDA builds.
func Download(ctx *Context, yuv []byte, frame *GPUFrame, width, height int) error {
	return ErrGPUNotAvailable
}

// FillBlack returns ErrGPUNotAvailable on non-CUDA builds.
func FillBlack(ctx *Context, frame *GPUFrame) error {
	return ErrGPUNotAvailable
}

// GPUEncoder is a stub for non-CUDA builds.
type GPUEncoder struct{}

// NewGPUEncoder returns ErrGPUNotAvailable on non-CUDA builds.
func NewGPUEncoder(ctx *Context, width, height, fpsNum, fpsDen, bitrate int) (*GPUEncoder, error) {
	return nil, ErrGPUNotAvailable
}

// EncodeGPU returns ErrGPUNotAvailable on non-CUDA builds.
func (e *GPUEncoder) EncodeGPU(frame *GPUFrame, forceIDR bool) ([]byte, bool, error) {
	return nil, false, ErrGPUNotAvailable
}

// EncodeCPU returns ErrGPUNotAvailable on non-CUDA builds.
func (e *GPUEncoder) EncodeCPU(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
	return nil, false, ErrGPUNotAvailable
}

// Close is a no-op on non-CUDA builds.
func (e *GPUEncoder) Close() {}

// GPUDecoder is a stub for non-CUDA builds.
type GPUDecoder struct{}

// NewGPUDecoder returns ErrGPUNotAvailable on non-CUDA builds.
func NewGPUDecoder(ctx *Context, threadCount int) (*GPUDecoder, error) {
	return nil, ErrGPUNotAvailable
}
