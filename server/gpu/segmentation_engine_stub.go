//go:build !cgo || !cuda || !tensorrt

package gpu

// SegmentationEngine is a stub for non-TensorRT builds.
type SegmentationEngine struct{}

// NewSegmentationEngine returns nil and ErrTensorRTNotAvailable on non-TensorRT builds.
func NewSegmentationEngine(ctx *Context, modelPath string) (*SegmentationEngine, error) {
	return nil, ErrTensorRTNotAvailable
}

// EnableSource returns ErrTensorRTNotAvailable on non-TensorRT builds.
func (se *SegmentationEngine) EnableSource(key string, w, h int, smoothing float32) error {
	return ErrTensorRTNotAvailable
}

// DisableSource is a no-op on non-TensorRT builds.
func (se *SegmentationEngine) DisableSource(key string) {}

// IsEnabled returns false on non-TensorRT builds.
func (se *SegmentationEngine) IsEnabled(key string) bool { return false }

// Segment is a no-op on non-TensorRT builds.
func (se *SegmentationEngine) Segment(key string, frame *GPUFrame) {}

// MaskForSource returns nil on non-TensorRT builds.
func (se *SegmentationEngine) MaskForSource(key string) *GPUFrame { return nil }

// Close is a no-op on non-TensorRT builds.
func (se *SegmentationEngine) Close() {}
