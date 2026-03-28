//go:build arm64

package mxl

import "unsafe"

// v210UnpackRow10 extracts Y, Cb, Cr from V210 packed data for one row (lossless 10-bit).
// Each group of 16 bytes (4 uint32 words) produces 6 Y + 3 Cb + 3 Cr uint16 samples.
// NEON-accelerated on arm64.
func v210UnpackRow10(yOut, cbOut, crOut []uint16, v210In []byte, groups int) {
	if groups <= 0 {
		return
	}
	v210UnpackRow10Ptr(
		unsafe.SliceData(yOut),
		unsafe.SliceData(cbOut),
		unsafe.SliceData(crOut),
		unsafe.SliceData(v210In),
		groups,
	)
}

// v210PackRow10 packs Y, Cb, Cr uint16 samples into V210 format for one row (lossless 10-bit).
// Each group of 6 Y + 3 Cb + 3 Cr produces 16 bytes (4 uint32 words).
// NEON-accelerated on arm64.
func v210PackRow10(v210Out []byte, yIn, cbIn, crIn []uint16, groups int) {
	if groups <= 0 {
		return
	}
	v210PackRow10Ptr(
		unsafe.SliceData(v210Out),
		unsafe.SliceData(yIn),
		unsafe.SliceData(cbIn),
		unsafe.SliceData(crIn),
		groups,
	)
}

// v210UnpackRow10Ptr is the NEON assembly implementation.
//
//go:noescape
func v210UnpackRow10Ptr(yOut, cbOut, crOut *uint16, v210In *byte, groups int)

// v210PackRow10Ptr is the NEON assembly implementation.
//
//go:noescape
func v210PackRow10Ptr(v210Out *byte, yIn, cbIn, crIn *uint16, groups int)
