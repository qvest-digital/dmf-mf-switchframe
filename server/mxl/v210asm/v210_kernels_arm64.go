//go:build arm64

package v210asm

// ChromaVAvg computes dst[i] = (top[i] + bot[i] + 1) >> 1 for n bytes.
// NEON implementation uses URHADD for single-instruction rounding average.
// Processes 16 bytes per iteration.
//
//go:noescape
func ChromaVAvg(dst, top, bot *byte, n int)

// V210UnpackRow extracts Y, Cb, Cr from V210 packed data for one row.
// Each group of 16 bytes (4 uint32 words) produces 6 Y + 3 Cb + 3 Cr bytes.
// NEON implementation processes one group per iteration with bitfield extraction.
//
//go:noescape
func V210UnpackRow(yOut, cbOut, crOut, v210In *byte, groups int)

// V210PackRow packs Y, Cb, Cr bytes into V210 format for one row.
// Each group of 6 Y + 3 Cb + 3 Cr bytes produces 16 bytes (4 uint32 words).
// NEON implementation processes one group per iteration.
//
//go:noescape
func V210PackRow(v210Out, yIn, cbIn, crIn *byte, groups int)
