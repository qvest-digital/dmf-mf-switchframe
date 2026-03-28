//go:build !cuda || !cgo

package sidecar

import "fmt"

// CUDAIPCHandle is a serializable handle to a CUDA device allocation.
// Exactly 64 bytes (CU_IPC_HANDLE_SIZE).
type CUDAIPCHandle [64]byte

// CUDAIPCEventHandle is a serializable handle to a CUDA IPC event.
// Exactly 64 bytes.
type CUDAIPCEventHandle [64]byte

// CUDAGPUPool is a stub for non-CUDA builds.
// All constructors return ErrCUDANotAvailable.
type CUDAGPUPool struct {
	cfg GPUPoolConfig
}

// ErrCUDANotAvailable is returned when CUDA IPC pool operations are attempted
// on a build without CUDA support.
var ErrCUDANotAvailable = fmt.Errorf("gpu pool: CUDA not available (build without cgo+cuda)")

// NewCUDAGPUPoolHost returns ErrCUDANotAvailable on non-CUDA builds.
func NewCUDAGPUPoolHost(cfg GPUPoolConfig, shmName string) (*CUDAGPUPool, error) {
	return nil, ErrCUDANotAvailable
}

// NewCUDAGPUPoolHostFromPtrs returns ErrCUDANotAvailable on non-CUDA builds.
func NewCUDAGPUPoolHostFromPtrs(framePtrs []uintptr, cfg GPUPoolConfig, shmName string) (*CUDAGPUPool, error) {
	return nil, ErrCUDANotAvailable
}

// ExportIPCHandle returns ErrCUDANotAvailable on non-CUDA builds.
func ExportIPCHandle(devPtr uintptr) (CUDAIPCHandle, error) {
	return CUDAIPCHandle{}, ErrCUDANotAvailable
}

// ImportIPCHandle returns ErrCUDANotAvailable on non-CUDA builds.
func ImportIPCHandle(h CUDAIPCHandle) (uintptr, error) {
	return 0, ErrCUDANotAvailable
}

// CloseIPCHandle is a no-op on non-CUDA builds.
func CloseIPCHandle(devPtr uintptr) {}

// OpenCUDAGPUPoolWorker returns ErrCUDANotAvailable on non-CUDA builds.
func OpenCUDAGPUPoolWorker(
	frameHandles, resultHandles []CUDAIPCHandle,
	readyHandles, doneHandles []CUDAIPCEventHandle,
	cfg GPUPoolConfig,
	shmName string,
) (*CUDAGPUPool, error) {
	return nil, ErrCUDANotAvailable
}

// FrameHandles returns nil on non-CUDA builds.
func (p *CUDAGPUPool) FrameHandles() []CUDAIPCHandle { return nil }

// ResultHandles returns nil on non-CUDA builds.
func (p *CUDAGPUPool) ResultHandles() []CUDAIPCHandle { return nil }

// ReadyEventHandles returns nil on non-CUDA builds.
func (p *CUDAGPUPool) ReadyEventHandles() []CUDAIPCEventHandle { return nil }

// DoneEventHandles returns nil on non-CUDA builds.
func (p *CUDAGPUPool) DoneEventHandles() []CUDAIPCEventHandle { return nil }

// FrameDevPtr returns 0 on non-CUDA builds.
func (p *CUDAGPUPool) FrameDevPtr(slot int) uintptr { return 0 }

// ResultDevPtr returns 0 on non-CUDA builds.
func (p *CUDAGPUPool) ResultDevPtr(slot int) uintptr { return 0 }

// Control returns nil on non-CUDA builds.
func (p *CUDAGPUPool) Control() *GPUPoolControl { return nil }

// Config returns the pool configuration.
func (p *CUDAGPUPool) Config() GPUPoolConfig { return p.cfg }

// SlotCount returns 0 on non-CUDA builds.
func (p *CUDAGPUPool) SlotCount() int { return 0 }

// Close is a no-op on non-CUDA builds.
func (p *CUDAGPUPool) Close() error { return nil }
