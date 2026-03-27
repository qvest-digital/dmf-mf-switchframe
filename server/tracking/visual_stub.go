//go:build !cgo || !cuda || !tensorrt

package tracking

import "errors"

// ErrVisualDisabled indicates visual localization is not available
// on this build (requires cgo, CUDA, and TensorRT).
var ErrVisualDisabled = errors.New("tracking: visual localization disabled (no CUDA/TensorRT)")

// VisualLocalizer is a stub for builds without CUDA/TensorRT support.
type VisualLocalizer struct{}

// NewVisualLocalizer returns ErrVisualDisabled on non-CUDA builds.
func NewVisualLocalizer(mapDir string, superpointPlan, lightgluePlan string, intrinsics CameraIntrinsics) (*VisualLocalizer, error) {
	return nil, ErrVisualDisabled
}

// Name returns the tracking source name.
func (v *VisualLocalizer) Name() string { return "visual" }

// Type returns the tracking system type.
func (v *VisualLocalizer) Type() string { return "visual" }

// Start returns ErrVisualDisabled on non-CUDA builds.
func (v *VisualLocalizer) Start() error { return ErrVisualDisabled }

// Stop is a no-op on non-CUDA builds.
func (v *VisualLocalizer) Stop() error { return nil }

// Running always returns false on non-CUDA builds.
func (v *VisualLocalizer) Running() bool { return false }

// LatestPose always returns nil on non-CUDA builds.
func (v *VisualLocalizer) LatestPose() *CameraPose { return nil }

// ProcessFrame is a no-op on non-CUDA builds.
func (v *VisualLocalizer) ProcessFrame(framePtr uintptr, w, h, pitch int) {}

// Close is a no-op on non-CUDA builds.
func (v *VisualLocalizer) Close() error { return nil }
