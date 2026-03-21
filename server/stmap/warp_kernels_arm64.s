#include "textflag.h"

// ARM64 ST map bilinear warp kernel with software prefetching.
//
// Optimizations:
// 1. int32 LUTs (halves LUT cache footprint vs int64)
// 2. PRFM PLDL1KEEP prefetch of next pixel's source rows
// 3. row1off = row0off + srcW (eliminates one MUL per pixel)
// 4. Bounds-check elimination via manual clamping
//
// func warpBilinearRow(dst, src *byte, srcW, srcH, n int, lutX, lutY *int32)

TEXT ·warpBilinearRow(SB), NOSPLIT, $0-56
	MOVD dst+0(FP), R0
	MOVD src+8(FP), R1
	MOVD srcW+16(FP), R2
	MOVD srcH+24(FP), R20
	MOVD n+32(FP), R3
	MOVD lutX+40(FP), R4
	MOVD lutY+48(FP), R5

	CMP  $0, R3
	BLE  warp_done

	SUB  $1, R2, R6            // R6 = srcW - 1
	SUB  $1, R20, R7           // R7 = srcH - 1
	MOVD $0xFFFF, R8
	MOVD $255, R9

warp_loop:
	// ---- Load int32 LUT entries (sign-extended) ----
	MOVW (R4), R10             // sx (int32 sign-extended to 64-bit)
	MOVW (R5), R11             // sy

	// ---- Prefetch next LUT entries into L1 ----
	PRFM 4(R4), PLDL1KEEP
	PRFM 4(R5), PLDL1KEEP

	// ---- Clamp sx ----
	CMP  $0, R10
	BGE  sx_ok
	MOVD $0, R10
sx_ok:
	LSL  $16, R6, R12
	CMP  R12, R10
	CSEL LO, R10, R12, R10

	// ---- Clamp sy ----
	CMP  $0, R11
	BGE  sy_ok
	MOVD $0, R11
sy_ok:
	LSL  $16, R7, R12
	CMP  R12, R11
	CSEL LO, R11, R12, R11

	// ---- Split sx → ix (R13), fx (R14) ----
	ASR  $16, R10, R13
	AND  R8, R10, R14

	// ---- Split sy → iy (R15), fy (R16) ----
	ASR  $16, R11, R15
	AND  R8, R11, R16

	// ---- Clamp ix1 (R17) ----
	ADD  $1, R13, R17
	CMP  R6, R17
	CSEL LO, R17, R6, R17

	// ---- Row base addresses ----
	MUL  R2, R15, R15          // row0off = iy * srcW
	ADD  R2, R15, R19          // row1off = row0off + srcW (saves one MUL)

	// ---- Prefetch next pixel's source rows ----
	// Read next sy, compute row address, prefetch. This is the key
	// optimization: source pixel reads are random L2/L3 misses.
	// Prefetching while we compute the current pixel hides latency.
	CMP  $1, R3
	BLE  skip_prefetch
	MOVW 4(R5), R20            // next sy (int32)
	CMP  $0, R20
	BGE  next_sy_pos
	MOVD $0, R20
next_sy_pos:
	LSL  $16, R7, R12
	CMP  R12, R20
	CSEL LO, R20, R12, R20
	ASR  $16, R20, R20         // next iy
	MUL  R2, R20, R20          // next row0off
	ADD  R1, R20, R20          // next row0 addr
	PRFM (R20), PLDL1KEEP     // prefetch source data for next pixel
skip_prefetch:

	// ---- Source base addresses ----
	ADD  R1, R15, R15          // R15 = &src[row0off]
	ADD  R1, R19, R19          // R19 = &src[row1off]

	// ---- Load 4 pixels ----
	MOVBU (R15)(R13), R10      // p00
	MOVBU (R15)(R17), R11      // p10
	MOVBU (R19)(R13), R12      // p01
	MOVBU (R19)(R17), R13      // p11

	// ---- invFx ----
	MOVD $65536, R15
	SUB  R14, R15, R15

	// ---- top = (p00*invFx + p10*fx + 32768) >> 16 ----
	MUL  R15, R10, R10
	MUL  R14, R11, R11
	ADD  R11, R10, R10
	ADD  $32768, R10, R10
	LSR  $16, R10, R10

	// ---- bot = (p01*invFx + p11*fx + 32768) >> 16 ----
	MUL  R15, R12, R12
	MUL  R14, R13, R13
	ADD  R13, R12, R12
	ADD  $32768, R12, R12
	LSR  $16, R12, R12

	// ---- invFy ----
	MOVD $65536, R15
	SUB  R16, R15, R15

	// ---- val = (top*invFy + bot*fy + 32768) >> 16 ----
	MUL  R15, R10, R10
	MUL  R16, R12, R12
	ADD  R12, R10, R10
	ADD  $32768, R10, R10
	LSR  $16, R10, R10

	// ---- Clamp ----
	CMP  R9, R10
	CSEL HI, R9, R10, R10

	// ---- Store ----
	MOVB R10, (R0)

	// ---- Advance (int32 = 4 bytes) ----
	ADD  $1, R0, R0
	ADD  $4, R4, R4
	ADD  $4, R5, R5
	SUB  $1, R3, R3
	CBNZ R3, warp_loop

warp_done:
	RET
