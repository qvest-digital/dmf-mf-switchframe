#include "textflag.h"

// AMD64 ST map bilinear warp kernel with software prefetching.
//
// Optimizations:
// 1. int32 LUTs (halves LUT cache footprint vs int64)
// 2. PREFETCHT0 of next pixel's source rows (hides L2/L3 miss latency)
// 3. row1off = row0off + srcW (eliminates one IMULQ per pixel)
// 4. Bounds-check elimination via manual clamping
//
// func warpBilinearRow(dst, src *byte, srcW, srcH, n int, lutX, lutY *int32)
//
// Register plan:
//   DI  = dst (advances)
//   SI  = src base (constant)
//   R8  = srcW (constant)
//   CX  = loop counter
//   R10 = lutX (advances)
//   R11 = lutY (advances)
//   R12 = srcW - 1 (constant)
//   R13 = srcH - 1 (constant)
//   AX, BX, DX, R9, R14, R15 = scratch

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

	MOVQ  R8, R12
	SUBQ  $1, R12              // R12 = srcW - 1
	MOVQ  R9, R13
	SUBQ  $1, R13              // R13 = srcH - 1

warp_loop:
	// ---- Load int32 LUT entries (sign-extended to 64-bit) ----
	MOVLQSX (R10), AX          // sx = lutX[i] (sign-extend int32→int64)
	MOVLQSX (R11), BX          // sy = lutY[i]

	// ---- Clamp sx to [0, (srcW-1)<<16] ----
	TESTQ AX, AX
	JGE   sx_ok
	XORQ  AX, AX
sx_ok:
	MOVQ  R12, R14
	SHLQ  $16, R14             // maxX
	CMPQ  AX, R14
	CMOVQGT R14, AX

	// ---- Clamp sy to [0, (srcH-1)<<16] ----
	TESTQ BX, BX
	JGE   sy_ok
	XORQ  BX, BX
sy_ok:
	MOVQ  R13, R14
	SHLQ  $16, R14             // maxY
	CMPQ  BX, R14
	CMOVQGT R14, BX

	// ---- Split sx → ix (R14), fx (AX) ----
	MOVQ  AX, R14
	SHRQ  $16, R14
	ANDQ  $0xFFFF, AX

	// ---- Split sy → iy (R15), fy → stack ----
	MOVQ  BX, R15
	SHRQ  $16, R15
	ANDQ  $0xFFFF, BX
	MOVQ  BX, (SP)             // save fy

	// ---- Clamp ix1, iy1 ----
	MOVQ  R14, R9
	INCQ  R9
	CMPQ  R9, R12
	CMOVQGT R12, R9

	// iy1 not needed for address — use row1off = row0off + srcW

	// ---- Compute row0off = iy * srcW ----
	IMULQ R8, R15              // R15 = row0off

	// ---- row1off = row0off + srcW (saves one IMULQ) ----
	MOVQ  R15, DX
	ADDQ  R8, DX               // DX = row1off

	// ---- Prefetch next pixel's source row ----
	CMPQ  CX, $1
	JLE   skip_prefetch
	MOVLQSX 4(R11), BX         // next sy (int32, sign-extended)
	TESTQ BX, BX
	JGE   next_sy_ok
	XORQ  BX, BX
next_sy_ok:
	MOVQ  R13, R9
	SHLQ  $16, R9
	CMPQ  BX, R9
	CMOVQGT R9, BX
	SHRQ  $16, BX              // next iy
	IMULQ R8, BX               // next row0off
	ADDQ  SI, BX               // next row0 addr
	PREFETCHT0 (BX)            // prefetch into L1
skip_prefetch:
	// Restore R9 = ix1 (may have been clobbered by prefetch path)
	MOVQ  R14, R9
	INCQ  R9
	CMPQ  R9, R12
	CMOVQGT R12, R9

	// ---- Source base addresses ----
	ADDQ  SI, R15              // R15 = &src[row0off]
	ADDQ  SI, DX               // DX  = &src[row1off]

	// ---- Load 4 source pixels ----
	MOVBLZX (R15)(R14*1), BX   // p00
	MOVBLZX (R15)(R9*1), R15   // p10
	MOVBLZX (DX)(R14*1), R14   // p01
	MOVBLZX (DX)(R9*1), R9     // p11

	// ---- invFx ----
	MOVQ  $65536, DX
	SUBQ  AX, DX

	// ---- top = (p00*invFx + p10*fx + 32768) >> 16 ----
	IMULQ DX, BX
	IMULQ AX, R15
	ADDQ  R15, BX
	ADDQ  $32768, BX
	SHRQ  $16, BX

	// ---- bot = (p01*invFx + p11*fx + 32768) >> 16 ----
	IMULQ DX, R14
	IMULQ AX, R9
	ADDQ  R9, R14
	ADDQ  $32768, R14
	SHRQ  $16, R14

	// ---- Reload fy, compute invFy ----
	MOVQ  (SP), AX
	MOVQ  $65536, DX
	SUBQ  AX, DX

	// ---- val = (top*invFy + bot*fy + 32768) >> 16 ----
	IMULQ DX, BX
	IMULQ AX, R14
	ADDQ  R14, BX
	ADDQ  $32768, BX
	SHRQ  $16, BX

	// ---- Clamp to 0-255 ----
	CMPQ  BX, $255
	JLE   val_ok
	MOVQ  $255, BX
val_ok:

	// ---- Store ----
	MOVB  BX, (DI)

	// ---- Advance (int32 = 4 bytes per LUT entry) ----
	INCQ  DI
	ADDQ  $4, R10              // lutX++ (int32)
	ADDQ  $4, R11              // lutY++ (int32)
	DECQ  CX
	JNZ   warp_loop

warp_done:
	RET
