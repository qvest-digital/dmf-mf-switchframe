//go:build !cgo || !cuda

package gpu

import "github.com/zsiec/switchframe/server/sidecar"

// SegmentationProxy is a stub for non-CUDA builds.
type SegmentationProxy struct{}

// Compile-time check that stub satisfies Segmenter.
var _ Segmenter = (*SegmentationProxy)(nil)

// NewSegmentationProxy returns nil on non-CUDA builds.
func NewSegmentationProxy(host *sidecar.Host, pool *sidecar.CUDAGPUPool) *SegmentationProxy {
	return nil
}

// EnableSource returns an error on non-CUDA builds.
func (p *SegmentationProxy) EnableSource(key string, w, h int, smoothing float32) error {
	return ErrGPUNotAvailable
}

// DisableSource is a no-op on non-CUDA builds.
func (p *SegmentationProxy) DisableSource(key string) {}

// SetPendingConfig is a no-op on non-CUDA builds.
func (p *SegmentationProxy) SetPendingConfig(key string, smoothing float32) {}

// HasPendingConfig returns false on non-CUDA builds.
func (p *SegmentationProxy) HasPendingConfig(key string) bool { return false }

// PendingSmoothing returns 0 on non-CUDA builds.
func (p *SegmentationProxy) PendingSmoothing(key string) float32 { return 0 }

// ClearPendingConfig is a no-op on non-CUDA builds.
func (p *SegmentationProxy) ClearPendingConfig(key string) {}

// IsEnabled returns false on non-CUDA builds.
func (p *SegmentationProxy) IsEnabled(key string) bool { return false }

// Segment is a no-op on non-CUDA builds.
func (p *SegmentationProxy) Segment(key string, frame *GPUFrame) {}

// MaskForSource returns nil on non-CUDA builds.
func (p *SegmentationProxy) MaskForSource(key string) *GPUFrame { return nil }

// MaskDevPtrForSource returns 0 on non-CUDA builds.
func (p *SegmentationProxy) MaskDevPtrForSource(key string) uintptr { return 0 }

// HandleMessage is a no-op on non-CUDA builds.
func (p *SegmentationProxy) HandleMessage(msg *sidecar.Message) {}

// RecoverStaleSlots is a no-op on non-CUDA builds.
func (p *SegmentationProxy) RecoverStaleSlots() {}

// ReenableSources is a no-op on non-CUDA builds.
func (p *SegmentationProxy) ReenableSources() {}

// Close is a no-op on non-CUDA builds.
func (p *SegmentationProxy) Close() {}
