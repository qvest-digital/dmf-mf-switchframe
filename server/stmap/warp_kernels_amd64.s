#include "textflag.h"

// AMD64 ST map bilinear warp kernel.
// Scalar gather + scalar interpolation per pixel. The irregular 2D gather
// pattern prevents SIMD vectorization. Assembly eliminates Go bounds checks
// and uses register-pinned loop constants for ~2x speedup over Go.
//
// func warpBilinearRow(dst, src *byte, srcW, srcH, n int, lutX, lutY *int64)
//
// Register plan (inside loop):
//   DI  = dst pointer (advances)
//   SI  = src base pointer (constant)
//   R8  = srcW (constant)
//   CX  = loop counter (decrements)
//   R10 = lutX pointer (advances)
//   R11 = lutY pointer (advances)
//   R12 = srcW - 1 (constant, column clamp)
//   R13 = srcH - 1 (constant, row clamp)
//   AX, BX, DX, R9, R14, R15 = scratch (per-pixel)

TEXT ·warpBilinearRow(SB), NOSPLIT, $8-56
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ srcW+16(FP), R8
	MOVQ srcH+24(FP), R9
	MOVQ n+32(FP), CX
	MOVQ lutX+40(FP), R10
	MOVQ lutY+48(FP), R11

	TESTQ CX, CX
	JLE   warp_done

	// Precompute clamp limits (constant across loop)
	MOVQ  R8, R12
	SUBQ  $1, R12             // R12 = srcW - 1
	MOVQ  R9, R13
	SUBQ  $1, R13             // R13 = srcH - 1

warp_loop:
	// ---- Load and clamp sx ----
	MOVQ  (R10), AX           // sx = lutX[i]
	TESTQ AX, AX
	CMOVQLT CX, AX            // (trick: if neg, we fix below)
	JGE   sx_ok
	XORQ  AX, AX              // sx = 0 if negative
sx_ok:
	MOVQ  R12, R14
	SHLQ  $16, R14            // maxX = (srcW-1) << 16
	CMPQ  AX, R14
	CMOVQGT R14, AX

	// ---- Load and clamp sy ----
	MOVQ  (R11), BX           // sy = lutY[i]
	TESTQ BX, BX
	JGE   sy_ok
	XORQ  BX, BX              // sy = 0 if negative
sy_ok:
	MOVQ  R13, R14
	SHLQ  $16, R14            // maxY = (srcH-1) << 16
	CMPQ  BX, R14
	CMOVQGT R14, BX

	// ---- Split sx → ix (R14), fx (AX) ----
	MOVQ  AX, R14
	SHRQ  $16, R14            // ix
	ANDQ  $0xFFFF, AX         // fx

	// ---- Split sy → iy (R15), fy → stack ----
	MOVQ  BX, R15
	SHRQ  $16, R15            // iy
	ANDQ  $0xFFFF, BX         // fy
	MOVQ  BX, (SP)            // save fy on stack (need BX for pixels)

	// ---- Clamp ix1, iy1 ----
	MOVQ  R14, R9
	INCQ  R9                  // ix1 = ix + 1
	CMPQ  R9, R12
	CMOVQGT R12, R9           // clamp ix1

	MOVQ  R15, DX
	INCQ  DX                  // iy1 = iy + 1
	CMPQ  DX, R13
	CMOVQGT R13, DX           // clamp iy1

	// ---- Compute row base addresses ----
	IMULQ R8, R15             // row0off = iy * srcW
	IMULQ R8, DX              // row1off = iy1 * srcW
	ADDQ  SI, R15             // R15 = &src[row0off]
	ADDQ  SI, DX              // DX  = &src[row1off]

	// ---- Load 4 source pixels ----
	MOVBLZX (R15)(R14*1), BX  // p00 = row0[ix]
	MOVBLZX (R15)(R9*1), R15  // p10 = row0[ix1]  (clobbers R15, done with it)
	MOVBLZX (DX)(R14*1), R14  // p01 = row1[ix]   (clobbers R14, done with ix)
	MOVBLZX (DX)(R9*1), R9    // p11 = row1[ix1]  (clobbers R9, done with ix1)

	// Now: BX=p00, R15=p10, R14=p01, R9=p11, AX=fx

	// ---- invFx ----
	MOVQ  $65536, DX
	SUBQ  AX, DX              // DX = invFx = 65536 - fx

	// ---- top = (p00*invFx + p10*fx + 32768) >> 16 ----
	IMULQ DX, BX              // p00 * invFx
	IMULQ AX, R15             // p10 * fx
	ADDQ  R15, BX
	ADDQ  $32768, BX
	SHRQ  $16, BX             // BX = top

	// ---- bot = (p01*invFx + p11*fx + 32768) >> 16 ----
	IMULQ DX, R14             // p01 * invFx
	IMULQ AX, R9              // p11 * fx
	ADDQ  R9, R14
	ADDQ  $32768, R14
	SHRQ  $16, R14            // R14 = bot

	// ---- Reload fy, compute invFy ----
	MOVQ  (SP), AX            // AX = fy (from stack)
	MOVQ  $65536, DX
	SUBQ  AX, DX              // DX = invFy = 65536 - fy

	// ---- val = (top*invFy + bot*fy + 32768) >> 16 ----
	IMULQ DX, BX              // top * invFy
	IMULQ AX, R14             // bot * fy
	ADDQ  R14, BX
	ADDQ  $32768, BX
	SHRQ  $16, BX             // BX = val

	// ---- Clamp to 0-255 ----
	CMPQ  BX, $255
	JLE   val_ok
	MOVQ  $255, BX
val_ok:

	// ---- Store result ----
	MOVB  BX, (DI)

	// ---- Advance pointers ----
	INCQ  DI                  // dst++
	ADDQ  $8, R10             // lutX++
	ADDQ  $8, R11             // lutY++
	DECQ  CX
	JNZ   warp_loop

warp_done:
	RET
