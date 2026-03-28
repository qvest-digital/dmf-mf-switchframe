#include "textflag.h"

// AMD64 ST map bilinear warp kernel for 10-bit planes with software prefetching.
//
// Each sample is uint16 LE (2 bytes). The vertical bilinear accumulation
// needs 64-bit arithmetic:
//   top = p00*(65536-fx) + p10*fx           // max ~67M, fits 64-bit
//   bot = p01*(65536-fx) + p11*fx           // max ~67M, fits 64-bit
//   val = (top*(65536-fy) + bot*fy + (1<<31)) >> 32
//
// Optimizations:
// 1. int32 LUTs (halves LUT cache footprint vs int64)
// 2. PREFETCHT0 of next pixel's source rows (hides L2/L3 miss latency)
// 3. row1off = row0off + srcStride (eliminates one IMULQ per pixel)
// 4. Bounds-check elimination via manual clamping
//
// func warpBilinearRow10(dst, src *byte, srcW, srcH, n int, lutX, lutY *int32)
//
// Register plan:
//   DI  = dst (advances by 2 per pixel)
//   SI  = src base (constant)
//   R8  = srcW (constant)
//   CX  = loop counter
//   R10 = lutX (advances by 4)
//   R11 = lutY (advances by 4)
//   R12 = srcW - 1 (constant)
//   R13 = srcH - 1 (constant)
//   AX, BX, DX, R9, R14, R15 = scratch
//   Stack: 0(SP) = fy, 8(SP) = srcStride

TEXT ·warpBilinearRow10(SB), NOSPLIT, $16-56
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ srcW+16(FP), R8
	MOVQ srcH+24(FP), R9
	MOVQ n+32(FP), CX
	MOVQ lutX+40(FP), R10
	MOVQ lutY+48(FP), R11

	TESTQ CX, CX
	JLE   warp10_done

	MOVQ  R8, R12
	SUBQ  $1, R12              // R12 = srcW - 1
	MOVQ  R9, R13
	SUBQ  $1, R13              // R13 = srcH - 1

	// srcStride = srcW * 2 (bytes per row for uint16 samples)
	MOVQ  R8, R14
	SHLQ  $1, R14
	MOVQ  R14, 8(SP)           // save srcStride to stack

warp10_loop:
	// ---- Load int32 LUT entries (sign-extended to 64-bit) ----
	MOVLQSX (R10), AX          // sx = lutX[i] (sign-extend int32 to int64)
	MOVLQSX (R11), BX          // sy = lutY[i]

	// ---- Clamp sx to [0, (srcW-1)<<16] ----
	TESTQ AX, AX
	JGE   warp10_sx_ok
	XORQ  AX, AX
warp10_sx_ok:
	MOVQ  R12, R14
	SHLQ  $16, R14             // maxX
	CMPQ  AX, R14
	CMOVQGT R14, AX

	// ---- Clamp sy to [0, (srcH-1)<<16] ----
	TESTQ BX, BX
	JGE   warp10_sy_ok
	XORQ  BX, BX
warp10_sy_ok:
	MOVQ  R13, R14
	SHLQ  $16, R14             // maxY
	CMPQ  BX, R14
	CMOVQGT R14, BX

	// ---- Split sx -> ix (R14), fx (AX) ----
	MOVQ  AX, R14
	SHRQ  $16, R14
	ANDQ  $0xFFFF, AX

	// ---- Split sy -> iy (R15), fy -> stack ----
	MOVQ  BX, R15
	SHRQ  $16, R15
	ANDQ  $0xFFFF, BX
	MOVQ  BX, (SP)             // save fy

	// ---- Clamp ix1 ----
	MOVQ  R14, R9
	INCQ  R9
	CMPQ  R9, R12
	CMOVQGT R12, R9

	// ---- Compute row0off = iy * srcStride (bytes) ----
	MOVQ  8(SP), DX            // DX = srcStride
	IMULQ DX, R15              // R15 = row0off (bytes)

	// ---- row1off = row0off + srcStride ----
	MOVQ  R15, DX
	ADDQ  8(SP), DX            // DX = row1off

	// ---- Prefetch next pixel's source row ----
	CMPQ  CX, $1
	JLE   warp10_skip_prefetch
	MOVLQSX 4(R11), BX         // next sy (int32, sign-extended)
	TESTQ BX, BX
	JGE   warp10_next_sy_ok
	XORQ  BX, BX
warp10_next_sy_ok:
	MOVQ  R13, R9
	SHLQ  $16, R9
	CMPQ  BX, R9
	CMOVQGT R9, BX
	SHRQ  $16, BX              // next iy
	IMULQ 8(SP), BX            // next row0off (bytes), using srcStride from stack
	ADDQ  SI, BX               // next row0 addr
	PREFETCHT0 (BX)            // prefetch into L1
warp10_skip_prefetch:
	// Restore R9 = ix1 (may have been clobbered by prefetch path)
	MOVQ  R14, R9
	INCQ  R9
	CMPQ  R9, R12
	CMOVQGT R12, R9

	// ---- Source base addresses ----
	ADDQ  SI, R15              // R15 = &src[row0off]
	ADDQ  SI, DX               // DX  = &src[row1off]

	// ---- Load 4 source uint16 LE pixels ----
	// Use *2 scale for uint16 byte addressing.
	MOVWLZX (R15)(R14*2), BX   // p00 (uint16, zero-extended to 64-bit)
	MOVWLZX (R15)(R9*2), R15   // p10
	MOVWLZX (DX)(R14*2), R14   // p01
	MOVWLZX (DX)(R9*2), R9     // p11

	// ---- invFx = 65536 - fx ----
	MOVQ  $65536, DX
	SUBQ  AX, DX               // DX = invFx

	// ---- top = p00*invFx + p10*fx ----
	IMULQ DX, BX               // p00 * invFx
	IMULQ AX, R15              // p10 * fx
	ADDQ  R15, BX              // BX = top

	// ---- bot = p01*invFx + p11*fx ----
	IMULQ DX, R14              // p01 * invFx
	IMULQ AX, R9               // p11 * fx
	ADDQ  R9, R14              // R14 = bot

	// ---- Reload fy, compute invFy ----
	MOVQ  (SP), AX             // AX = fy
	MOVQ  $65536, DX
	SUBQ  AX, DX               // DX = invFy

	// ---- val = (top*invFy + bot*fy + (1<<31)) >> 32 ----
	// top (BX) max ~67M * invFy max 65536 = ~4.4T, fits int64
	IMULQ DX, BX               // top * invFy
	IMULQ AX, R14              // bot * fy
	ADDQ  R14, BX              // top*invFy + bot*fy
	MOVQ  $0x80000000, DX      // 1<<31
	ADDQ  DX, BX               // + rounding
	SHRQ  $32, BX              // >> 32

	// ---- Clamp to [0, 1023] ----
	CMPQ  BX, $1023
	JLE   warp10_val_ok
	MOVQ  $1023, BX
warp10_val_ok:

	// ---- Store uint16 LE ----
	MOVW  BX, (DI)

	// ---- Advance ----
	ADDQ  $2, DI               // dst += 2 bytes (uint16)
	ADDQ  $4, R10              // lutX += 4 bytes (int32)
	ADDQ  $4, R11              // lutY += 4 bytes (int32)
	DECQ  CX
	JNZ   warp10_loop

warp10_done:
	RET
