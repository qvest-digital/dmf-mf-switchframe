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

// CopyGPUFrame is a no-op on non-GPU builds.
func CopyGPUFrame(dst, src *GPUFrame) {}
