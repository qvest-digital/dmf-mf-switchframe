//go:build amd64

package mxl

import "golang.org/x/sys/cpu"

// avx2Available is set at init time if the CPU supports AVX2.
// Assembly routines branch to AVX2 or SSE2 path based on this flag.
var avx2Available = cpu.X86.HasAVX2

// chromaVAvg computes dst[i] = (top[i] + bot[i] + 1) >> 1 for n bytes.
// AVX2 path uses VPAVGB (32 bytes/iter), SSE2 uses PAVGB (16 bytes/iter).
//
//go:noescape
func chromaVAvg(dst, top, bot *byte, n int)

// v210UnpackRow extracts Y, Cb, Cr from V210 packed data for one row.
// Each group of 16 bytes (4 uint32 words) produces 6 Y + 3 Cb + 3 Cr bytes.
//
//go:noescape
func v210UnpackRow(yOut, cbOut, crOut, v210In *byte, groups int)

// v210PackRow packs Y, Cb, Cr bytes into V210 format for one row.
// Each group of 6 Y + 3 Cb + 3 Cr bytes produces 16 bytes (4 uint32 words).
//
//go:noescape
func v210PackRow(v210Out, yIn, cbIn, crIn *byte, groups int)
