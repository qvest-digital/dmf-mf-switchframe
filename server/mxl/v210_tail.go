package mxl

import (
	"encoding/binary"
	"fmt"
	"unsafe"
)

// A v210 line packs six pixels into four little-endian 32-bit words and is
// padded to 128 bytes, (width+47)/48*128 as libmxl sizes it, so any even
// width fits. A line whose width is not a multiple of six ends in a partial
// group, which still takes all four words; the samples of the pixels past
// the width are zero, as ffmpeg's v210 encoder writes them:
//
//	pixels  word 0         word 1         word 2         word 3
//	2       Cb0 Y0 Cr0     Y1  0   0      0   0   0      0  0   0
//	4       Cb0 Y0 Cr0     Y1  Cb2 Y2     Cr2 Y3  0      0  0   0
//
// The row converters handle the whole groups; these handle the last one.

// v210CheckWidth rejects a width v210 cannot carry: 4:2:2 shares one
// chroma sample between two pixels, so the width is even.
func v210CheckWidth(width int) error {
	if width < 2 || width%2 != 0 {
		return fmt.Errorf("v210: width %d must be even and >= 2", width)
	}
	return nil
}

// packV210Group writes one group of 10-bit samples.
func packV210Group(out []byte, y *[6]uint32, cb, cr *[3]uint32) {
	binary.LittleEndian.PutUint32(out[0:], (cb[0]&0x3FF)|(y[0]&0x3FF)<<10|(cr[0]&0x3FF)<<20)
	binary.LittleEndian.PutUint32(out[4:], (y[1]&0x3FF)|(cb[1]&0x3FF)<<10|(y[2]&0x3FF)<<20)
	binary.LittleEndian.PutUint32(out[8:], (cr[1]&0x3FF)|(y[3]&0x3FF)<<10|(cb[2]&0x3FF)<<20)
	binary.LittleEndian.PutUint32(out[12:], (y[4]&0x3FF)|(cr[2]&0x3FF)<<10|(y[5]&0x3FF)<<20)
}

// unpackV210Group reads one group of 10-bit samples.
func unpackV210Group(in []byte, y *[6]uint32, cb, cr *[3]uint32) {
	w0 := binary.LittleEndian.Uint32(in[0:])
	w1 := binary.LittleEndian.Uint32(in[4:])
	w2 := binary.LittleEndian.Uint32(in[8:])
	w3 := binary.LittleEndian.Uint32(in[12:])
	*y = [6]uint32{w0 >> 10 & 0x3FF, w1 & 0x3FF, w1 >> 20 & 0x3FF, w2 >> 10 & 0x3FF, w3 & 0x3FF, w3 >> 20 & 0x3FF}
	*cb = [3]uint32{w0 & 0x3FF, w1 >> 10 & 0x3FF, w2 >> 20 & 0x3FF}
	*cr = [3]uint32{w0 >> 20 & 0x3FF, w2 & 0x3FF, w3 >> 10 & 0x3FF}
}

// expand8to10 widens an 8-bit sample as V210PackRow does, by bit
// replication.
func expand8to10(v byte) uint32 { return uint32(v)<<2 | uint32(v)>>6 }

// packV210Tail8 packs the last n pixels of a row of 8-bit samples, n/2
// chroma pairs, into the group at out.
func packV210Tail8(out, y, cb, cr []byte, n int) {
	var gy [6]uint32
	var gcb, gcr [3]uint32
	for i := 0; i < n; i++ {
		gy[i] = expand8to10(y[i])
	}
	for i := 0; i < n/2; i++ {
		gcb[i], gcr[i] = expand8to10(cb[i]), expand8to10(cr[i])
	}
	packV210Group(out, &gy, &gcb, &gcr)
}

// unpackV210Tail8 unpacks the last n pixels of a row from the group at in
// into 8-bit samples, truncating as V210UnpackRow does.
func unpackV210Tail8(y, cb, cr, in []byte, n int) {
	var gy [6]uint32
	var gcb, gcr [3]uint32
	unpackV210Group(in, &gy, &gcb, &gcr)
	for i := 0; i < n; i++ {
		y[i] = byte(gy[i] >> 2)
	}
	for i := 0; i < n/2; i++ {
		cb[i], cr[i] = byte(gcb[i]>>2), byte(gcr[i]>>2)
	}
}

// packV210Tail10 packs the last n pixels of a row of 10-bit samples into
// the group at out.
func packV210Tail10(out []byte, y, cb, cr []uint16, n int) {
	var gy [6]uint32
	var gcb, gcr [3]uint32
	for i := 0; i < n; i++ {
		gy[i] = uint32(y[i])
	}
	for i := 0; i < n/2; i++ {
		gcb[i], gcr[i] = uint32(cb[i]), uint32(cr[i])
	}
	packV210Group(out, &gy, &gcb, &gcr)
}

// yuv422P10ToV210PartialInto is YUV422P10ToV210Into for a width that is
// not a multiple of six: the row kernel packs the whole groups, then the
// partial one.
func yuv422P10ToV210PartialInto(yuv, out []byte, width, height int) error {
	if err := v210CheckWidth(width); err != nil {
		return err
	}
	if height < 1 {
		return fmt.Errorf("v210_10bit: height %d must be >= 1", height)
	}
	chromaW := width / 2
	ySize := width * height
	cSize := chromaW * height
	if len(yuv) < (ySize+2*cSize)*2 {
		return fmt.Errorf("v210_10bit: YUV422P10 buffer size %d too small, need %d for %dx%d",
			len(yuv), (ySize+2*cSize)*2, width, height)
	}
	stride := V210LineStride(width)
	if len(out) < stride*height {
		return fmt.Errorf("v210_10bit: output buffer size %d too small, need %d for %dx%d",
			len(out), stride*height, width, height)
	}
	groups, n := width/6, width%6
	yPlane := unsafe.Slice((*uint16)(unsafe.Pointer(&yuv[0])), ySize)
	cbPlane := unsafe.Slice((*uint16)(unsafe.Pointer(&yuv[ySize*2])), cSize)
	crPlane := unsafe.Slice((*uint16)(unsafe.Pointer(&yuv[(ySize+cSize)*2])), cSize)
	for row := 0; row < height; row++ {
		yOff, cOff := row*width, row*chromaW
		v210Row := out[row*stride:]
		v210PackRow10(v210Row, yPlane[yOff:], cbPlane[cOff:], crPlane[cOff:], groups)
		packV210Tail10(v210Row[groups*16:], yPlane[yOff+groups*6:], cbPlane[cOff+groups*3:], crPlane[cOff+groups*3:], n)
	}
	return nil
}
