package video

import "encoding/binary"

// FillFrame fills buf with uniform Y, Cb, Cr sample values for the given format.
// For 8-bit formats, values are clamped to [0,255]. For 10-bit formats, values
// are clamped to [0,1023] and stored as little-endian uint16.
// buf must be at least f.FrameSize(w, h) bytes.
func FillFrame(buf []byte, f Format, w, h, y, cb, cr int) {
	yPlane := f.YPlaneSize(w, h)
	chromaPlane := f.ChromaPlaneSize(w, h)
	cbOff := yPlane
	crOff := yPlane + chromaPlane

	if f == YUV422_10bit {
		fill10bit(buf[:yPlane], uint16(y))
		fill10bit(buf[cbOff:cbOff+chromaPlane], uint16(cb))
		fill10bit(buf[crOff:crOff+chromaPlane], uint16(cr))
	} else {
		fillBytes(buf[:yPlane], byte(y))
		fillBytes(buf[cbOff:cbOff+chromaPlane], byte(cb))
		fillBytes(buf[crOff:crOff+chromaPlane], byte(cr))
	}
}

// FillBlack fills buf with BT.709 limited-range black for the given format.
// 8-bit: Y=16, Cb=Cr=128. 10-bit: Y=64, Cb=Cr=512.
// buf must be at least f.FrameSize(w, h) bytes.
func FillBlack(buf []byte, f Format, w, h int) {
	FillFrame(buf, f, w, h, f.BlackY(), f.NeutralChroma(), f.NeutralChroma())
}

// NewBlackFrame allocates and returns a new frame filled with BT.709
// limited-range black for the given format and dimensions.
func NewBlackFrame(f Format, w, h int) []byte {
	buf := make([]byte, f.FrameSize(w, h))
	FillBlack(buf, f, w, h)
	return buf
}

// NewFrame allocates and returns a new frame filled with uniform Y, Cb, Cr values.
func NewFrame(f Format, w, h, y, cb, cr int) []byte {
	buf := make([]byte, f.FrameSize(w, h))
	FillFrame(buf, f, w, h, y, cb, cr)
	return buf
}

// fillBytes fills dst with a repeated byte value.
func fillBytes(dst []byte, v byte) {
	for i := range dst {
		dst[i] = v
	}
}

// fill10bit fills dst with a repeated uint16 value in little-endian byte order.
// dst length must be a multiple of 2.
func fill10bit(dst []byte, v uint16) {
	var pair [2]byte
	binary.LittleEndian.PutUint16(pair[:], v)
	for i := 0; i < len(dst); i += 2 {
		dst[i] = pair[0]
		dst[i+1] = pair[1]
	}
}
