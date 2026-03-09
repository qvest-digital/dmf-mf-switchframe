#include "textflag.h"

// AMD64 SSE/AVX2 butterfly for radix-2 FFT stage.
//
// func butterflyRadix2(data, twiddle []float32, halfN, twiddleStride int)
//
// AVX2 path: 4 butterflies/iteration (8 complex floats = 32 bytes per half).
// SSE path: 2 butterflies/iteration (4 complex floats = 16 bytes per half).
// Scalar fallback for twiddleStride != 1.

// Sign mask for SSE complex multiply:
// XOR with [signbit, 0, signbit, 0] negates real parts after swap.
DATA sign_neg_re<>+0x00(SB)/4, $0x80000000
DATA sign_neg_re<>+0x04(SB)/4, $0x00000000
DATA sign_neg_re<>+0x08(SB)/4, $0x80000000
DATA sign_neg_re<>+0x0C(SB)/4, $0x00000000
// Duplicate for AVX2 (32-byte aligned)
DATA sign_neg_re<>+0x10(SB)/4, $0x80000000
DATA sign_neg_re<>+0x14(SB)/4, $0x00000000
DATA sign_neg_re<>+0x18(SB)/4, $0x80000000
DATA sign_neg_re<>+0x1C(SB)/4, $0x00000000
GLOBL sign_neg_re<>(SB), RODATA|NOPTR, $32

TEXT ·butterflyRadix2(SB), NOSPLIT, $0-64
	MOVQ data_base+0(FP), DI
	MOVQ twiddle_base+24(FP), SI
	MOVQ halfN+48(FP), DX
	MOVQ twiddleStride+56(FP), R8

	TESTQ DX, DX
	JLE  bf_done

	// Check if we can use SIMD path (twiddleStride == 1)
	CMPQ R8, $1
	JNE  bf_scalar_init

	// halfN * 8 = byte offset from even to odd
	MOVQ DX, R12
	SHLQ $3, R12

	// Check AVX2 availability
	CMPB ·avx2Available(SB), $1
	JE   bf_avx2_init

	// --- SSE path (2 butterflies/iteration) ---
	MOVOU sign_neg_re<>(SB), X7
	XORQ R9, R9

	MOVQ DX, R13
	SUBQ $1, R13
	CMPQ R9, R13
	JGE  bf_sse_tail

bf_sse_loop:
	MOVQ R9, R10
	SHLQ $3, R10
	MOVQ R10, R11
	ADDQ R12, R11

	MOVQ R9, R13
	SHLQ $3, R13
	MOVUPS (SI)(R13*1), X0        // twiddle [wRe0, wIm0, wRe1, wIm1]
	MOVUPS (DI)(R10*1), X1        // even [eRe0, eIm0, eRe1, eIm1]
	MOVUPS (DI)(R11*1), X2        // odd [oRe0, oIm0, oRe1, oIm1]

	// Complex multiply: t = W * odd
	MOVAPS X0, X3
	SHUFPS $0xA0, X3, X3          // [wRe0, wRe0, wRe1, wRe1]
	MOVAPS X0, X4
	SHUFPS $0xF5, X4, X4          // [wIm0, wIm0, wIm1, wIm1]

	MOVAPS X2, X5
	MULPS  X3, X5                  // wRe * odd

	MOVAPS X2, X6
	SHUFPS $0xB1, X6, X6          // swap re/im
	MULPS  X4, X6                  // wIm * swapped
	XORPS  X7, X6                  // negate real parts

	ADDPS  X6, X5                  // t = W * odd

	// Butterfly
	MOVAPS X1, X3
	ADDPS  X5, X3                  // even + t
	MOVAPS X1, X4
	SUBPS  X5, X4                  // even - t

	MOVUPS X3, (DI)(R10*1)
	MOVUPS X4, (DI)(R11*1)

	ADDQ   $2, R9
	MOVQ   DX, R13
	SUBQ   $1, R13
	CMPQ   R9, R13
	JLT    bf_sse_loop

bf_sse_tail:
	CMPQ R9, DX
	JGE  bf_done

	// Single scalar butterfly (SSE tail)
	MOVQ R9, R10
	SHLQ $3, R10
	MOVQ R10, R11
	ADDQ R12, R11
	MOVQ R9, R13
	SHLQ $3, R13

	MOVSS (SI)(R13*1), X0         // wRe
	MOVSS 4(SI)(R13*1), X1        // wIm
	MOVSS (DI)(R11*1), X2         // oRe
	MOVSS 4(DI)(R11*1), X3        // oIm

	MOVAPS X0, X4
	MULSS  X2, X4
	MOVAPS X1, X5
	MULSS  X3, X5
	SUBSS  X5, X4                  // tRe

	MOVAPS X0, X5
	MULSS  X3, X5
	MOVAPS X1, X6
	MULSS  X2, X6
	ADDSS  X6, X5                  // tIm

	MOVSS (DI)(R10*1), X2         // eRe
	MOVSS 4(DI)(R10*1), X3        // eIm

	MOVAPS X2, X6
	ADDSS  X4, X6
	MOVAPS X3, X8
	ADDSS  X5, X8
	SUBSS  X4, X2
	SUBSS  X5, X3

	MOVSS  X6, (DI)(R10*1)
	MOVSS  X8, 4(DI)(R10*1)
	MOVSS  X2, (DI)(R11*1)
	MOVSS  X3, 4(DI)(R11*1)

	JMP    bf_done

	// --- AVX2 path (4 butterflies/iteration) ---
bf_avx2_init:
	VMOVUPS sign_neg_re<>(SB), Y7
	XORQ R9, R9

	// Need at least 4 butterflies for AVX2
	MOVQ DX, R13
	SUBQ $3, R13
	CMPQ R9, R13
	JGE  bf_avx2_cleanup

bf_avx2_loop:
	MOVQ R9, R10
	SHLQ $3, R10
	MOVQ R10, R11
	ADDQ R12, R11

	MOVQ R9, R13
	SHLQ $3, R13

	// Load 4 twiddle pairs (8 floats)
	VMOVUPS (SI)(R13*1), Y0       // [wRe0,wIm0, wRe1,wIm1, wRe2,wIm2, wRe3,wIm3]
	// Load 4 even complex values
	VMOVUPS (DI)(R10*1), Y1       // [eRe0,eIm0, eRe1,eIm1, eRe2,eIm2, eRe3,eIm3]
	// Load 4 odd complex values
	VMOVUPS (DI)(R11*1), Y2       // [oRe0,oIm0, oRe1,oIm1, oRe2,oIm2, oRe3,oIm3]

	// Complex multiply: t = W * odd
	// Duplicate real parts: SHUFPS $0xA0 within 128-bit lanes
	VSHUFPS $0xA0, Y0, Y0, Y3    // [wRe0,wRe0, wRe1,wRe1, wRe2,wRe2, wRe3,wRe3]
	// Duplicate imag parts
	VSHUFPS $0xF5, Y0, Y0, Y4    // [wIm0,wIm0, wIm1,wIm1, wIm2,wIm2, wIm3,wIm3]

	VMULPS Y2, Y3, Y5             // wRe * odd

	// Swap re/im pairs within 128-bit lanes
	VSHUFPS $0xB1, Y2, Y2, Y6    // swap re/im
	VMULPS  Y6, Y4, Y6            // wIm * swapped
	VXORPS  Y7, Y6, Y6            // negate real parts

	VADDPS  Y6, Y5, Y5            // t = W * odd

	// Butterfly
	VADDPS  Y5, Y1, Y3            // even + t
	VSUBPS  Y5, Y1, Y4            // even - t

	VMOVUPS Y3, (DI)(R10*1)
	VMOVUPS Y4, (DI)(R11*1)

	ADDQ   $4, R9
	MOVQ   DX, R13
	SUBQ   $3, R13
	CMPQ   R9, R13
	JLT    bf_avx2_loop

bf_avx2_cleanup:
	VZEROUPPER

	// Fall through to SSE for remaining 0-3 butterflies
	MOVOU sign_neg_re<>(SB), X7

	MOVQ DX, R13
	SUBQ $1, R13
	CMPQ R9, R13
	JGE  bf_sse_tail
	JMP  bf_sse_loop

bf_scalar_init:
	// --- Scalar path (twiddleStride != 1) ---
	MOVQ DX, R12
	SHLQ $3, R12
	XORQ R9, R9

bf_scalar_loop:
	CMPQ R9, DX
	JGE  bf_done

	MOVQ R9, R13
	IMULQ R8, R13
	SHLQ $3, R13

	MOVQ R9, R10
	SHLQ $3, R10
	MOVQ R10, R11
	ADDQ R12, R11

	MOVSS (SI)(R13*1), X0
	MOVSS 4(SI)(R13*1), X1
	MOVSS (DI)(R11*1), X2
	MOVSS 4(DI)(R11*1), X3

	MOVAPS X0, X4
	MULSS  X2, X4
	MOVAPS X1, X5
	MULSS  X3, X5
	SUBSS  X5, X4

	MOVAPS X0, X5
	MULSS  X3, X5
	MOVAPS X1, X6
	MULSS  X2, X6
	ADDSS  X6, X5

	MOVSS (DI)(R10*1), X2
	MOVSS 4(DI)(R10*1), X3

	MOVAPS X2, X6
	ADDSS  X4, X6
	MOVAPS X3, X8
	ADDSS  X5, X8
	SUBSS  X4, X2
	SUBSS  X5, X3

	MOVSS  X6, (DI)(R10*1)
	MOVSS  X8, 4(DI)(R10*1)
	MOVSS  X2, (DI)(R11*1)
	MOVSS  X3, 4(DI)(R11*1)

	INCQ   R9
	JMP    bf_scalar_loop

bf_done:
	RET
