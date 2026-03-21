//go:build cgo && cuda && !darwin

package gpu

/*
#include <cuda_runtime.h>
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// NewWorkQueue creates a new CUDA stream wrapped as a GPUWorkQueue.
// Operations submitted to different streams can execute concurrently on the GPU.
// The caller must call CloseWorkQueue when done.
func NewWorkQueue(ctx *Context) (*GPUWorkQueue, error) {
	if ctx == nil {
		return nil, ErrGPUNotAvailable
	}
	stream, err := ctx.NewStream()
	if err != nil {
		return nil, fmt.Errorf("gpu: failed to create CUDA stream for work queue: %w", err)
	}
	return &GPUWorkQueue{handle: uintptr(unsafe.Pointer(stream))}, nil
}

// CloseWorkQueue destroys the CUDA stream and zeros the handle.
func CloseWorkQueue(q *GPUWorkQueue) {
	if q == nil || q.handle == 0 {
		return
	}
	stream := C.cudaStream_t(unsafe.Pointer(q.handle))
	C.cudaStreamDestroy(stream)
	q.handle = 0
}

// SyncWorkQueue synchronizes the CUDA stream, blocking until all previously
// queued operations on the stream have completed.
func SyncWorkQueue(q *GPUWorkQueue) error {
	if q == nil || q.handle == 0 {
		return nil
	}
	stream := C.cudaStream_t(unsafe.Pointer(q.handle))
	if rc := C.cudaStreamSynchronize(stream); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: cuda work queue sync failed: %d", rc)
	}
	return nil
}

// cudaStream extracts the underlying C.cudaStream_t from a GPUWorkQueue.
// Returns nil if the queue is nil or invalid.
func cudaStream(q *GPUWorkQueue) C.cudaStream_t {
	if q == nil || q.handle == 0 {
		return nil
	}
	return C.cudaStream_t(unsafe.Pointer(q.handle))
}
