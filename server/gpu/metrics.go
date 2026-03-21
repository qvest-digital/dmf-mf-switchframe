//go:build cgo && cuda

package gpu

/*
#include <cuda.h>
#include <cuda_runtime.h>
*/
import "C"

import (
	"fmt"
	"sync/atomic"
)

// Timer measures GPU kernel execution time using CUDA events.
// CUDA events are recorded on a stream and measure the actual GPU-side
// duration, not including cgo overhead or CPU-side scheduling.
type Timer struct {
	start C.cudaEvent_t
	stop  C.cudaEvent_t
}

// NewTimer creates a CUDA event timer.
func NewTimer() (*Timer, error) {
	t := &Timer{}
	if rc := C.cudaEventCreate(&t.start); rc != C.cudaSuccess {
		return nil, fmt.Errorf("gpu: create start event: %d", rc)
	}
	if rc := C.cudaEventCreate(&t.stop); rc != C.cudaSuccess {
		C.cudaEventDestroy(t.start)
		return nil, fmt.Errorf("gpu: create stop event: %d", rc)
	}
	return t, nil
}

// Start records the start event on the given stream.
func (t *Timer) Start(stream C.cudaStream_t) {
	C.cudaEventRecord(t.start, stream)
}

// Stop records the stop event, synchronizes, and returns elapsed milliseconds.
// This blocks until the GPU work between Start and Stop completes.
func (t *Timer) Stop(stream C.cudaStream_t) float32 {
	C.cudaEventRecord(t.stop, stream)
	C.cudaEventSynchronize(t.stop)
	var ms C.float
	C.cudaEventElapsedTime(&ms, t.start, t.stop)
	return float32(ms)
}

// Close releases the CUDA events.
func (t *Timer) Close() {
	if t.start != nil {
		C.cudaEventDestroy(t.start)
	}
	if t.stop != nil {
		C.cudaEventDestroy(t.stop)
	}
}

// PipelineMetrics tracks GPU pipeline performance counters.
type PipelineMetrics struct {
	// Per-operation timing (microseconds, atomic for concurrent reads)
	UploadUs   atomic.Int64
	EncodeUs   atomic.Int64
	DownloadUs atomic.Int64
	ScaleUs    atomic.Int64
	BlendUs    atomic.Int64
	KeyUs      atomic.Int64
	CompositeUs atomic.Int64
	DSKUs      atomic.Int64
	STMapUs    atomic.Int64
	FRUCUs     atomic.Int64
	V210Us     atomic.Int64
	TotalUs    atomic.Int64

	// Frame counters
	FramesProcessed atomic.Int64
	FramesDropped   atomic.Int64
}

// Snapshot returns a JSON-serializable view of GPU pipeline metrics.
func (m *PipelineMetrics) Snapshot() map[string]any {
	return map[string]any{
		"upload_us":    m.UploadUs.Load(),
		"encode_us":    m.EncodeUs.Load(),
		"download_us":  m.DownloadUs.Load(),
		"scale_us":     m.ScaleUs.Load(),
		"blend_us":     m.BlendUs.Load(),
		"key_us":       m.KeyUs.Load(),
		"composite_us": m.CompositeUs.Load(),
		"dsk_us":       m.DSKUs.Load(),
		"stmap_us":     m.STMapUs.Load(),
		"fruc_us":      m.FRUCUs.Load(),
		"v210_us":      m.V210Us.Load(),
		"total_us":     m.TotalUs.Load(),
		"frames":       m.FramesProcessed.Load(),
		"drops":        m.FramesDropped.Load(),
	}
}

// MemoryStatsExtended returns detailed GPU memory usage including pool stats.
func MemoryStatsExtended(ctx *Context) map[string]any {
	if ctx == nil {
		return map[string]any{"available": false}
	}
	stats := ctx.MemoryStats()
	result := map[string]any{
		"available": true,
		"total_mb":  stats.TotalMB,
		"free_mb":   stats.FreeMB,
		"used_mb":   stats.UsedMB,
		"device":    ctx.DeviceProperties().Name,
		"compute":   fmt.Sprintf("sm_%d%d", ctx.DeviceProperties().ComputeCapability[0], ctx.DeviceProperties().ComputeCapability[1]),
	}
	if pool := ctx.Pool(); pool != nil {
		hits, misses := pool.Stats()
		result["pool_hits"] = hits
		result["pool_misses"] = misses
		result["pool_pitch"] = pool.Pitch()
	}
	return result
}
