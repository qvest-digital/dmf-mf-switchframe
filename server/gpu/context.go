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
	"sync/atomic"
	"unsafe"
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

// defaultCUDAStream is the main pipeline CUDA stream, set when NewContext()
// creates the context. Used by CopyGPUFrame and other functions that need
// stream-aware operation but don't receive a *Context parameter. All kernel
// launches and memory copies MUST use an explicit stream (never the null
// stream), because ctx.stream is created with cudaStreamNonBlocking — the
// null stream does NOT synchronize with non-blocking streams, so operations
// on the null stream can race with kernel launches on ctx.stream.
var defaultCUDAStream C.cudaStream_t

// Context manages the shared CUDA context for all GPU operations.
// Uses the CUDA Runtime API's primary context (via cudaSetDevice) which is
// automatically shared across all OS threads — critical for cgo where
// goroutines may migrate between threads.
type Context struct {
	device    int
	stream    C.cudaStream_t // default processing stream
	encStream C.cudaStream_t // encode stream (concurrent with processing)
	pool      *FramePool
	props     DeviceProperties
	mu        sync.Mutex
	closed    atomic.Bool

	// Persistent staging buffers for Upload/Download — lazily allocated
	// on first use, freed in Close(). Avoids 180 cudaMalloc/cudaFree per
	// second at 30fps (6 allocs per upload + 6 per download × 30fps).
	stagingY      unsafe.Pointer
	stagingCb     unsafe.Pointer
	stagingCr     unsafe.Pointer
	stagingSize   int // Y plane size (width * height)
	stagingCbSize int // chroma plane size ((width/2) * (height/2))

	// Persistent staging buffer for UploadV210/DownloadV210.
	// Lazily allocated under ctx.mu, freed in Close().
	stagingV210     unsafe.Pointer
	stagingV210Size int

	// Persistent temporary float32 device buffer for Lanczos-3 scaler.
	// Lazily allocated under ctx.mu, freed in Close().
	// Size: lanczosTmpSize floats (4 bytes each).
	lanczosTmp     *C.float
	lanczosTmpSize int
}

// NewContext initializes CUDA and creates a shared context on device 0.
// Uses the Runtime API primary context which is thread-safe across cgo calls.
func NewContext() (*Context, error) {
	var deviceCount C.int
	if rc := C.cudaGetDeviceCount(&deviceCount); rc != C.cudaSuccess {
		return nil, fmt.Errorf("gpu: cudaGetDeviceCount failed: %d", rc)
	}
	if deviceCount == 0 {
		return nil, ErrGPUNotAvailable
	}

	// Select device 0 — this activates the primary context for all threads
	if rc := C.cudaSetDevice(0); rc != C.cudaSuccess {
		return nil, fmt.Errorf("gpu: cudaSetDevice failed: %d", rc)
	}

	c := &Context{device: 0}

	// Create CUDA streams for concurrent operations.
	// cudaStreamNonBlocking means these streams do NOT synchronize with the
	// null/default stream. All memory copies MUST use explicit streams to
	// prevent data races — cudaMemcpy (null stream) can run concurrently
	// with kernels on non-blocking streams.
	if rc := C.cudaStreamCreateWithFlags(&c.stream, C.cudaStreamNonBlocking); rc != C.cudaSuccess {
		c.Close()
		return nil, fmt.Errorf("gpu: stream create failed: %d", rc)
	}
	if rc := C.cudaStreamCreateWithFlags(&c.encStream, C.cudaStreamNonBlocking); rc != C.cudaSuccess {
		c.Close()
		return nil, fmt.Errorf("gpu: enc stream create failed: %d", rc)
	}

	// Store as package-level for CopyGPUFrame and other functions that
	// don't receive a *Context parameter.
	defaultCUDAStream = c.stream

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
	if c == nil || c.closed.Swap(true) {
		return nil
	}
	if c.pool != nil {
		c.pool.Close()
	}
	if c.stream != nil {
		C.cudaStreamDestroy(c.stream)
		c.stream = nil
		defaultCUDAStream = nil
	}
	if c.encStream != nil {
		C.cudaStreamDestroy(c.encStream)
		c.encStream = nil
	}
	// Free persistent staging buffers if allocated.
	if c.stagingY != nil {
		C.cudaFree(c.stagingY)
		c.stagingY = nil
	}
	if c.stagingCb != nil {
		C.cudaFree(c.stagingCb)
		c.stagingCb = nil
	}
	if c.stagingCr != nil {
		C.cudaFree(c.stagingCr)
		c.stagingCr = nil
	}
	c.stagingSize = 0
	c.stagingCbSize = 0
	if c.stagingV210 != nil {
		C.cudaFree(c.stagingV210)
		c.stagingV210 = nil
	}
	c.stagingV210Size = 0
	if c.lanczosTmp != nil {
		C.cudaFree(unsafe.Pointer(c.lanczosTmp))
		c.lanczosTmp = nil
	}
	c.lanczosTmpSize = 0
	// Note: we don't call cudaDeviceReset() — the primary context
	// persists for the process lifetime. This is intentional: other
	// tests or subsystems may still use the GPU.
	return nil
}

// Backend returns the GPU backend name ("cuda").
func (c *Context) Backend() string {
	if c == nil {
		return ""
	}
	return "cuda"
}

// DeviceName returns the GPU device name (e.g., "NVIDIA Tesla T4").
func (c *Context) DeviceName() string {
	if c == nil {
		return ""
	}
	return c.props.Name
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
	if c.closed.Load() {
		return nil
	}
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

// CUDAContext returns the current CUcontext pointer for use with FFmpeg's
// CUDA hw_device_ctx. The CUDA Runtime API shares the primary context across
// all threads, so cuCtxGetCurrent() returns the correct context after
// cudaSetDevice() has been called.
// Returns nil if the context is closed or CUDA context retrieval fails.
func (c *Context) CUDAContext() unsafe.Pointer {
	if c.closed.Load() {
		return nil
	}
	var cuCtx C.CUcontext
	// cuCtxGetCurrent is a Driver API call that works alongside Runtime API.
	if rc := C.cuCtxGetCurrent(&cuCtx); rc != C.CUDA_SUCCESS {
		return nil
	}
	return unsafe.Pointer(cuCtx)
}

// NewStream creates a new non-blocking CUDA stream for independent GPU
// workflow isolation (e.g., per-preview-encoder). Operations submitted to
// different streams can execute concurrently on the GPU. The caller must
// call DestroyStream when done.
func (c *Context) NewStream() (C.cudaStream_t, error) {
	if c.closed.Load() {
		return nil, fmt.Errorf("gpu: context closed")
	}
	var stream C.cudaStream_t
	if rc := C.cudaStreamCreateWithFlags(&stream, C.cudaStreamNonBlocking); rc != C.cudaSuccess {
		return nil, fmt.Errorf("gpu: create stream failed: %d", rc)
	}
	return stream, nil
}

// DestroyStream destroys a CUDA stream created by NewStream.
func (c *Context) DestroyStream(stream C.cudaStream_t) {
	if stream != nil {
		C.cudaStreamDestroy(stream)
	}
}

// SyncStream synchronizes a specific CUDA stream, blocking until all
// previously queued operations on that stream complete.
func (c *Context) SyncStream(stream C.cudaStream_t) error {
	if c.closed.Load() {
		return nil
	}
	if rc := C.cudaStreamSynchronize(stream); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: stream sync failed: %d", rc)
	}
	return nil
}

// ensureLanczosTemp ensures the Lanczos-3 temporary float32 device buffer is
// at least `needed` floats in size. Lazily allocates or grows as required.
// Must be called under the same serialization as scale_lanczos3_nv12 (i.e.
// the caller holds no concurrent access to lanczosTmp).
func (c *Context) ensureLanczosTemp(needed int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if needed <= c.lanczosTmpSize {
		return nil
	}
	if c.lanczosTmp != nil {
		C.cudaFree(unsafe.Pointer(c.lanczosTmp))
		c.lanczosTmp = nil
		c.lanczosTmpSize = 0
	}
	var ptr unsafe.Pointer
	bytes := C.size_t(needed * 4) // float32 = 4 bytes
	if rc := C.cudaMalloc(&ptr, bytes); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: lanczos3 tmpBuf alloc (%d floats) failed: %d", needed, rc)
	}
	c.lanczosTmp = (*C.float)(ptr)
	c.lanczosTmpSize = needed
	return nil
}
