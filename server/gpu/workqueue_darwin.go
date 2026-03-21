//go:build darwin

package gpu

/*
#include "metal_bridge.h"
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// NewWorkQueue creates a new Metal command queue on the same device as ctx.
// Each queue serializes its own command buffers independently, preventing
// interleaving with other queues.
func NewWorkQueue(ctx *Context) (*GPUWorkQueue, error) {
	if ctx == nil || ctx.mtl == nil {
		return nil, ErrGPUNotAvailable
	}
	queue := ctx.mtl.createQueue()
	if queue == nil {
		return nil, fmt.Errorf("gpu: failed to create Metal command queue")
	}
	return &GPUWorkQueue{handle: uintptr(unsafe.Pointer(queue))}, nil
}

// CloseWorkQueue releases the work queue. Under MRC (-fno-objc-arc),
// the MTLCommandQueue created by [dev newCommandQueue] is retained and
// must be explicitly released via CFRelease.
func CloseWorkQueue(q *GPUWorkQueue) {
	if q != nil && q.handle != 0 {
		C.metal_queue_release(C.MetalQueueRef(unsafe.Pointer(q.handle)))
		q.handle = 0
	}
}

// SyncWorkQueue synchronizes the work queue, blocking until all previously
// submitted command buffers on this queue have completed.
func SyncWorkQueue(q *GPUWorkQueue) error {
	if q == nil || q.handle == 0 {
		return nil
	}
	queue := C.MetalQueueRef(unsafe.Pointer(q.handle))
	rc := C.metal_sync(queue)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: metal work queue sync failed: %d", rc)
	}
	return nil
}

// metalQueueRef extracts the underlying C.MetalQueueRef from a GPUWorkQueue.
// Returns nil if the queue is nil or invalid.
func metalQueueRef(q *GPUWorkQueue) C.MetalQueueRef {
	if q == nil || q.handle == 0 {
		return nil
	}
	return C.MetalQueueRef(unsafe.Pointer(q.handle))
}
