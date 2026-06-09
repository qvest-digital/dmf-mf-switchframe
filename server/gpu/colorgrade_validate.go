package gpu

import "fmt"

// Sane bounds for 3D LUT cube dimensions. Real-world .cube LUTs are
// typically 17, 33, or 65; 2 is the minimum that supports trilinear
// interpolation and 64 covers the largest practical sizes while keeping
// the device allocation (size^3*3 floats) bounded.
const (
	minLUT3DSize = 2
	maxLUT3DSize = 64
)

// validateLUT3D checks that a flat LUT float buffer is consistent with the
// declared cube dimension before it is uploaded to the GPU. The color-grade
// kernels index lut[((b*size^2)+(g*size)+r)*3+ch], so the buffer MUST hold
// exactly size^3*3 floats; a smaller buffer with a larger declared size
// would make the kernel read past the device allocation (VRAM corruption /
// illegal access). lutSize and the data arrive from independent provider
// calls, so this fails closed when they disagree.
func validateLUT3D(data []float32, lutSize int) error {
	if lutSize < minLUT3DSize || lutSize > maxLUT3DSize {
		return fmt.Errorf("gpu: LUT3D size %d out of range [%d,%d]", lutSize, minLUT3DSize, maxLUT3DSize)
	}
	want := lutSize * lutSize * lutSize * 3
	if len(data) != want {
		return fmt.Errorf("gpu: LUT3D data length %d does not match size %d (want %d floats)", len(data), lutSize, want)
	}
	return nil
}
