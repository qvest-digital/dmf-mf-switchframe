//go:build !cgo || !cuda

package sidecar

// WaitReadyEvent is a no-op on non-CUDA builds.
func (p *CUDAGPUPool) WaitReadyEvent(slot int) error { return nil }

// RecordDoneEvent is a no-op on non-CUDA builds.
func (p *CUDAGPUPool) RecordDoneEvent(slot int) error { return nil }
