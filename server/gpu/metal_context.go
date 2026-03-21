//go:build darwin

package gpu

/*
#include "metal_bridge.h"
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"unsafe"
)

const BackendMetal = "metal"

// metalContext manages the shared Metal context for all GPU operations.
// Apple Silicon has unified memory — CPU and GPU see the same physical RAM,
// so there is zero PCIe transfer overhead. MTLBuffer with StorageModeShared
// gives both CPU and GPU direct access.
type metalContext struct {
	device  C.MetalDeviceRef
	queue   C.MetalQueueRef
	library C.MetalLibraryRef
	pool *FramePool
	// mu is no longer used for Upload/Download (staging buffers have their own lock).
	// Retained for future use by operations that require exclusive GPU access.
	mu     sync.Mutex
	closed atomic.Bool

	// Lazily-created compute pipeline states keyed by kernel function name
	pipelines   map[string]C.MetalPipelineRef
	pipelinesMu sync.Mutex

	// Lanczos-3 temporary float buffer (cached, grown as needed).
	lanczosTmpBuf  C.MetalBufferRef
	lanczosTmpSize int

	// Luma key LUT buffer (cached, 256 bytes).
	lumaKeyLUTBuf C.MetalBufferRef

	// Temp file path for embedded metallib (cleaned up on Close).
	tempMetallibPath string

	// Staging buffer cache for Upload/Download (keyed by total NV12 size).
	// Avoids per-call allocation overhead.
	stagingBufs   map[int]*stagingBufferSet
	stagingBufsMu sync.Mutex
}

// stagingBufferSet holds the three planar staging buffers used for
// YUV420p <-> NV12 conversion during Upload/Download.
type stagingBufferSet struct {
	yBuf  C.MetalBufferRef
	cbBuf C.MetalBufferRef
	crBuf C.MetalBufferRef
	ySize int
}

// findMetallib searches for the compiled Metal shader library.
// Checks: (1) next to binary, (2) gpu/metal/ relative to working dir,
// (3) METAL_LIBRARY_PATH env var.
func findMetallib() (string, error) {
	name := "switchframe_gpu.metallib"

	// Check environment variable first
	if envPath := os.Getenv("METAL_LIBRARY_PATH"); envPath != "" {
		if _, err := os.Stat(envPath); err == nil {
			return envPath, nil
		}
	}

	// Check next to the binary
	exe, err := os.Executable()
	if err == nil {
		p := filepath.Join(filepath.Dir(exe), name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	// Check relative paths from working dir
	candidates := []string{
		filepath.Join("gpu", "metal", name),
		filepath.Join("metal", name),
		name,
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			abs, _ := filepath.Abs(c)
			return abs, nil
		}
	}

	return "", fmt.Errorf("gpu: metal: %s not found (set METAL_LIBRARY_PATH or build with: cd gpu/metal && make)", name)
}

// newMetalContext initializes Metal and creates a shared context.
// Tries to find the metallib on disk first; falls back to the embedded
// metallib (written to a temp file) if not found.
func newMetalContext() (*metalContext, error) {
	var tempPath string
	libPath, err := findMetallib()
	if err != nil {
		// Fallback: write embedded metallib to temp file.
		if len(embeddedMetallib) == 0 {
			return nil, fmt.Errorf("gpu: metal: no metallib found and no embedded metallib available")
		}
		tmpFile, tmpErr := os.CreateTemp("", "switchframe_gpu_*.metallib")
		if tmpErr != nil {
			return nil, fmt.Errorf("gpu: metal: create temp metallib: %w", tmpErr)
		}
		if _, wErr := tmpFile.Write(embeddedMetallib); wErr != nil {
			tmpFile.Close()
			os.Remove(tmpFile.Name())
			return nil, fmt.Errorf("gpu: metal: write temp metallib: %w", wErr)
		}
		tmpFile.Close()
		libPath = tmpFile.Name()
		tempPath = libPath
	}

	cPath := C.CString(libPath)
	defer C.free(unsafe.Pointer(cPath))

	c := &metalContext{
		pipelines:        make(map[string]C.MetalPipelineRef),
		stagingBufs:      make(map[int]*stagingBufferSet),
		tempMetallibPath: tempPath,
	}

	rc := C.metal_init(&c.device, &c.queue, &c.library, cPath)
	if rc != C.METAL_SUCCESS {
		if tempPath != "" {
			os.Remove(tempPath)
		}
		return nil, fmt.Errorf("gpu: metal_init failed: %d", rc)
	}

	return c, nil
}

// Close releases all Metal resources, staging buffers, and temp files.
func (c *metalContext) Close() error {
	if c == nil || c.closed.Swap(true) {
		return nil
	}
	if c.pool != nil {
		c.pool.Close()
	}

	// Free Lanczos temp buffer
	if c.lanczosTmpBuf != nil {
		C.metal_buffer_free(c.lanczosTmpBuf)
		c.lanczosTmpBuf = nil
		c.lanczosTmpSize = 0
	}

	// Free cached luma key LUT buffer
	if c.lumaKeyLUTBuf != nil {
		C.metal_buffer_free(c.lumaKeyLUTBuf)
		c.lumaKeyLUTBuf = nil
	}

	// Free cached staging buffers
	c.stagingBufsMu.Lock()
	for _, s := range c.stagingBufs {
		if s.yBuf != nil {
			C.metal_buffer_free(s.yBuf)
		}
		if s.cbBuf != nil {
			C.metal_buffer_free(s.cbBuf)
		}
		if s.crBuf != nil {
			C.metal_buffer_free(s.crBuf)
		}
	}
	c.stagingBufs = nil
	c.stagingBufsMu.Unlock()

	// Free all cached pipeline states
	c.pipelinesMu.Lock()
	for _, p := range c.pipelines {
		C.metal_pipeline_free(p)
	}
	c.pipelines = nil
	c.pipelinesMu.Unlock()

	C.metal_release(c.device, c.queue, c.library)
	c.device = nil
	c.queue = nil
	c.library = nil

	// Clean up temp metallib file
	if c.tempMetallibPath != "" {
		os.Remove(c.tempMetallibPath)
		c.tempMetallibPath = ""
	}

	return nil
}

// getOrCreateStagingBuffers returns cached staging buffers for the given
// frame dimensions, allocating them on first use.
func (c *metalContext) getOrCreateStagingBuffers(width, height int) (*stagingBufferSet, error) {
	key := width<<16 | height // collision-free for resolutions up to 65535
	c.stagingBufsMu.Lock()
	defer c.stagingBufsMu.Unlock()

	if s, ok := c.stagingBufs[key]; ok {
		return s, nil
	}

	ySize := width * height
	cbSize := (width / 2) * (height / 2)

	yBuf, err := c.allocBuffer(ySize)
	if err != nil {
		return nil, fmt.Errorf("alloc Y staging: %w", err)
	}
	cbBuf, err := c.allocBuffer(cbSize)
	if err != nil {
		C.metal_buffer_free(yBuf)
		return nil, fmt.Errorf("alloc Cb staging: %w", err)
	}
	crBuf, err := c.allocBuffer(cbSize)
	if err != nil {
		C.metal_buffer_free(yBuf)
		C.metal_buffer_free(cbBuf)
		return nil, fmt.Errorf("alloc Cr staging: %w", err)
	}

	s := &stagingBufferSet{yBuf: yBuf, cbBuf: cbBuf, crBuf: crBuf, ySize: ySize}
	c.stagingBufs[key] = s
	return s, nil
}

// ensureLanczosTemp ensures the Lanczos-3 temporary float32 buffer is
// large enough for the given number of floats. Grows but never shrinks.
func (c *metalContext) ensureLanczosTemp(needed int) error {
	if needed <= c.lanczosTmpSize {
		return nil
	}
	if c.lanczosTmpBuf != nil {
		C.metal_buffer_free(c.lanczosTmpBuf)
		c.lanczosTmpBuf = nil
		c.lanczosTmpSize = 0
	}
	buf, err := c.allocBuffer(needed * 4) // 4 bytes per float32
	if err != nil {
		return fmt.Errorf("gpu: lanczos temp alloc (%d floats): %w", needed, err)
	}
	c.lanczosTmpBuf = buf
	c.lanczosTmpSize = needed
	return nil
}

// Backend returns "metal".
func (c *metalContext) Backend() string {
	return BackendMetal
}

// DeviceName returns the Metal GPU device name (e.g., "Apple M2 Max").
func (c *metalContext) DeviceName() string {
	if c == nil || c.device == nil {
		return ""
	}
	return C.GoString(C.metal_device_name(c.device))
}

// DeviceMemory returns the recommended max working set size in bytes.
func (c *metalContext) DeviceMemory() int64 {
	if c == nil || c.device == nil {
		return 0
	}
	return int64(C.metal_device_memory(c.device))
}

// Sync waits for all queued GPU operations to complete.
func (c *metalContext) Sync() error {
	if c == nil || c.closed.Load() {
		return nil
	}
	rc := C.metal_sync(c.queue)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: metal sync failed: %d", rc)
	}
	return nil
}

// SetPool associates a FramePool with this context.
func (c *metalContext) SetPool(pool *FramePool) {
	c.pool = pool
}

// Pool returns the associated FramePool, if any.
func (c *metalContext) Pool() *FramePool {
	return c.pool
}

// getPipeline lazily creates and caches a compute pipeline state for the named kernel.
func (c *metalContext) getPipeline(name string) (C.MetalPipelineRef, error) {
	c.pipelinesMu.Lock()
	defer c.pipelinesMu.Unlock()

	if p, ok := c.pipelines[name]; ok {
		return p, nil
	}

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	p := C.metal_pipeline_create(c.device, c.library, cName)
	if p == nil {
		return nil, fmt.Errorf("gpu: metal: failed to create pipeline for kernel %q", name)
	}

	c.pipelines[name] = p
	return p, nil
}

// allocBuffer allocates a Metal buffer with unified memory.
func (c *metalContext) allocBuffer(size int) (C.MetalBufferRef, error) {
	buf := C.metal_buffer_alloc(c.device, C.size_t(size))
	if buf == nil {
		return nil, fmt.Errorf("gpu: metal buffer alloc failed (size=%d)", size)
	}
	return buf, nil
}

// allocBufferAligned allocates a Metal buffer aligned to the given boundary.
func (c *metalContext) allocBufferAligned(size, alignment int) (C.MetalBufferRef, error) {
	buf := C.metal_buffer_alloc_aligned(c.device, C.size_t(size), C.size_t(alignment))
	if buf == nil {
		return nil, fmt.Errorf("gpu: metal buffer alloc aligned failed (size=%d, align=%d)", size, alignment)
	}
	return buf, nil
}
