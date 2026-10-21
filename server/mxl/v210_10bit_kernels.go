package mxl

import (
	"unsafe"

	"github.com/zsiec/switchframe/server/mxl/v210asm"
)

// v210UnpackRow10 extracts Y, Cb, Cr from V210 packed data for one row
// (lossless 10-bit). Each group of 16 bytes (4 uint32 words) produces
// 6 Y + 3 Cb + 3 Cr uint16 samples.
//
// The kernels live in v210asm because this package uses cgo under the mxl
// build tag, and Go refuses assembly files in a cgo package.
func v210UnpackRow10(yOut, cbOut, crOut []uint16, v210In []byte, groups int) {
	if groups <= 0 {
		return
	}
	v210asm.V210UnpackRow10(
		unsafe.SliceData(yOut),
		unsafe.SliceData(cbOut),
		unsafe.SliceData(crOut),
		unsafe.SliceData(v210In),
		groups,
	)
}

// v210PackRow10 packs Y, Cb, Cr uint16 samples into V210 format for one row
// (lossless 10-bit). Each group of 6 Y + 3 Cb + 3 Cr produces 16 bytes.
func v210PackRow10(v210Out []byte, yIn, cbIn, crIn []uint16, groups int) {
	if groups <= 0 {
		return
	}
	v210asm.V210PackRow10(
		unsafe.SliceData(v210Out),
		unsafe.SliceData(yIn),
		unsafe.SliceData(cbIn),
		unsafe.SliceData(crIn),
		groups,
	)
}
