#include "textflag.h"

// ARM64 scalar kernel for RGBA-to-YUV alpha blending (Y plane, one row).
//
// func alphaBlendRGBARowY(yRow *byte, rgba *byte, width int, alphaScale256 int)
//
// For each pixel:
//   A' = A + (A >> 7)                       (map 0-255 to 0-256)
//   a256 = (A' * alphaScale256) >> 8
//   if a256 == 0: skip (transparent)
//   overlayY = (54*R + 183*G + 18*B + 128) >> 8
//   yRow[i] = (yRow[i]*(256-a256) + overlayY*a256 + 128) >> 8
//
// Registers:
//   R0 = yRow pointer
//   R1 = rgba pointer
//   R2 = loop counter (width)
//   R3 = alphaScale256
TEXT ·alphaBlendRGBARowY(SB), NOSPLIT, $0-32
	MOVD yRow+0(FP), R0
	MOVD rgba+8(FP), R1
	MOVD width+16(FP), R2
	MOVD alphaScale256+24(FP), R3

	CMP  $0, R2
	BLE  done

loop:
	// Load alpha byte and map 0-255 to 0-256
	MOVBU 3(R1), R4            // R4 = rgba[3] (alpha = A)
	LSR   $7, R4, R5           // R5 = A >> 7
	ADD   R5, R4, R4           // R4 = A' = A + (A >> 7)
	MUL   R3, R4, R4           // R4 = A' * alphaScale256
	LSR   $8, R4, R4           // R4 = a256

	CBZ   R4, skip             // skip if transparent

	// Load R, G, B
	MOVBU (R1), R5             // R5 = R
	MOVBU 1(R1), R6            // R6 = G
	MOVBU 2(R1), R7            // R7 = B

	// overlayY = (54*R + 183*G + 18*B + 128) >> 8
	MOVD  $54, R8
	MUL   R8, R5, R8           // R8 = 54*R
	MOVD  $183, R9
	MADD  R9, R8, R6, R8       // R8 = 54*R + 183*G
	MOVD  $18, R9
	MADD  R9, R8, R7, R8       // R8 = 54*R + 183*G + 18*B
	ADD   $128, R8, R8
	LSR   $8, R8, R8           // R8 = overlayY

	// inv = 256 - a256
	MOVD  $256, R9
	SUB   R4, R9, R9           // R9 = inv

	// yRow[i] = (yRow[i]*inv + overlayY*a256 + 128) >> 8
	MOVBU (R0), R10            // R10 = yRow[i]
	MUL   R9, R10, R10         // R10 = yRow[i] * inv
	MUL   R4, R8, R8           // R8 = overlayY * a256
	ADD   R8, R10, R10
	ADD   $128, R10, R10
	LSR   $8, R10, R10
	MOVB  R10, (R0)            // store result

skip:
	ADD   $4, R1, R1           // next RGBA pixel
	ADD   $1, R0, R0           // next Y pixel
	SUB   $1, R2, R2
	CBNZ  R2, loop

done:
	RET
