//go:build (!cgo || !cuda) && !darwin

package gpu

// NewWorkQueue returns ErrGPUNotAvailable on non-GPU builds.
func NewWorkQueue(ctx *Context) (*GPUWorkQueue, error) {
	return nil, ErrGPUNotAvailable
}

// CloseWorkQueue is a no-op on non-GPU builds.
func CloseWorkQueue(q *GPUWorkQueue) {}

// SyncWorkQueue is a no-op on non-GPU builds.
func SyncWorkQueue(q *GPUWorkQueue) error { return nil }
