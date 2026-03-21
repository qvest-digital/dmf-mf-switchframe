//go:build darwin

package gpu

import (
	"fmt"
	"sync/atomic"
	"time"
)

// Timer measures GPU kernel execution time using wall-clock timing.
// Each Metal operation calls waitUntilCompleted before returning, so
// the wall-clock duration (time.Now before → after ProcessGPU) accurately
// captures GPU execution time plus negligible dispatch overhead (~10µs).
// MTLCommandBuffer.GPUStartTime/GPUEndTime would give sub-microsecond
// precision but adds complexity (storing command buffer references,
// deferred reads) for minimal practical benefit at frame-level granularity.
type Timer struct {
	startTime time.Time
}

// NewTimer creates a timer.
func NewTimer() (*Timer, error) {
	return &Timer{}, nil
}

// Start records the start time.
func (t *Timer) Start(stream uintptr) {
	t.startTime = time.Now()
}

// Stop returns elapsed time in milliseconds since Start.
func (t *Timer) Stop(stream uintptr) float32 {
	return float32(time.Since(t.startTime).Seconds() * 1000.0)
}

// Close is a no-op.
func (t *Timer) Close() {}

// PipelineMetrics tracks GPU pipeline performance counters.
type PipelineMetrics struct {
	UploadUs    atomic.Int64
	EncodeUs    atomic.Int64
	DownloadUs  atomic.Int64
	ScaleUs     atomic.Int64
	BlendUs     atomic.Int64
	KeyUs       atomic.Int64
	CompositeUs atomic.Int64
	DSKUs       atomic.Int64
	STMapUs     atomic.Int64
	FRUCUs      atomic.Int64
	V210Us      atomic.Int64
	TotalUs     atomic.Int64

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

// MemoryStatsExtended returns detailed GPU memory usage.
func MemoryStatsExtended(ctx *Context) map[string]any {
	if ctx == nil || ctx.mtl == nil {
		return map[string]any{"available": false}
	}
	stats := ctx.MemoryStats()
	result := map[string]any{
		"available": true,
		"total_mb":  stats.TotalMB,
		"free_mb":   stats.FreeMB,
		"used_mb":   stats.UsedMB,
		"device":    ctx.DeviceProperties().Name,
		"backend":   "metal",
		"compute":   fmt.Sprintf("apple_gpu_%d", ctx.DeviceProperties().ComputeCapability[0]),
	}
	if pool := ctx.Pool(); pool != nil {
		hits, misses := pool.Stats()
		result["pool_hits"] = hits
		result["pool_misses"] = misses
		result["pool_pitch"] = pool.Pitch()
	}
	return result
}
