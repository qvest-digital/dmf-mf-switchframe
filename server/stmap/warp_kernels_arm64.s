#include "textflag.h"

// ARM64 ST map bilinear warp kernel.
// Scalar gather + scalar interpolation per pixel. Same algorithm as the
// amd64 version. Assembly eliminates Go bounds checks.
//
// func warpBilinearRow(dst, src *byte, srcW, srcH, n int, lutX, lutY *int64)
//
// Register plan:
//   R0  = dst pointer (advances)
//   R1  = src base pointer (constant)
//   R2  = srcW (constant)
//   R3  = loop counter (decrements)
//   R4  = lutX pointer (advances)
//   R5  = lutY pointer (advances)
//   R6  = srcW - 1 (constant)
//   R7  = srcH - 1 (constant)
//   R8  = 0xFFFF mask (constant)
//   R9  = 255 clamp (constant)
//   R10-R20 = scratch

TEXT ·warpBilinearRow(SB), NOSPLIT, $0-56
	MOVD dst+0(FP), R0
	MOVD src+8(FP), R1
	MOVD srcW+16(FP), R2
	MOVD srcH+24(FP), R3
	MOVD n+32(FP), R3         // overwrite srcH with n (loop counter)
	MOVD srcH+24(FP), R7      // reload srcH
	MOVD lutX+40(FP), R4
	MOVD lutY+48(FP), R5

	CMP  $0, R3
	BLE  warp_done

	SUB  $1, R2, R6           // R6 = srcW - 1
	SUB  $1, R7, R7           // R7 = srcH - 1
	MOVD $0xFFFF, R8          // fraction mask
	MOVD $255, R9             // clamp constant

warp_loop:
	// ---- Load and clamp sx ----
	MOVD (R4), R10            // sx = lutX[i]
	CMP  $0, R10
	BGE  sx_ok
	MOVD $0, R10              // clamp to 0
sx_ok:
	LSL  $16, R6, R11         // maxX = (srcW-1) << 16
	CMP  R11, R10
	CSEL LO, R10, R11, R10   // if sx < maxX, keep sx; else maxX

	// ---- Load and clamp sy ----
	MOVD (R5), R11            // sy = lutY[i]
	CMP  $0, R11
	BGE  sy_ok
	MOVD $0, R11
sy_ok:
	LSL  $16, R7, R12         // maxY = (srcH-1) << 16
	CMP  R12, R11
	CSEL LO, R11, R12, R11   // clamp sy

	// ---- Split sx → ix (R13), fx (R14) ----
	LSR  $16, R10, R13        // ix
	AND  R8, R10, R14         // fx

	// ---- Split sy → iy (R15), fy (R16) ----
	LSR  $16, R11, R15        // iy
	AND  R8, R11, R16         // fy

	// ---- Clamp ix1 (R17), iy1 (R19) ----
	ADD  $1, R13, R17         // ix1
	CMP  R6, R17
	CSEL LO, R17, R6, R17    // clamp ix1

	ADD  $1, R15, R19         // iy1
	CMP  R7, R19
	CSEL LO, R19, R7, R19    // clamp iy1

	// ---- Row base addresses ----
	MUL  R2, R15, R15         // row0off = iy * srcW
	MUL  R2, R19, R19         // row1off = iy1 * srcW
	ADD  R1, R15, R15         // R15 = &src[row0off]
	ADD  R1, R19, R19         // R19 = &src[row1off]

	// ---- Load 4 pixels ----
	MOVBU (R15)(R13), R10     // p00 = row0[ix]
	MOVBU (R15)(R17), R11     // p10 = row0[ix1]
	MOVBU (R19)(R13), R12     // p01 = row1[ix]
	MOVBU (R19)(R17), R13     // p11 = row1[ix1]

	// Now: R10=p00, R11=p10, R12=p01, R13=p11, R14=fx, R16=fy

	// ---- invFx ----
	MOVD $65536, R15
	SUB  R14, R15, R15        // R15 = invFx

	// ---- top = (p00*invFx + p10*fx + 32768) >> 16 ----
	MUL  R15, R10, R10        // p00 * invFx
	MUL  R14, R11, R11        // p10 * fx
	ADD  R11, R10, R10
	ADD  $32768, R10, R10
	LSR  $16, R10, R10        // top

	// ---- bot = (p01*invFx + p11*fx + 32768) >> 16 ----
	MUL  R15, R12, R12        // p01 * invFx
	MUL  R14, R13, R13        // p11 * fx
	ADD  R13, R12, R12
	ADD  $32768, R12, R12
	LSR  $16, R12, R12        // bot

	// ---- invFy ----
	MOVD $65536, R15
	SUB  R16, R15, R15        // R15 = invFy

	// ---- val = (top*invFy + bot*fy + 32768) >> 16 ----
	MUL  R15, R10, R10        // top * invFy
	MUL  R16, R12, R12        // bot * fy
	ADD  R12, R10, R10
	ADD  $32768, R10, R10
	LSR  $16, R10, R10        // val

	// ---- Clamp to 0-255 ----
	CMP  R9, R10
	CSEL HI, R9, R10, R10    // if val > 255, val = 255

	// ---- Store ----
	MOVB R10, (R0)

	// ---- Advance ----
	ADD  $1, R0, R0           // dst++
	ADD  $8, R4, R4           // lutX++
	ADD  $8, R5, R5           // lutY++
	SUB  $1, R3, R3
	CBNZ R3, warp_loop

warp_done:
	RET
