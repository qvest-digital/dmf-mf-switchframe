#include "textflag.h"

// AMD64 bilinear scaler kernel.
// Scalar gather + scalar interpolation per pixel.
// Eliminates bounds checks vs the Go version.

// ============================================================================
// func scaleBilinearRow(dst, row0, row1 *byte, srcW, dstW int, xCoords *int64, fy int)
// ============================================================================
TEXT ·scaleBilinearRow(SB), NOSPLIT, $0-56
	MOVQ dst+0(FP), DI        // dst pointer
	MOVQ row0+8(FP), SI       // row0 pointer
	MOVQ row1+16(FP), DX      // row1 pointer
	MOVQ srcW+24(FP), R8      // source width
	MOVQ dstW+32(FP), CX      // dest width (loop counter)
	MOVQ xCoords+40(FP), R9   // xCoords pointer
	MOVQ fy+48(FP), R10       // fy (16.16 fraction)

	TESTQ CX, CX
	JLE   scale_done

	SUBQ  $1, R8              // R8 = srcW - 1 (clamp limit)
	MOVQ  $65536, R11
	SUBQ  R10, R11            // R11 = invFy = 65536 - fy

scale_loop:
	// Load xCoords[dx] (int64)
	MOVQ  (R9), AX            // srcX
	MOVQ  AX, BX
	SHRQ  $16, BX             // ix = srcX >> 16
	ANDQ  $0xFFFF, AX         // fx = srcX & 0xFFFF

	// Clamp ix1 = min(ix+1, srcW-1)
	LEAQ  1(BX), R12          // ix1 = ix + 1
	CMPQ  R12, R8
	CMOVQGT R8, R12           // if ix1 > srcW-1, ix1 = srcW-1

	// Load 4 source pixels
	MOVBLZX (SI)(BX*1), R13   // p00 = row0[ix]
	MOVBLZX (SI)(R12*1), R14  // p10 = row0[ix1]
	MOVBLZX (DX)(BX*1), R15   // p01 = row1[ix]
	MOVBLZX (DX)(R12*1), BX   // p11 = row1[ix1] (reuse BX)

	// invFx = 65536 - fx
	MOVQ  $65536, R12
	SUBQ  AX, R12             // R12 = invFx

	// top = (p00*invFx + p10*fx) >> 16
	IMULQ R12, R13            // p00 * invFx
	IMULQ AX, R14             // p10 * fx
	ADDQ  R14, R13
	SHRQ  $16, R13            // top

	// bot = (p01*invFx + p11*fx) >> 16
	IMULQ R12, R15            // p01 * invFx
	IMULQ AX, BX              // p11 * fx
	ADDQ  BX, R15
	SHRQ  $16, R15            // bot

	// val = (top*invFy + bot*fy) >> 16
	IMULQ R11, R13            // top * invFy
	IMULQ R10, R15            // bot * fy
	ADDQ  R15, R13
	SHRQ  $16, R13            // val

	// Clamp to 0-255
	CMPQ  R13, $255
	MOVQ  $255, AX
	CMOVQGT AX, R13           // if val > 255, val = 255

	MOVB  R13, (DI)           // store

	INCQ  DI                  // dst++
	ADDQ  $8, R9              // xCoords++ (int64 = 8 bytes)
	DECQ  CX
	JNZ   scale_loop

scale_done:
	RET
