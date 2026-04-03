package video

import "fmt"

// Format defines the pixel format and bit depth for YUV video frames.
type Format int

const (
	// YUV420_8bit is planar YUV 4:2:0 at 8 bits per sample (standard broadcast).
	// Frame size: w*h*3/2. Layout: Y (w*h) + Cb (w/2 * h/2) + Cr (w/2 * h/2).
	YUV420_8bit Format = iota
	// YUV422_10bit is planar YUV 4:2:2 at 10 bits per sample (professional broadcast).
	// Frame size: w*h*4. Layout: Y (w*h*2) + Cb (w/2 * h * 2) + Cr (w/2 * h * 2).
	YUV422_10bit
)

// String returns the FFmpeg-style pixel format name.
func (f Format) String() string {
	switch f {
	case YUV420_8bit:
		return "yuv420p"
	case YUV422_10bit:
		return "yuv422p10le"
	default:
		return fmt.Sprintf("unknown(%d)", int(f))
	}
}

// BytesPerSample returns the number of bytes per luma/chroma sample.
func (f Format) BytesPerSample() int {
	if f == YUV422_10bit {
		return 2
	}
	return 1
}

// ChromaWidth returns the chroma plane width (half luma width for both 420 and 422).
func (f Format) ChromaWidth(w int) int {
	return w / 2
}

// ChromaHeight returns the chroma plane height.
// For 4:2:0 this is half the luma height; for 4:2:2 it equals luma height.
func (f Format) ChromaHeight(h int) int {
	if f == YUV422_10bit {
		return h
	}
	return h / 2
}

// YPlaneSize returns the Y plane size in bytes.
func (f Format) YPlaneSize(w, h int) int {
	return w * h * f.BytesPerSample()
}

// ChromaPlaneSize returns the size of one chroma plane (Cb or Cr) in bytes.
func (f Format) ChromaPlaneSize(w, h int) int {
	return f.ChromaWidth(w) * f.ChromaHeight(h) * f.BytesPerSample()
}

// FrameSize returns the total frame size in bytes (Y + Cb + Cr).
func (f Format) FrameSize(w, h int) int {
	return f.YPlaneSize(w, h) + 2*f.ChromaPlaneSize(w, h)
}

// CbOffset returns the byte offset of the Cb plane in a planar frame.
func (f Format) CbOffset(w, h int) int {
	return f.YPlaneSize(w, h)
}

// CrOffset returns the byte offset of the Cr plane in a planar frame.
func (f Format) CrOffset(w, h int) int {
	return f.YPlaneSize(w, h) + f.ChromaPlaneSize(w, h)
}

// BlackY returns the black level for the Y (luma) channel.
// BT.709 limited range: 16 for 8-bit, 64 for 10-bit.
func (f Format) BlackY() int {
	if f == YUV422_10bit {
		return 64
	}
	return 16
}

// WhiteY returns the white level for the Y (luma) channel.
// BT.709 limited range: 235 for 8-bit, 940 for 10-bit.
func (f Format) WhiteY() int {
	if f == YUV422_10bit {
		return 940
	}
	return 235
}

// NeutralChroma returns the neutral (zero-color) value for Cb/Cr channels.
// 128 for 8-bit, 512 for 10-bit.
func (f Format) NeutralChroma() int {
	if f == YUV422_10bit {
		return 512
	}
	return 128
}

// MaxSampleValue returns the maximum representable sample value.
// 255 for 8-bit, 1023 for 10-bit.
func (f Format) MaxSampleValue() int {
	if f == YUV422_10bit {
		return 1023
	}
	return 255
}

// YUV420FrameSize returns the byte size of a YUV420P 8-bit frame (w*h*3/2).
// Convenience function for the common case -- equivalent to YUV420_8bit.FrameSize(w, h).
func YUV420FrameSize(w, h int) int {
	return w * h * 3 / 2
}
