package gpu

// GPUWorkQueue represents an isolated GPU execution context.
// On Metal: wraps an MTLCommandQueue (serial command buffer submission).
// On CUDA: wraps a cudaStream_t (serial kernel/memcpy execution).
// On stub: nil handle (no GPU operations).
type GPUWorkQueue struct {
	handle uintptr
}

// IsValid returns true if the work queue holds a non-nil GPU handle.
func (q *GPUWorkQueue) IsValid() bool { return q != nil && q.handle != 0 }
