#include "textflag.h"

// ARM64 ST map bilinear warp kernel for 10-bit planes with software prefetching.
//
// Each sample is uint16 LE (2 bytes). The bilinear vertical accumulation
// uses 64-bit arithmetic to avoid overflow:
//   top = p00*(65536-fx) + p10*fx           // max ~67M, fits int64
//   bot = p01*(65536-fx) + p11*fx           // max ~67M, fits int64
//   val = (top*(65536-fy) + bot*fy + (1<<31)) >> 32
//
// Optimizations:
// 1. int32 LUTs (halves LUT cache footprint vs int64)
// 2. PRFM PLDL1KEEP prefetch of next pixel's source rows
// 3. row1off = row0off + srcStride (eliminates one MUL per pixel)
// 4. Bounds-check elimination via manual clamping
//
// Register plan:
//   R0  = dst (advances by 2 per pixel)
//   R1  = src base (constant)
//   R2  = srcW (constant)
//   R3  = loop counter (decrements)
//   R4  = lutX (advances by 4 per pixel)
//   R5  = lutY (advances by 4 per pixel)
//   R6  = srcW - 1 (constant)
//   R7  = srcH - 1 (constant)
//   R8  = 0xFFFF mask (constant)
//   R9  = 1023 clamp value (constant)
//   R21 = srcStride = srcW * 2 bytes (constant)
//   R10-R20, R22-R25 = scratch
//
// func warpBilinearRow10(dst, src *byte, srcW, srcH, n int, lutX, lutY *int32)

TEXT ·warpBilinearRow10(SB), NOSPLIT, $0-56
	MOVD dst+0(FP), R0
	MOVD src+8(FP), R1
	MOVD srcW+16(FP), R2
	MOVD srcH+24(FP), R20
	MOVD n+32(FP), R3
	MOVD lutX+40(FP), R4
	MOVD lutY+48(FP), R5

	CMP  $0, R3
	BLE  warp10_done

	SUB  $1, R2, R6            // R6 = srcW - 1
	SUB  $1, R20, R7           // R7 = srcH - 1
	MOVD $0xFFFF, R8
	MOVD $1023, R9
	LSL  $1, R2, R21           // R21 = srcStride = srcW * 2 (bytes per row)

warp10_loop:
	// ---- Load int32 LUT entries (sign-extended to 64-bit) ----
	MOVW (R4), R10             // sx (int32 sign-extended)
	MOVW (R5), R11             // sy

	// ---- Prefetch next LUT entries into L1 ----
	PRFM 4(R4), PLDL1KEEP
	PRFM 4(R5), PLDL1KEEP

	// ---- Clamp sx to [0, (srcW-1)<<16] ----
	CMP  $0, R10
	BGE  warp10_sx_ok
	MOVD $0, R10
warp10_sx_ok:
	LSL  $16, R6, R12
	CMP  R12, R10
	CSEL LO, R10, R12, R10

	// ---- Clamp sy to [0, (srcH-1)<<16] ----
	CMP  $0, R11
	BGE  warp10_sy_ok
	MOVD $0, R11
warp10_sy_ok:
	LSL  $16, R7, R12
	CMP  R12, R11
	CSEL LO, R11, R12, R11

	// ---- Split sx -> ix (R13), fx (R14) ----
	ASR  $16, R10, R13         // ix
	AND  R8, R10, R14          // fx

	// ---- Split sy -> iy (R15), fy (R16) ----
	ASR  $16, R11, R15         // iy
	AND  R8, R11, R16          // fy

	// ---- Clamp ix1 (R17) ----
	ADD  $1, R13, R17
	CMP  R6, R17
	CSEL LO, R17, R6, R17

	// ---- Row base addresses (byte offsets) ----
	MUL  R21, R15, R15         // R15 = iy * srcStride (bytes)
	ADD  R21, R15, R19         // R19 = row1off = row0off + srcStride

	// ---- Prefetch next pixel's source rows ----
	CMP  $1, R3
	BLE  warp10_skip_prefetch
	MOVW 4(R5), R20            // next sy (int32)
	CMP  $0, R20
	BGE  warp10_next_sy_pos
	MOVD $0, R20
warp10_next_sy_pos:
	LSL  $16, R7, R12
	CMP  R12, R20
	CSEL LO, R20, R12, R20
	ASR  $16, R20, R20         // next iy
	MUL  R21, R20, R20         // next row0off (bytes)
	ADD  R1, R20, R20          // next row0 addr
	PRFM (R20), PLDL1KEEP     // prefetch source data for next pixel
warp10_skip_prefetch:

	// ---- Source base addresses ----
	ADD  R1, R15, R15          // R15 = &src[row0off]
	ADD  R1, R19, R19          // R19 = &src[row1off]

	// ---- Compute byte offsets for ix and ix1 ----
	LSL  $1, R13, R22          // R22 = ix * 2  (byte offset)
	LSL  $1, R17, R23          // R23 = ix1 * 2 (byte offset)

	// ---- Load 4 uint16 LE pixels ----
	MOVHU (R15)(R22), R10      // p00 (uint16, zero-extended to 64-bit)
	MOVHU (R15)(R23), R11      // p10
	MOVHU (R19)(R22), R12      // p01
	MOVHU (R19)(R23), R13      // p11

	// ---- invFx = 65536 - fx ----
	MOVD $65536, R15
	SUB  R14, R15, R15         // R15 = invFx

	// ---- top = p00*invFx + p10*fx (int64, max ~67M, fits 64-bit) ----
	MUL  R15, R10, R10         // p00 * invFx
	MUL  R14, R11, R11         // p10 * fx
	ADD  R11, R10, R10         // R10 = top

	// ---- bot = p01*invFx + p11*fx ----
	MUL  R15, R12, R12         // p01 * invFx
	MUL  R14, R13, R13         // p11 * fx
	ADD  R13, R12, R12         // R12 = bot

	// ---- invFy = 65536 - fy ----
	MOVD $65536, R15
	SUB  R16, R15, R15         // R15 = invFy

	// ---- val = (top*invFy + bot*fy + (1<<31)) >> 32 ----
	// top and bot are up to ~67M (1023 * 65536). Multiplied by 65536 = ~4.4T.
	// This fits in 64-bit signed (max 2^63).
	MUL  R15, R10, R10         // top * invFy
	MUL  R16, R12, R12         // bot * fy
	ADD  R12, R10, R10         // top*invFy + bot*fy
	MOVD $1, R11
	LSL  $31, R11, R11         // R11 = 1<<31
	ADD  R11, R10, R10         // + rounding
	LSR  $32, R10, R10         // >> 32

	// ---- Clamp to [0, 1023] ----
	// Value should never be negative after proper clamping of inputs,
	// but clamp anyway for safety.
	CMP  R9, R10
	CSEL HI, R9, R10, R10     // if R10 > 1023, R10 = 1023

	// ---- Store uint16 LE ----
	MOVH R10, (R0)

	// ---- Advance ----
	ADD  $2, R0, R0            // dst += 2 bytes (uint16)
	ADD  $4, R4, R4            // lutX += 4 bytes (int32)
	ADD  $4, R5, R5            // lutY += 4 bytes (int32)
	SUB  $1, R3, R3
	CBNZ R3, warp10_loop

warp10_done:
	RET
