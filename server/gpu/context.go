//go:build cgo && cuda

package gpu

/*
#include <cuda.h>
#include <cuda_runtime.h>
*/
import "C"

import (
	"fmt"
	"sync"
)

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

// Context manages the shared CUDA context for all GPU operations.
// One context is shared across all decoders, encoders, and kernel launches.
type Context struct {
	device    C.CUdevice
	ctx       C.CUcontext
	stream    C.cudaStream_t // default processing stream
	encStream C.cudaStream_t // encode stream (concurrent with processing)
	pool      *FramePool
	props     DeviceProperties
	mu        sync.Mutex
}

// NewContext initializes CUDA and creates a shared context on device 0.
func NewContext() (*Context, error) {
	// Initialize CUDA driver API
	if rc := C.cuInit(0); rc != C.CUDA_SUCCESS {
		return nil, fmt.Errorf("gpu: cuInit failed: %d", rc)
	}

	var deviceCount C.int
	if rc := C.cudaGetDeviceCount(&deviceCount); rc != C.cudaSuccess {
		return nil, fmt.Errorf("gpu: cudaGetDeviceCount failed: %d", rc)
	}
	if deviceCount == 0 {
		return nil, ErrGPUNotAvailable
	}

	// Select device 0 (single GPU on g2-standard-8)
	if rc := C.cudaSetDevice(0); rc != C.cudaSuccess {
		return nil, fmt.Errorf("gpu: cudaSetDevice failed: %d", rc)
	}

	c := &Context{}

	// Get device handle via driver API
	if rc := C.cuDeviceGet(&c.device, 0); rc != C.CUDA_SUCCESS {
		return nil, fmt.Errorf("gpu: cuDeviceGet failed: %d", rc)
	}

	// Create CUDA context via driver API
	if rc := C.cuCtxCreate(&c.ctx, C.CU_CTX_SCHED_BLOCKING_SYNC, c.device); rc != C.CUDA_SUCCESS {
		return nil, fmt.Errorf("gpu: cuCtxCreate failed: %d", rc)
	}

	// Create CUDA streams for concurrent operations
	if rc := C.cudaStreamCreateWithFlags(&c.stream, C.cudaStreamNonBlocking); rc != C.cudaSuccess {
		c.Close()
		return nil, fmt.Errorf("gpu: stream create failed: %d", rc)
	}
	if rc := C.cudaStreamCreateWithFlags(&c.encStream, C.cudaStreamNonBlocking); rc != C.cudaSuccess {
		c.Close()
		return nil, fmt.Errorf("gpu: enc stream create failed: %d", rc)
	}

	// Query device properties
	var props C.struct_cudaDeviceProp
	if rc := C.cudaGetDeviceProperties(&props, 0); rc != C.cudaSuccess {
		c.Close()
		return nil, fmt.Errorf("gpu: cudaGetDeviceProperties failed: %d", rc)
	}
	c.props = DeviceProperties{
		Name:                C.GoString(&props.name[0]),
		ComputeCapability:   [2]int{int(props.major), int(props.minor)},
		TotalMemory:         int64(props.totalGlobalMem),
		MultiprocessorCount: int(props.multiProcessorCount),
		MaxThreadsPerBlock:  int(props.maxThreadsPerBlock),
	}

	return c, nil
}

// Close releases all CUDA resources.
func (c *Context) Close() error {
	if c == nil {
		return nil
	}
	if c.pool != nil {
		c.pool.Close()
	}
	if c.stream != nil {
		C.cudaStreamDestroy(c.stream)
		c.stream = nil
	}
	if c.encStream != nil {
		C.cudaStreamDestroy(c.encStream)
		c.encStream = nil
	}
	if c.ctx != nil {
		C.cuCtxDestroy(c.ctx)
		c.ctx = nil
	}
	return nil
}

// DeviceProperties returns the GPU device properties.
func (c *Context) DeviceProperties() DeviceProperties {
	return c.props
}

// Stream returns the default processing CUDA stream.
func (c *Context) Stream() C.cudaStream_t {
	return c.stream
}

// EncStream returns the encode CUDA stream (for concurrent encode).
func (c *Context) EncStream() C.cudaStream_t {
	return c.encStream
}

// SetPool associates a FramePool with this context.
func (c *Context) SetPool(pool *FramePool) {
	c.pool = pool
}

// Pool returns the associated FramePool, if any.
func (c *Context) Pool() *FramePool {
	return c.pool
}

// Sync synchronizes the default processing stream, blocking until
// all previously queued operations complete.
func (c *Context) Sync() error {
	if rc := C.cudaStreamSynchronize(c.stream); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: stream sync failed: %d", rc)
	}
	return nil
}

// MemoryStats returns current GPU memory usage.
func (c *Context) MemoryStats() MemoryStats {
	var free, total C.size_t
	C.cudaMemGetInfo(&free, &total)
	return MemoryStats{
		TotalMB: int(total) / (1024 * 1024),
		FreeMB:  int(free) / (1024 * 1024),
		UsedMB:  int(total-free) / (1024 * 1024),
	}
}
