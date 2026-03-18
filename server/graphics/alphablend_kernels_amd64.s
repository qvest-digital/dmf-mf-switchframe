#include "textflag.h"

// AMD64 unrolled kernel for RGBA-to-YUV alpha blending (Y plane, one row).
//
// func alphaBlendRGBARowY(yRow *byte, rgba *byte, width int, alphaScale256 int)
//
// Processes 4 pixels per iteration (unrolled) to reduce loop overhead and
// improve instruction-level parallelism. Falls back to 1-pixel loop for
// remaining pixels.
//
// For each pixel:
//   A' = A + (A >> 7)                       (map 0-255 to 0-256)
//   a256 = (A' * alphaScale256) >> 8
//   if a256 == 0: skip (transparent)
//   overlayY = 16 + ((47*R + 157*G + 16*B + 128) >> 8)
//   yRow[i] = (yRow[i]*(256-a256) + overlayY*a256 + 128) >> 8
//
// Registers:
//   DI = yRow pointer
//   SI = rgba pointer
//   CX = loop counter (width)
//   R8 = alphaScale256
TEXT ·alphaBlendRGBARowY(SB), NOSPLIT, $0-32
	MOVQ yRow+0(FP), DI
	MOVQ rgba+8(FP), SI
	MOVQ width+16(FP), CX
	MOVQ alphaScale256+24(FP), R8

	TESTQ CX, CX
	JLE   done

	CMPQ  CX, $4
	JLT   scalar_tail

unrolled4:
	// ---- Pixel 0 ----
	MOVBQZX 3(SI), AX
	MOVQ    AX, R15
	SHRQ    $7, R15
	ADDQ    R15, AX
	IMULQ   R8, AX
	SHRQ    $8, AX
	TESTQ   AX, AX
	JZ      p0_skip

	MOVBQZX (SI), R9
	MOVBQZX 1(SI), R10
	MOVBQZX 2(SI), R11
	IMUL3Q  $47, R9, R12
	IMUL3Q  $157, R10, R13
	ADDQ    R13, R12
	IMUL3Q  $16, R11, R13
	ADDQ    R13, R12
	ADDQ    $128, R12
	SHRQ    $8, R12
	ADDQ    $16, R12
	MOVQ    $256, R13
	SUBQ    AX, R13
	MOVBQZX (DI), R14
	IMULQ   R13, R14
	IMULQ   AX, R12
	ADDQ    R12, R14
	ADDQ    $128, R14
	SHRQ    $8, R14
	MOVB    R14, (DI)
p0_skip:

	// ---- Pixel 1 ----
	MOVBQZX 7(SI), AX
	MOVQ    AX, R15
	SHRQ    $7, R15
	ADDQ    R15, AX
	IMULQ   R8, AX
	SHRQ    $8, AX
	TESTQ   AX, AX
	JZ      p1_skip

	MOVBQZX 4(SI), R9
	MOVBQZX 5(SI), R10
	MOVBQZX 6(SI), R11
	IMUL3Q  $47, R9, R12
	IMUL3Q  $157, R10, R13
	ADDQ    R13, R12
	IMUL3Q  $16, R11, R13
	ADDQ    R13, R12
	ADDQ    $128, R12
	SHRQ    $8, R12
	ADDQ    $16, R12
	MOVQ    $256, R13
	SUBQ    AX, R13
	MOVBQZX 1(DI), R14
	IMULQ   R13, R14
	IMULQ   AX, R12
	ADDQ    R12, R14
	ADDQ    $128, R14
	SHRQ    $8, R14
	MOVB    R14, 1(DI)
p1_skip:

	// ---- Pixel 2 ----
	MOVBQZX 11(SI), AX
	MOVQ    AX, R15
	SHRQ    $7, R15
	ADDQ    R15, AX
	IMULQ   R8, AX
	SHRQ    $8, AX
	TESTQ   AX, AX
	JZ      p2_skip

	MOVBQZX 8(SI), R9
	MOVBQZX 9(SI), R10
	MOVBQZX 10(SI), R11
	IMUL3Q  $47, R9, R12
	IMUL3Q  $157, R10, R13
	ADDQ    R13, R12
	IMUL3Q  $16, R11, R13
	ADDQ    R13, R12
	ADDQ    $128, R12
	SHRQ    $8, R12
	ADDQ    $16, R12
	MOVQ    $256, R13
	SUBQ    AX, R13
	MOVBQZX 2(DI), R14
	IMULQ   R13, R14
	IMULQ   AX, R12
	ADDQ    R12, R14
	ADDQ    $128, R14
	SHRQ    $8, R14
	MOVB    R14, 2(DI)
p2_skip:

	// ---- Pixel 3 ----
	MOVBQZX 15(SI), AX
	MOVQ    AX, R15
	SHRQ    $7, R15
	ADDQ    R15, AX
	IMULQ   R8, AX
	SHRQ    $8, AX
	TESTQ   AX, AX
	JZ      p3_skip

	MOVBQZX 12(SI), R9
	MOVBQZX 13(SI), R10
	MOVBQZX 14(SI), R11
	IMUL3Q  $47, R9, R12
	IMUL3Q  $157, R10, R13
	ADDQ    R13, R12
	IMUL3Q  $16, R11, R13
	ADDQ    R13, R12
	ADDQ    $128, R12
	SHRQ    $8, R12
	ADDQ    $16, R12
	MOVQ    $256, R13
	SUBQ    AX, R13
	MOVBQZX 3(DI), R14
	IMULQ   R13, R14
	IMULQ   AX, R12
	ADDQ    R12, R14
	ADDQ    $128, R14
	SHRQ    $8, R14
	MOVB    R14, 3(DI)
p3_skip:

	ADDQ    $16, SI
	ADDQ    $4, DI
	SUBQ    $4, CX
	CMPQ    CX, $4
	JGE     unrolled4

scalar_tail:
	TESTQ CX, CX
	JLE   done

loop:
	MOVBQZX 3(SI), AX
	MOVQ    AX, R15
	SHRQ    $7, R15
	ADDQ    R15, AX
	IMULQ   R8, AX
	SHRQ    $8, AX
	TESTQ   AX, AX
	JZ      skip

	MOVBQZX (SI), R9
	MOVBQZX 1(SI), R10
	MOVBQZX 2(SI), R11

	IMUL3Q  $47, R9, R12
	IMUL3Q  $157, R10, R13
	ADDQ    R13, R12
	IMUL3Q  $16, R11, R13
	ADDQ    R13, R12
	ADDQ    $128, R12
	SHRQ    $8, R12
	ADDQ    $16, R12

	MOVQ    $256, R13
	SUBQ    AX, R13

	MOVBQZX (DI), R14
	IMULQ   R13, R14
	IMULQ   AX, R12
	ADDQ    R12, R14
	ADDQ    $128, R14
	SHRQ    $8, R14
	MOVB    R14, (DI)

skip:
	ADDQ    $4, SI
	ADDQ    $1, DI
	DECQ    CX
	JNZ     loop

done:
	RET
