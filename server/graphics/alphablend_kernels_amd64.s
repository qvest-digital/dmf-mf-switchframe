#include "textflag.h"

// AMD64 scalar kernel for RGBA-to-YUV alpha blending (Y plane, one row).
//
// func alphaBlendRGBARowY(yRow *byte, rgba *byte, width int, alphaScale256 int)
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

loop:
	// Load alpha byte and map 0-255 to 0-256
	MOVBQZX 3(SI), AX        // AX = rgba[3] (alpha)
	MOVQ    AX, R15           // R15 = A
	SHRQ    $7, R15           // R15 = A >> 7
	ADDQ    R15, AX           // AX = A + (A >> 7) = A'
	IMULQ   R8, AX            // AX = A' * alphaScale256
	SHRQ    $8, AX             // AX = a256 = (A' * alphaScale256) >> 8
	TESTQ   AX, AX
	JZ      skip               // skip if transparent

	// Load R, G, B
	MOVBQZX (SI), R9          // R9 = R
	MOVBQZX 1(SI), R10        // R10 = G
	MOVBQZX 2(SI), R11        // R11 = B

	// overlayY = 16 + ((47*R + 157*G + 16*B + 128) >> 8)
	IMUL3Q  $47, R9, R12
	IMUL3Q  $157, R10, R13
	ADDQ    R13, R12
	IMUL3Q  $16, R11, R13
	ADDQ    R13, R12
	ADDQ    $128, R12
	SHRQ    $8, R12
	ADDQ    $16, R12            // R12 = overlayY (limited-range)

	// inv = 256 - a256
	MOVQ    $256, R13
	SUBQ    AX, R13            // R13 = inv

	// yRow[i] = (yRow[i]*inv + overlayY*a256 + 128) >> 8
	MOVBQZX (DI), R14         // R14 = yRow[i]
	IMULQ   R13, R14           // R14 = yRow[i] * inv
	IMULQ   AX, R12            // R12 = overlayY * a256
	ADDQ    R12, R14
	ADDQ    $128, R14
	SHRQ    $8, R14
	MOVB    R14, (DI)          // store result

skip:
	ADDQ    $4, SI             // next RGBA pixel
	ADDQ    $1, DI             // next Y pixel
	DECQ    CX
	JNZ     loop

done:
	RET
