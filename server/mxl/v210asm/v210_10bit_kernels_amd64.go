//go:build amd64

package v210asm

// V210UnpackRow10 extracts Y, Cb, Cr from V210 packed data for one row
// (lossless 10-bit). Each group of 16 bytes (4 uint32 words) produces
// 6 Y + 3 Cb + 3 Cr uint16 samples. Assembly implementation (scalar, no bounds checks).
//
//go:noescape
func V210UnpackRow10(yOut, cbOut, crOut *uint16, v210In *byte, groups int)

// V210PackRow10 packs Y, Cb, Cr uint16 samples into V210 for one row
// (lossless 10-bit). Each group of 6 Y + 3 Cb + 3 Cr produces 16 bytes.
// Assembly implementation (scalar, no bounds checks).
//
//go:noescape
func V210PackRow10(v210Out *byte, yIn, cbIn, crIn *uint16, groups int)
