#include "textflag.h"

// AMD64 SSE2 kernel for mask-based YUV plane blending.
//
// func blendMaskY(bg *byte, fill *byte, mask *byte, n int)
//
// SSE2 path processes 16 bytes per iteration:
//   w = mask[i] + (mask[i] >> 7)           // 0-255 → 0-256
//   inv = 256 - w
//   bg[i] = (bg[i]*inv + fill[i]*w + 128) >> 8
//
// Max value of bg*inv + fill*w + 128 = 255*256 + 128 = 65408 < 65535,
// so all arithmetic fits in uint16. PMULLW is safe.
//
// Registers:
//   DI = bg ptr, SI = fill ptr, DX = mask ptr, CX = n (loop counter)
//
// SSE constants:
//   X13 = zero, X14 = 256 broadcast, X15 = 128 broadcast
TEXT ·blendMaskY(SB), NOSPLIT, $0-32
	MOVQ bg+0(FP), DI
	MOVQ fill+8(FP), SI
	MOVQ mask+16(FP), DX
	MOVQ n+24(FP), CX

	TESTQ CX, CX
	JLE   bmy_done

	CMPQ  CX, $16
	JLT   bmy_scalar

	// Set up SSE2 constants
	PXOR X13, X13              // X13 = all zeros

	// X14 = [256, 256, 256, 256, 256, 256, 256, 256] (8 × uint16)
	MOVQ $0x0100010001000100, AX
	MOVQ AX, X14
	PUNPCKLQDQ X14, X14

	// X15 = [128, 128, 128, 128, 128, 128, 128, 128] (8 × uint16)
	MOVQ $0x0080008000800080, AX
	MOVQ AX, X15
	PUNPCKLQDQ X15, X15

bmy_sse2:
	// Load 16 bytes
	MOVOU (DI), X0             // X0 = bg[0..15]
	MOVOU (SI), X1             // X1 = fill[0..15]
	MOVOU (DX), X2             // X2 = mask[0..15]

	// === Lower 8 bytes ===
	// Widen mask lower to uint16
	MOVO  X2, X3
	PUNPCKLBW X13, X3          // X3 = mask_lo.8H

	// w = mask + (mask >> 7)
	MOVO  X3, X4
	PSRLW $7, X4               // X4 = mask >> 7
	PADDW X4, X3               // X3 = w_lo

	// inv = 256 - w
	MOVO  X14, X5
	PSUBW X3, X5               // X5 = inv_lo

	// Widen bg and fill lower to uint16
	MOVO  X0, X6
	PUNPCKLBW X13, X6          // X6 = bg_lo.8H
	MOVO  X1, X7
	PUNPCKLBW X13, X7          // X7 = fill_lo.8H

	// bg * inv + fill * w + 128
	PMULLW X5, X6              // X6 = bg_lo * inv_lo
	PMULLW X3, X7              // X7 = fill_lo * w_lo
	PADDW X7, X6               // X6 = bg*inv + fill*w
	PADDW X15, X6              // X6 += 128
	PSRLW $8, X6               // X6 = result_lo

	// === Upper 8 bytes ===
	MOVO  X2, X3
	PUNPCKHBW X13, X3          // X3 = mask_hi.8H

	MOVO  X3, X4
	PSRLW $7, X4
	PADDW X4, X3               // X3 = w_hi

	MOVO  X14, X5
	PSUBW X3, X5               // X5 = inv_hi

	MOVO  X0, X7
	PUNPCKHBW X13, X7          // X7 = bg_hi.8H
	MOVO  X1, X8
	PUNPCKHBW X13, X8          // X8 = fill_hi.8H

	PMULLW X5, X7              // X7 = bg_hi * inv_hi
	PMULLW X3, X8              // X8 = fill_hi * w_hi
	PADDW X8, X7               // X7 = bg*inv + fill*w
	PADDW X15, X7              // X7 += 128
	PSRLW $8, X7               // X7 = result_hi

	// Pack uint16 → uint8 with unsigned saturation
	PACKUSWB X7, X6            // X6 = [result_lo | result_hi]

	// Store 16 result bytes
	MOVOU X6, (DI)

	ADDQ $16, DI
	ADDQ $16, SI
	ADDQ $16, DX
	SUBQ $16, CX
	CMPQ CX, $16
	JGE  bmy_sse2

bmy_scalar:
	TESTQ CX, CX
	JLE   bmy_done

bmy_loop:
	// w = mask + (mask >> 7)
	MOVBQZX (DX), AX           // AX = mask[i]
	MOVQ    AX, R8
	SHRQ    $7, R8             // R8 = mask >> 7
	ADDQ    R8, AX             // AX = w
	TESTQ   AX, AX
	JZ      bmy_skip           // skip if transparent

	// inv = 256 - w
	MOVQ    $256, R8
	SUBQ    AX, R8             // R8 = inv

	// bg[i] = (bg[i]*inv + fill[i]*w + 128) >> 8
	MOVBQZX (DI), R9           // R9 = bg[i]
	MOVBQZX (SI), R10          // R10 = fill[i]
	IMULQ   R8, R9             // R9 = bg * inv
	IMULQ   AX, R10            // R10 = fill * w
	ADDQ    R10, R9
	ADDQ    $128, R9
	SHRQ    $8, R9
	MOVB    R9, (DI)

bmy_skip:
	INCQ DI
	INCQ SI
	INCQ DX
	DECQ CX
	JNZ  bmy_loop

bmy_done:
	RET
