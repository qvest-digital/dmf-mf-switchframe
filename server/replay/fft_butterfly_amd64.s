#include "textflag.h"

// AMD64 SSE butterfly for radix-2 FFT stage.
//
// func butterflyRadix2(data, twiddle []float32, halfN, stride, twiddleStride int)
//
// For twiddleStride == 1, processes 2 butterflies per iteration using SSE.
// Falls back to scalar for other strides.

// Sign mask for complex multiply: negate real part after swap.
// XOR with [signbit, 0, signbit, 0] flips sign of lanes 0 and 2.
DATA sign_neg_re<>+0x00(SB)/4, $0x80000000  // -0.0 (sign bit)
DATA sign_neg_re<>+0x04(SB)/4, $0x00000000  // +0.0
DATA sign_neg_re<>+0x08(SB)/4, $0x80000000  // -0.0
DATA sign_neg_re<>+0x0C(SB)/4, $0x00000000  // +0.0
GLOBL sign_neg_re<>(SB), RODATA|NOPTR, $16

// Registers:
//   DI = data ptr, SI = twiddle ptr
//   DX = halfN, CX = stride (unused), R8 = twiddleStride
//   R9 = k (loop counter)
//   R10 = even byte offset, R11 = odd byte offset
//   R12 = halfN * 8 (byte offset for odd half)
TEXT ·butterflyRadix2(SB), NOSPLIT, $0-80
	MOVQ data_base+0(FP), DI
	MOVQ twiddle_base+24(FP), SI
	MOVQ halfN+48(FP), DX
	MOVQ stride+56(FP), CX
	MOVQ twiddleStride+64(FP), R8

	TESTQ DX, DX
	JLE  bf_done

	// Check if we can use SSE path (twiddleStride == 1)
	CMPQ R8, $1
	JNE  bf_scalar_init

	// --- SSE path (twiddleStride == 1) ---
	// halfN * 8 = byte offset from even to odd
	MOVQ DX, R12
	SHLQ $3, R12                   // R12 = halfN * 8

	// Load sign mask
	MOVOU sign_neg_re<>(SB), X7

	XORQ R9, R9                   // k = 0

	// Need at least 2 butterflies for SSE
	MOVQ DX, R13
	SUBQ $1, R13
	CMPQ R9, R13
	JGE  bf_sse_tail

bf_sse_loop:
	// Even offset: k * 8 bytes
	MOVQ R9, R10
	SHLQ $3, R10
	// Odd offset: even + halfN*8
	MOVQ R10, R11
	ADDQ R12, R11

	// Load twiddle [wRe0, wIm0, wRe1, wIm1]
	MOVQ R9, R13
	SHLQ $3, R13
	MOVUPS (SI)(R13*1), X0        // X0 = [wRe0, wIm0, wRe1, wIm1]

	// Load even data
	MOVUPS (DI)(R10*1), X1        // X1 = [eRe0, eIm0, eRe1, eIm1]

	// Load odd data
	MOVUPS (DI)(R11*1), X2        // X2 = [oRe0, oIm0, oRe1, oIm1]

	// Complex multiply: t = W * odd
	// Strategy using SHUFPS:
	// 1. SHUFPS $0xA0 on W: [wRe0, wRe0, wRe1, wRe1]
	// 2. SHUFPS $0xF5 on W: [wIm0, wIm0, wIm1, wIm1]
	// 3. MULPS wRe_dup * odd = [wRe*oRe, wRe*oIm, wRe*oRe, wRe*oIm]
	// 4. SHUFPS $0xB1 on odd: swap re/im = [oIm0, oRe0, oIm1, oRe1]
	// 5. MULPS wIm_dup * swapped = [wIm*oIm, wIm*oRe, wIm*oIm, wIm*oRe]
	// 6. XOR sign mask: [-wIm*oIm, wIm*oRe, -wIm*oIm, wIm*oRe]
	// 7. ADDPS step3 + step6

	MOVAPS X0, X3
	SHUFPS $0xA0, X3, X3          // X3 = [wRe0, wRe0, wRe1, wRe1]
	MOVAPS X0, X4
	SHUFPS $0xF5, X4, X4          // X4 = [wIm0, wIm0, wIm1, wIm1]

	MOVAPS X2, X5
	MULPS  X3, X5                  // X5 = wRe * odd

	MOVAPS X2, X6
	SHUFPS $0xB1, X6, X6          // X6 = [oIm0, oRe0, oIm1, oRe1]
	MULPS  X4, X6                  // X6 = wIm * swapped
	XORPS  X7, X6                  // X6 = [-wIm*oIm, wIm*oRe, ...]

	ADDPS  X6, X5                  // X5 = t = W * odd

	// Butterfly: even' = even + t, odd' = even - t
	MOVAPS X1, X3
	ADDPS  X5, X3                  // X3 = even + t
	MOVAPS X1, X4
	SUBPS  X5, X4                  // X4 = even - t

	// Store results
	MOVUPS X3, (DI)(R10*1)        // store even'
	MOVUPS X4, (DI)(R11*1)        // store odd'

	ADDQ   $2, R9                 // k += 2
	MOVQ   DX, R13
	SUBQ   $1, R13
	CMPQ   R9, R13
	JLT    bf_sse_loop

bf_sse_tail:
	// Handle remaining single butterfly (if halfN is odd)
	CMPQ R9, DX
	JGE  bf_done

	// Single scalar butterfly
	MOVQ R9, R10
	SHLQ $3, R10                   // even byte offset
	MOVQ R10, R11
	ADDQ R12, R11                  // odd byte offset

	MOVQ R9, R13
	SHLQ $3, R13                   // twiddle byte offset

	// Load twiddle
	MOVSS (SI)(R13*1), X0         // wRe
	MOVSS 4(SI)(R13*1), X1        // wIm

	// Load odd
	MOVSS (DI)(R11*1), X2         // oRe
	MOVSS 4(DI)(R11*1), X3        // oIm

	// Complex multiply
	MOVAPS X0, X4
	MULSS  X2, X4                  // wRe * oRe
	MOVAPS X1, X5
	MULSS  X3, X5                  // wIm * oIm
	SUBSS  X5, X4                  // tRe = wRe*oRe - wIm*oIm

	MOVAPS X0, X5
	MULSS  X3, X5                  // wRe * oIm
	MOVAPS X1, X6
	MULSS  X2, X6                  // wIm * oRe
	ADDSS  X6, X5                  // tIm = wRe*oIm + wIm*oRe

	// Load even
	MOVSS (DI)(R10*1), X2         // eRe
	MOVSS 4(DI)(R10*1), X3        // eIm

	// Butterfly
	MOVAPS X2, X6
	ADDSS  X4, X6                  // even' Re
	MOVAPS X3, X8
	ADDSS  X5, X8                  // even' Im

	SUBSS  X4, X2                  // odd' Re
	SUBSS  X5, X3                  // odd' Im

	// Store
	MOVSS  X6, (DI)(R10*1)
	MOVSS  X8, 4(DI)(R10*1)
	MOVSS  X2, (DI)(R11*1)
	MOVSS  X3, 4(DI)(R11*1)

	JMP    bf_done

bf_scalar_init:
	// --- Scalar path (twiddleStride != 1) ---
	MOVQ DX, R12
	SHLQ $3, R12                   // R12 = halfN * 8
	XORQ R9, R9                   // k = 0

bf_scalar_loop:
	CMPQ R9, DX
	JGE  bf_done

	// Twiddle index: k * twiddleStride * 8 bytes
	MOVQ R9, R13
	IMULQ R8, R13                  // k * twiddleStride
	SHLQ $3, R13                   // * 8 bytes

	// Even/odd data offsets
	MOVQ R9, R10
	SHLQ $3, R10                   // k * 8
	MOVQ R10, R11
	ADDQ R12, R11                  // (k + halfN) * 8

	// Load twiddle
	MOVSS (SI)(R13*1), X0         // wRe
	MOVSS 4(SI)(R13*1), X1        // wIm

	// Load odd
	MOVSS (DI)(R11*1), X2         // oRe
	MOVSS 4(DI)(R11*1), X3        // oIm

	// Complex multiply: t = W * odd
	MOVAPS X0, X4
	MULSS  X2, X4                  // wRe * oRe
	MOVAPS X1, X5
	MULSS  X3, X5                  // wIm * oIm
	SUBSS  X5, X4                  // tRe

	MOVAPS X0, X5
	MULSS  X3, X5                  // wRe * oIm
	MOVAPS X1, X6
	MULSS  X2, X6                  // wIm * oRe
	ADDSS  X6, X5                  // tIm

	// Load even
	MOVSS (DI)(R10*1), X2         // eRe
	MOVSS 4(DI)(R10*1), X3        // eIm

	// Butterfly
	MOVAPS X2, X6
	ADDSS  X4, X6                  // even' Re
	MOVAPS X3, X8
	ADDSS  X5, X8                  // even' Im

	SUBSS  X4, X2                  // odd' Re
	SUBSS  X5, X3                  // odd' Im

	MOVSS  X6, (DI)(R10*1)
	MOVSS  X8, 4(DI)(R10*1)
	MOVSS  X2, (DI)(R11*1)
	MOVSS  X3, 4(DI)(R11*1)

	INCQ   R9
	JMP    bf_scalar_loop

bf_done:
	RET
