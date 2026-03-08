//go:build amd64

package v210asm

import "golang.org/x/sys/cpu"

// avx2Available is set at init time if the CPU supports AVX2.
// Assembly routines branch to AVX2 or SSE2 path based on this flag.
// Referenced from assembly via ·avx2Available(SB).
var avx2Available = cpu.X86.HasAVX2 //nolint:unused // used in v210_kernels_amd64.s

// ChromaVAvg computes dst[i] = (top[i] + bot[i] + 1) >> 1 for n bytes.
// AVX2 path uses VPAVGB (32 bytes/iter), SSE2 uses PAVGB (16 bytes/iter).
//
//go:noescape
func ChromaVAvg(dst, top, bot *byte, n int)

// V210UnpackRow extracts Y, Cb, Cr from V210 packed data for one row.
// Each group of 16 bytes (4 uint32 words) produces 6 Y + 3 Cb + 3 Cr bytes.
//
//go:noescape
func V210UnpackRow(yOut, cbOut, crOut, v210In *byte, groups int)

// V210PackRow packs Y, Cb, Cr bytes into V210 format for one row.
// Each group of 6 Y + 3 Cb + 3 Cr bytes produces 16 bytes (4 uint32 words).
//
//go:noescape
func V210PackRow(v210Out, yIn, cbIn, crIn *byte, groups int)
