#include "textflag.h"

// ARM64 bilinear scaler kernel.
//
// scaleBilinearRow: scalar gather + scalar interpolation per pixel.
// The irregular gather pattern (each pixel reads from a different source X)
// prevents SIMD vectorization of the load phase. Assembly provides a
// speedup by eliminating bounds checks and using efficient register allocation.

// ============================================================================
// func scaleBilinearRow(dst, row0, row1 *byte, srcW, dstW int, xCoords *int64, fy int)
// ============================================================================
// Per destination pixel:
//   srcX = xCoords[dx]
//   ix = srcX >> 16, fx = srcX & 0xFFFF
//   ix1 = min(ix+1, srcW-1)
//   p00 = row0[ix], p10 = row0[ix1], p01 = row1[ix], p11 = row1[ix1]
//   invFx = 65536 - fx
//   top = (p00*invFx + p10*fx) >> 16
//   bot = (p01*invFx + p11*fx) >> 16
//   invFy = 65536 - fy
//   val = (top*invFy + bot*fy) >> 16
//   clamp 0-255
TEXT ·scaleBilinearRow(SB), NOSPLIT, $0-56
	MOVD dst+0(FP), R0        // dst pointer
	MOVD row0+8(FP), R1       // row0 pointer
	MOVD row1+16(FP), R2      // row1 pointer
	MOVD srcW+24(FP), R3      // source width
	MOVD dstW+32(FP), R4      // dest width (loop counter)
	MOVD xCoords+40(FP), R5   // xCoords pointer
	MOVD fy+48(FP), R6        // fy (16.16 fraction)

	CMP  $0, R4
	BLE  scale_done

	SUB  $1, R3, R7           // R7 = srcW - 1 (clamp limit)
	MOVD $65536, R8
	SUB  R6, R8, R8           // R8 = invFy = 65536 - fy
	MOVD $0xFFFF, R9          // fraction mask
	MOVD $255, R20            // clamp constant

scale_loop:
	// Load xCoords[dx] (int64)
	MOVD (R5), R10            // srcX
	LSR  $16, R10, R11        // ix = srcX >> 16
	AND  R9, R10, R12         // fx = srcX & 0xFFFF

	// Clamp ix1 = min(ix+1, srcW-1)
	ADD  $1, R11, R13         // ix1 = ix + 1
	CMP  R7, R13
	CSEL LT, R13, R7, R13    // if ix1 < srcW-1, keep ix1; else srcW-1

	// Load 4 source pixels
	MOVBU (R1)(R11), R14      // p00 = row0[ix]
	MOVBU (R1)(R13), R15      // p10 = row0[ix1]
	MOVBU (R2)(R11), R16      // p01 = row1[ix]
	MOVBU (R2)(R13), R17      // p11 = row1[ix1]

	// invFx = 65536 - fx
	MOVD $65536, R19
	SUB  R12, R19, R19        // R19 = invFx

	// top = (p00*invFx + p10*fx + 32768) >> 16  (round to nearest)
	MUL  R19, R14, R14        // p00 * invFx
	MUL  R12, R15, R15        // p10 * fx
	ADD  R15, R14, R14
	ADD  $32768, R14, R14     // rounding bias
	LSR  $16, R14, R14        // top

	// bot = (p01*invFx + p11*fx + 32768) >> 16  (round to nearest)
	MUL  R19, R16, R16        // p01 * invFx
	MUL  R12, R17, R17        // p11 * fx
	ADD  R17, R16, R16
	ADD  $32768, R16, R16     // rounding bias
	LSR  $16, R16, R16        // bot

	// val = (top*invFy + bot*fy + 32768) >> 16  (round to nearest)
	MUL  R8, R14, R14         // top * invFy
	MUL  R6, R16, R16         // bot * fy
	ADD  R16, R14, R14
	ADD  $32768, R14, R14     // rounding bias
	LSR  $16, R14, R14        // val

	// Clamp to 0-255 (only >255 is possible with unsigned math)
	CMP  R20, R14
	CSEL HI, R20, R14, R14   // if val > 255, val = 255

	MOVB R14, (R0)            // store

	ADD  $1, R0, R0           // dst++
	ADD  $8, R5, R5           // xCoords++ (int64 = 8 bytes)
	SUB  $1, R4, R4
	CBNZ R4, scale_loop

scale_done:
	RET
