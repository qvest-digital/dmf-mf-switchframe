#include "textflag.h"

// ARM64 scalar assembly for luma key LUT lookup.
//
// func lumaKeyMaskLUT(mask, yPlane *byte, lut *byte, n int)
//
// Performs mask[i] = lut[yPlane[i]] for i in [0, n).
// 4x unrolled inner loop eliminates bounds checks and maximizes ILP.
//
// Register allocation:
//   R0 = mask ptr
//   R1 = yPlane ptr
//   R2 = lut ptr (base of 256-byte table)
//   R3 = n (loop counter)
//   R4-R7 = temporaries for 4x unroll

TEXT ·lumaKeyMaskLUT(SB), NOSPLIT, $0-32
	MOVD mask+0(FP), R0
	MOVD yPlane+8(FP), R1
	MOVD lut+16(FP), R2
	MOVD n+24(FP), R3

	CMP  $0, R3
	BLE  lkl_done

	// Try 4x unrolled loop
	CMP  $4, R3
	BLT  lkl_tail

lkl_loop4:
	// Pixel 0
	MOVBU (R1), R4             // R4 = yPlane[i]
	MOVBU (R2)(R4), R5         // R5 = lut[yPlane[i]]
	MOVB  R5, (R0)

	// Pixel 1
	MOVBU 1(R1), R4            // R4 = yPlane[i+1]
	MOVBU (R2)(R4), R5         // R5 = lut[yPlane[i+1]]
	MOVB  R5, 1(R0)

	// Pixel 2
	MOVBU 2(R1), R6            // R6 = yPlane[i+2]
	MOVBU (R2)(R6), R7         // R7 = lut[yPlane[i+2]]
	MOVB  R7, 2(R0)

	// Pixel 3
	MOVBU 3(R1), R6            // R6 = yPlane[i+3]
	MOVBU (R2)(R6), R7         // R7 = lut[yPlane[i+3]]
	MOVB  R7, 3(R0)

	ADD  $4, R1
	ADD  $4, R0
	SUB  $4, R3, R3
	CMP  $4, R3
	BGE  lkl_loop4

lkl_tail:
	CBZ  R3, lkl_done

lkl_tail_loop:
	MOVBU (R1), R4
	MOVBU (R2)(R4), R5
	MOVB  R5, (R0)
	ADD   $1, R1
	ADD   $1, R0
	SUB   $1, R3, R3
	CBNZ  R3, lkl_tail_loop

lkl_done:
	RET
