#include "textflag.h"

// AMD64 scalar assembly for luma key LUT lookup.
//
// func lumaKeyMaskLUT(mask, yPlane *byte, lut *byte, n int)
//
// Performs mask[i] = lut[yPlane[i]] for i in [0, n).
// 4x unrolled inner loop eliminates bounds checks and maximizes ILP.
//
// Register allocation:
//   DI = mask ptr
//   SI = yPlane ptr
//   DX = lut ptr (base of 256-byte table)
//   CX = n (loop counter)
//   AX, BX, R8, R9 = temporaries for 4x unroll

TEXT ·lumaKeyMaskLUT(SB), NOSPLIT, $0-32
	MOVQ mask+0(FP), DI
	MOVQ yPlane+8(FP), SI
	MOVQ lut+16(FP), DX
	MOVQ n+24(FP), CX

	TESTQ CX, CX
	JLE   lkl_done

	// Try 4x unrolled loop
	CMPQ CX, $4
	JLT  lkl_tail

lkl_loop4:
	// Pixel 0
	MOVBLZX (SI), AX           // AX = yPlane[i]
	MOVB    (DX)(AX*1), AX     // AX = lut[yPlane[i]]
	MOVB    AX, (DI)

	// Pixel 1
	MOVBLZX 1(SI), BX          // BX = yPlane[i+1]
	MOVB    (DX)(BX*1), BX     // BX = lut[yPlane[i+1]]
	MOVB    BX, 1(DI)

	// Pixel 2
	MOVBLZX 2(SI), R8          // R8 = yPlane[i+2]
	MOVB    (DX)(R8*1), R8     // R8 = lut[yPlane[i+2]]
	MOVB    R8, 2(DI)

	// Pixel 3
	MOVBLZX 3(SI), R9          // R9 = yPlane[i+3]
	MOVB    (DX)(R9*1), R9     // R9 = lut[yPlane[i+3]]
	MOVB    R9, 3(DI)

	ADDQ $4, SI
	ADDQ $4, DI
	SUBQ $4, CX
	CMPQ CX, $4
	JGE  lkl_loop4

lkl_tail:
	TESTQ CX, CX
	JZ    lkl_done

lkl_tail_loop:
	MOVBLZX (SI), AX
	MOVB    (DX)(AX*1), AX
	MOVB    AX, (DI)
	INCQ    SI
	INCQ    DI
	DECQ    CX
	JNZ     lkl_tail_loop

lkl_done:
	RET
