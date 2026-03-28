package gpu

import "fmt"

// GPUFrameFormat identifies the pixel format of a GPUFrame.
type GPUFrameFormat int

const (
	// GPUFormatNV12 is 8-bit 4:2:0 semi-planar (Y uint8 + UV uint8 interleaved).
	// Y plane: pitch * height bytes. UV plane: pitch * height/2 bytes.
	// Total: pitch * height * 3/2.
	GPUFormatNV12 GPUFrameFormat = iota

	// GPUFormatP210 is 10-bit 4:2:2 semi-planar (Y uint16 + UV uint16 interleaved).
	// Y plane: pitch * height bytes (pitch = width*2 rounded to alignment).
	// UV plane: pitch * height bytes (full height for 4:2:2).
	// Total: pitch * height * 2.
	GPUFormatP210
)

// String returns a human-readable name for the format.
func (f GPUFrameFormat) String() string {
	switch f {
	case GPUFormatNV12:
		return "NV12"
	case GPUFormatP210:
		return "P210"
	default:
		return fmt.Sprintf("Unknown(%d)", int(f))
	}
}
