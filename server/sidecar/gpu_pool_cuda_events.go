//go:build cgo && cuda

package sidecar

/*
#include <cuda_runtime.h>
*/
import "C"

import "fmt"

// WaitReadyEvent makes the default CUDA stream wait for the ready event on the
// given slot. The sidecar calls this after MarkProcessing to ensure the host's
// GPU writes to the frame slot are visible before the sidecar reads the frame.
//
// Uses cudaStreamWaitEvent on stream 0 (default stream) so that subsequent
// GPU operations on this stream are ordered after the host's event record.
//
// Returns nil for out-of-range slots or if no events are allocated (stub builds).
func (p *CUDAGPUPool) WaitReadyEvent(slot int) error {
	if slot < 0 || slot >= len(p.readyEvents) {
		return nil
	}
	evt := p.readyEvents[slot]
	if evt == nil {
		return nil
	}
	rc := C.cudaStreamWaitEvent(nil, evt, 0) // nil = default stream
	if rc != C.cudaSuccess {
		return fmt.Errorf("cudaStreamWaitEvent(ready[%d]): error %d", slot, rc)
	}
	return nil
}

// RecordDoneEvent records the done event on the given slot on the default CUDA
// stream. The sidecar calls this after GPU inference completes (before marking
// the slot Done via the control region) so the host can synchronize before
// reading results.
//
// Returns nil for out-of-range slots or if no events are allocated (stub builds).
func (p *CUDAGPUPool) RecordDoneEvent(slot int) error {
	if slot < 0 || slot >= len(p.doneEvents) {
		return nil
	}
	evt := p.doneEvents[slot]
	if evt == nil {
		return nil
	}
	rc := C.cudaEventRecord(evt, nil) // nil = default stream
	if rc != C.cudaSuccess {
		return fmt.Errorf("cudaEventRecord(done[%d]): error %d", slot, rc)
	}
	return nil
}
