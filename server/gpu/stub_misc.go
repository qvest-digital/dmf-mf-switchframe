//go:build (!cgo || !cuda) && !darwin

package gpu

// Timer is a stub for non-GPU builds.
type Timer struct{}

// NewTimer returns ErrGPUNotAvailable on non-GPU builds.
func NewTimer() (*Timer, error) { return nil, ErrGPUNotAvailable }

// Start is a no-op on non-GPU builds.
func (t *Timer) Start(stream uintptr) {}

// Stop returns 0 on non-GPU builds.
func (t *Timer) Stop(stream uintptr) float32 { return 0 }

// Close is a no-op on non-GPU builds.
func (t *Timer) Close() {}

// PipelineMetrics tracks GPU pipeline performance counters.
type PipelineMetrics struct{}

// Snapshot returns empty stats on non-GPU builds.
func (m *PipelineMetrics) Snapshot() map[string]any { return map[string]any{} }

// MemoryStatsExtended returns unavailable status on non-GPU builds.
func MemoryStatsExtended(ctx *Context) map[string]any {
	return map[string]any{"available": false}
}

// FRUC is a stub for non-GPU builds.
type FRUC struct{}

// FRUCAvailable returns false on non-GPU builds.
func FRUCAvailable() bool { return false }

// NewFRUC returns ErrGPUNotAvailable on non-GPU builds.
func NewFRUC(ctx *Context, width, height int) (*FRUC, error) { return nil, ErrGPUNotAvailable }

// Interpolate returns ErrGPUNotAvailable on non-GPU builds.
func (f *FRUC) Interpolate(prev, curr, output *GPUFrame, alpha float64) error {
	return ErrGPUNotAvailable
}

// Close is a no-op on non-GPU builds.
func (f *FRUC) Close() {}

// V210LineStride returns the byte stride for one line of V210 data.
func V210LineStride(width int) int {
	groups := (width + 5) / 6
	rawBytes := groups * 16
	return (rawBytes + 127) &^ 127
}

// V210ToNV12 returns ErrGPUNotAvailable on non-GPU builds.
func V210ToNV12(ctx *Context, dst *GPUFrame, v210DevPtr uintptr, v210Stride, width, height int) error {
	return ErrGPUNotAvailable
}

// NV12ToV210 returns ErrGPUNotAvailable on non-GPU builds.
func NV12ToV210(ctx *Context, v210DevPtr uintptr, v210Stride int, src *GPUFrame, width, height int) error {
	return ErrGPUNotAvailable
}

// UploadV210 returns ErrGPUNotAvailable on non-GPU builds.
func UploadV210(ctx *Context, dst *GPUFrame, v210 []byte, width, height int) error {
	return ErrGPUNotAvailable
}

// DownloadV210 returns ErrGPUNotAvailable on non-GPU builds.
func DownloadV210(ctx *Context, v210 []byte, src *GPUFrame, width, height int) error {
	return ErrGPUNotAvailable
}
