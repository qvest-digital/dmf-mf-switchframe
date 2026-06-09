package gpu

// Segmenter is the interface for AI person segmentation engines.
// Implemented by SegmentationEngine (in-process TensorRT) and
// SegmentationProxy (sidecar delegation via CUDA IPC).
//
// The source manager calls Segment() on every ingested frame for enabled
// sources. The pipeline node reads masks via MaskForSource(), which returns
// the latest GPU-resident segmentation mask with a reference taken.
type Segmenter interface {
	// EnableSource creates a segmentation session for a source with the
	// given frame dimensions. The smoothing parameter is accepted for API
	// compatibility (RVM uses recurrent states instead of explicit smoothing).
	EnableSource(key string, w, h int, smoothing float32) error

	// DisableSource destroys the segmentation session for a source.
	DisableSource(key string)

	// SetPendingConfig stores a deferred segmentation config for a source.
	// Called by the REST API when the source resolution is not yet known.
	SetPendingConfig(key string, smoothing float32)

	// HasPendingConfig returns true if a deferred config exists for the
	// source but no active session has been created yet.
	HasPendingConfig(key string) bool

	// PendingSmoothing returns the smoothing value for a pending config.
	PendingSmoothing(key string) float32

	// ClearPendingConfig removes the deferred config for a source.
	ClearPendingConfig(key string)

	// IsEnabled returns true if segmentation is active for the source.
	IsEnabled(key string) bool

	// Segment submits a frame for segmentation processing. The frame must
	// be GPU-resident (DevPtr in shared VRAM). Implementations may process
	// synchronously (SegmentationEngine) or asynchronously (SegmentationProxy).
	Segment(key string, frame *GPUFrame)

	// MaskForSource returns the latest GPU-resident segmentation mask with
	// a reference taken; callers MUST Release() the returned frame when
	// done so DisableSource cannot free the mask's device memory while a
	// pipeline kernel is still reading it. Returns nil if no mask is
	// available. The returned GPUFrame is a uint8 single-plane mask
	// (255 = foreground, 0 = background).
	MaskForSource(key string) *GPUFrame

	// MaskDevPtrForSource returns the raw device pointer of the mask buffer
	// for a source. Returns 0 if no active session exists. Used by the sidecar
	// to export the mask buffer as a CUDA IPC handle for zero-copy sharing.
	MaskDevPtrForSource(key string) uintptr

	// Close releases all resources.
	Close()
}
