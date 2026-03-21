//go:build (!cgo || !cuda) && !darwin

package gpu

// GPUSourceManager is a stub for non-GPU builds.
type GPUSourceManager struct{}

// NewGPUSourceManager returns nil on non-GPU builds.
func NewGPUSourceManager(ctx *Context, pool *FramePool, stmaps SourceSTMapProvider) *GPUSourceManager {
	return nil
}

// RegisterSource is a no-op on non-GPU builds.
func (m *GPUSourceManager) RegisterSource(sourceKey string, w, h int, preview *PreviewConfig) {}

// RemoveSource is a no-op on non-GPU builds.
func (m *GPUSourceManager) RemoveSource(sourceKey string) {}

// IngestYUV is a no-op on non-GPU builds.
func (m *GPUSourceManager) IngestYUV(sourceKey string, yuv []byte, w, h int, pts int64) {}

// GetFrame returns nil on non-GPU builds.
func (m *GPUSourceManager) GetFrame(sourceKey string) *GPUFrame { return nil }

// Close is a no-op on non-GPU builds.
func (m *GPUSourceManager) Close() {}

// Snapshot returns empty stats on non-GPU builds.
func (m *GPUSourceManager) Snapshot() map[string]any {
	return map[string]any{
		"source_count": 0,
		"sources":      map[string]any{},
	}
}

// CopyGPUFrame returns nil on non-GPU builds (no frames to copy).
func CopyGPUFrame(dst, src *GPUFrame) error { return nil }

// CopyGPUFrameOn returns ErrGPUNotAvailable on non-GPU builds.
func CopyGPUFrameOn(dst, src *GPUFrame, q *GPUWorkQueue) error { return ErrGPUNotAvailable }

