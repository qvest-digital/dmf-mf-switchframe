#include "textflag.h"

// AMD64 AVX2/scalar kernel for 10-bit key mask blending.
//
// func keyMaskBlend10Row(bg, fill, mask *byte, count int)
//
// For each uint16 sample:
//   w = mask[i] + (mask[i] >> 7)    // 0-255 → 0-256
//   bg[i] = clamp10((bg[i] * (256-w) + fill[i] * w + 128) >> 8)
//
// Max intermediate: 1023 * 256 + 128 = 262016, fits in int32.
//
// Scalar implementation (AVX2 deferred due to mixed-width mask/sample access).

TEXT ·keyMaskBlend10Row(SB), NOSPLIT, $0-32
	MOVQ bg+0(FP), DI           // bg *byte
	MOVQ fill+8(FP), SI         // fill *byte
	MOVQ mask+16(FP), DX        // mask *byte
	MOVQ count+24(FP), CX       // count

	TESTQ CX, CX
	JLE   km10_done

	MOVQ  $128, R10              // rounding constant
	MOVQ  $1023, R11             // clamp constant

km10_loop:
	// w = mask + (mask >> 7)
	MOVBLZX (DX), AX            // AX = mask[i]
	MOVQ    AX, BX
	SHRQ    $7, BX               // BX = mask >> 7
	ADDQ    BX, AX               // AX = w

	TESTQ   AX, AX
	JZ      km10_skip

	// inv = 256 - w
	MOVQ    $256, R8
	SUBQ    AX, R8               // R8 = inv

	// bg[i] = (bg * inv + fill * w + 128) >> 8
	MOVWLZX (DI), R9            // R9 = bg[i]
	MOVWLZX (SI), BX            // BX = fill[i]

	IMULQ   R8, R9               // R9 = bg * inv
	IMULQ   AX, BX               // BX = fill * w
	ADDQ    BX, R9               // R9 = bg*inv + fill*w
	ADDQ    R10, R9              // R9 += 128
	SHRQ    $8, R9               // R9 >>= 8

	// Clamp to 1023
	CMPQ    R9, R11
	JLE     km10_store
	MOVQ    R11, R9

km10_store:
	MOVW    R9, (DI)

km10_skip:
	ADDQ    $2, DI
	ADDQ    $2, SI
	INCQ    DX
	DECQ    CX
	JNZ     km10_loop

km10_done:
	RET
