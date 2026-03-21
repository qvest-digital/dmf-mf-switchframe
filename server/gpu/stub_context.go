//go:build (!cgo || !cuda) && !darwin

package gpu

import "unsafe"

// Context is a stub for non-GPU builds.
type Context struct{}

// NewContext returns ErrGPUNotAvailable on non-GPU builds.
func NewContext() (*Context, error) { return nil, ErrGPUNotAvailable }

// Close is a no-op on non-GPU builds.
func (c *Context) Close() error { return nil }

// Backend returns "" on non-GPU builds.
func (c *Context) Backend() string { return "" }

// DeviceName returns "" on non-GPU builds.
func (c *Context) DeviceName() string { return "" }

// DeviceProperties returns a zero-value DeviceProperties.
func (c *Context) DeviceProperties() DeviceProperties { return DeviceProperties{} }

// Stream returns 0 (nil stream).
func (c *Context) Stream() uintptr { return 0 }

// EncStream returns 0 (nil stream).
func (c *Context) EncStream() uintptr { return 0 }

// MemoryStats returns zero stats on non-GPU builds.
func (c *Context) MemoryStats() MemoryStats { return MemoryStats{} }

// Sync is a no-op on non-GPU builds.
func (c *Context) Sync() error { return nil }

// SetPool is a no-op on non-GPU builds.
func (c *Context) SetPool(pool *FramePool) {}

// Pool returns nil on non-GPU builds.
func (c *Context) Pool() *FramePool { return nil }

// CUDAContext returns nil on non-GPU builds.
func (c *Context) CUDAContext() unsafe.Pointer { return nil }

// DeviceProperties holds GPU device information.
type DeviceProperties struct {
	Name                string
	ComputeCapability   [2]int
	TotalMemory         int64
	MultiprocessorCount int
	MaxThreadsPerBlock  int
}

// MemoryStats holds GPU memory usage information.
type MemoryStats struct {
	TotalMB int
	FreeMB  int
	UsedMB  int
}
