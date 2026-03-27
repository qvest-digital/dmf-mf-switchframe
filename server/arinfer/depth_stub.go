//go:build !cgo || !cuda || !tensorrt

package arinfer

// depthAnythingV2 is a stub for builds without TensorRT/CUDA support.
type depthAnythingV2 struct{}

// newDepthAnythingV2 returns ErrDisabled on non-TensorRT builds.
func newDepthAnythingV2(planPath string, inputW, inputH int) (*depthAnythingV2, error) {
	return nil, ErrDisabled
}

// Infer returns ErrDisabled on non-TensorRT builds.
func (d *depthAnythingV2) Infer(inputPtr uintptr, w, h, pitch int, outputPtr uintptr) error {
	return ErrDisabled
}

// OutputPtr returns 0 on non-TensorRT builds (no GPU buffer).
func (d *depthAnythingV2) OutputPtr() uintptr { return 0 }

// Close is a no-op on non-TensorRT builds.
func (d *depthAnythingV2) Close() {}
