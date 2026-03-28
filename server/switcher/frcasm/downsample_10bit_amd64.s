#include "textflag.h"

// AMD64 scalar kernel for 10-bit Y plane 2x downsample with 10->8 bit conversion.
//
// func DownsampleY10_2x(dst, src *byte, srcStride, dstW, dstH int)
//
// For each 2x2 block of uint16 LE source samples:
//   avg = (p00 + p10 + p01 + p11 + 2) >> 2   (10-bit average)
//   dst = avg >> 2                             (10->8 bit)
//
// Uses scalar loop. AVX2 optimization deferred due to complexity of
// paired uint16 add + narrow to uint8 with cross-lane handling.

TEXT ·DownsampleY10_2x(SB), NOSPLIT, $0-40
	MOVQ dst+0(FP), DI          // dst
	MOVQ src+8(FP), SI          // src (row0)
	MOVQ srcStride+16(FP), R8   // srcStride (bytes)
	MOVQ dstW+24(FP), R9        // dstW
	MOVQ dstH+32(FP), R10       // dstH

	TESTQ R10, R10
	JZ    ds10x_done
	TESTQ R9, R9
	JZ    ds10x_done

	// R11 = row1 (row0 + srcStride)
	MOVQ  SI, R11
	ADDQ  R8, R11

	// R12 = srcStride * 2 (advance to next row pair)
	MOVQ  R8, R12
	SHLQ  $1, R12

ds10x_row_loop:
	MOVQ  R9, CX               // CX = remaining output cols
	MOVQ  SI, AX               // AX = row0 temp ptr
	MOVQ  R11, BX               // BX = row1 temp ptr

ds10x_col_loop:
	// Load 2 uint16 from row0
	MOVWLZX (AX), DX           // p00
	MOVWLZX 2(AX), R13         // p10
	ADDQ    $4, AX

	// Load 2 uint16 from row1
	MOVWLZX (BX), R14          // p01
	MOVWLZX 2(BX), R15         // p11
	ADDQ    $4, BX

	// avg = (p00 + p10 + p01 + p11 + 2) >> 2
	ADDQ    R13, DX
	ADDQ    R14, DX
	ADDQ    R15, DX
	ADDQ    $2, DX
	SHRQ    $2, DX              // 10-bit avg

	// 10->8 bit: >> 2
	SHRQ    $2, DX

	MOVB    DX, (DI)
	INCQ    DI

	DECQ    CX
	JNZ     ds10x_col_loop

	// Advance to next row pair
	ADDQ    R12, SI             // row0 += srcStride * 2
	MOVQ    SI, R11
	ADDQ    R8, R11             // row1 = new row0 + srcStride

	DECQ    R10
	JNZ     ds10x_row_loop

ds10x_done:
	RET
