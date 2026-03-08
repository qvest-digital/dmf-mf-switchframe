#include "textflag.h"

// AMD64 blend kernels for YUV420 frame blending.
// AVX2 (32 bytes/iter) with SSE2 fallback (16 bytes/iter).
//
// Strategy: widen bytes to 16-bit words using PUNPCKLBW/PUNPCKHBW with zero
// register, multiply at 16-bit width with PMULLW, then narrow back with
// PACKUSWB. AVX2 uses VEX-encoded 256-bit instructions.

// ============================================================================
// func blendUniform(dst, a, b *byte, n, pos, inv int)
// ============================================================================
// dst[i] = (a[i]*inv + b[i]*pos) >> 8
TEXT ·blendUniform(SB), NOSPLIT, $0-48
	MOVQ dst+0(FP), DI
	MOVQ a+8(FP), SI
	MOVQ b+16(FP), DX
	MOVQ n+24(FP), CX
	MOVQ pos+32(FP), R8
	MOVQ inv+40(FP), R9

	TESTQ CX, CX
	JLE   uniform_done

	// Check AVX2 availability
	CMPB ·avx2Available(SB), $1
	JE   uniform_avx2

	// --- SSE2 path (16 bytes/iteration) ---
	// Broadcast pos and inv to XMM registers as 16-bit words
	MOVQ    R8, X6
	PSHUFLW $0, X6, X6
	PUNPCKLQDQ X6, X6           // X6 = [pos] × 8

	MOVQ    R9, X7
	PSHUFLW $0, X7, X7
	PUNPCKLQDQ X7, X7           // X7 = [inv] × 8

	PXOR    X5, X5              // X5 = zero register

	CMPQ    CX, $16
	JLT     uniform_sse2_tail

uniform_sse2_loop:
	MOVOU   (SI), X0            // a[i..i+15]
	MOVOU   (DX), X1            // b[i..i+15]

	// Lower 8 bytes: widen to 16-bit
	MOVO    X0, X2
	PUNPCKLBW X5, X2            // X2 = a_lo (zero-extended to words)
	MOVO    X1, X3
	PUNPCKLBW X5, X3            // X3 = b_lo

	PMULLW  X7, X2              // X2 = a_lo * inv
	PMULLW  X6, X3              // X3 = b_lo * pos
	PADDW   X3, X2              // X2 = sum_lo
	PSRLW   $8, X2              // X2 >>= 8

	// Upper 8 bytes: widen to 16-bit
	MOVO    X0, X3
	PUNPCKHBW X5, X3            // X3 = a_hi
	MOVO    X1, X4
	PUNPCKHBW X5, X4            // X4 = b_hi

	PMULLW  X7, X3              // X3 = a_hi * inv
	PMULLW  X6, X4              // X4 = b_hi * pos
	PADDW   X4, X3              // X3 = sum_hi
	PSRLW   $8, X3              // X3 >>= 8

	// Narrow and store
	PACKUSWB X3, X2             // X2 = pack(lo, hi) → 16 bytes
	MOVOU   X2, (DI)

	ADDQ    $16, SI
	ADDQ    $16, DX
	ADDQ    $16, DI
	SUBQ    $16, CX
	CMPQ    CX, $16
	JGE     uniform_sse2_loop

uniform_sse2_tail:
	TESTQ   CX, CX
	JZ      uniform_done

uniform_scalar_loop:
	MOVBLZX (SI), AX
	MOVBLZX (DX), BX
	IMULQ   R9, AX              // a * inv
	IMULQ   R8, BX              // b * pos
	ADDQ    BX, AX
	SHRQ    $8, AX
	MOVB    AX, (DI)
	INCQ    SI
	INCQ    DX
	INCQ    DI
	DECQ    CX
	JNZ     uniform_scalar_loop
	JMP     uniform_done

uniform_avx2:
	// --- AVX2 path (32 bytes/iteration) ---
	// Broadcast pos and inv to YMM registers
	MOVQ    R8, X6
	VPBROADCASTW X6, Y6         // Y6 = [pos] × 16
	MOVQ    R9, X7
	VPBROADCASTW X7, Y7         // Y7 = [inv] × 16
	VPXOR   Y5, Y5, Y5          // Y5 = zero

	CMPQ    CX, $32
	JLT     uniform_avx2_tail

uniform_avx2_loop:
	VMOVDQU (SI), Y0            // a[i..i+31]
	VMOVDQU (DX), Y1            // b[i..i+31]

	// Lower 16 bytes → 16 words
	VPUNPCKLBW Y5, Y0, Y2       // Y2 = a_lo (interleaved with zeros)
	VPUNPCKLBW Y5, Y1, Y3       // Y3 = b_lo
	VPMULLW Y7, Y2, Y2          // Y2 = a_lo * inv
	VPMULLW Y6, Y3, Y3          // Y3 = b_lo * pos
	VPADDW  Y3, Y2, Y2          // Y2 = sum_lo
	VPSRLW  $8, Y2, Y2

	// Upper 16 bytes → 16 words
	VPUNPCKHBW Y5, Y0, Y3       // Y3 = a_hi
	VPUNPCKHBW Y5, Y1, Y4       // Y4 = b_hi
	VPMULLW Y7, Y3, Y3
	VPMULLW Y6, Y4, Y4
	VPADDW  Y4, Y3, Y3
	VPSRLW  $8, Y3, Y3

	// Pack 16-bit words back to bytes.
	// VPUNPCKLBW/VPUNPCKHBW split lanes as [0-7|16-23] and [8-15|24-31],
	// so VPACKUSWB recombines them into correct order [0-15|16-31].
	VPACKUSWB Y3, Y2, Y2
	VMOVDQU Y2, (DI)

	ADDQ    $32, SI
	ADDQ    $32, DX
	ADDQ    $32, DI
	SUBQ    $32, CX
	CMPQ    CX, $32
	JGE     uniform_avx2_loop

uniform_avx2_tail:
	VZEROUPPER
	// Fall through to SSE2/scalar for remaining bytes
	TESTQ   CX, CX
	JZ      uniform_done
	CMPQ    CX, $16
	JLT     uniform_scalar_loop_avx2_tail

	// Use SSE2 for 16-byte chunks
	MOVQ    R8, X6
	PSHUFLW $0, X6, X6
	PUNPCKLQDQ X6, X6
	MOVQ    R9, X7
	PSHUFLW $0, X7, X7
	PUNPCKLQDQ X7, X7
	PXOR    X5, X5

uniform_avx2_sse2_tail:
	MOVOU   (SI), X0
	MOVOU   (DX), X1
	MOVO    X0, X2
	PUNPCKLBW X5, X2
	MOVO    X1, X3
	PUNPCKLBW X5, X3
	PMULLW  X7, X2
	PMULLW  X6, X3
	PADDW   X3, X2
	PSRLW   $8, X2
	MOVO    X0, X3
	PUNPCKHBW X5, X3
	MOVO    X1, X4
	PUNPCKHBW X5, X4
	PMULLW  X7, X3
	PMULLW  X6, X4
	PADDW   X4, X3
	PSRLW   $8, X3
	PACKUSWB X3, X2
	MOVOU   X2, (DI)
	ADDQ    $16, SI
	ADDQ    $16, DX
	ADDQ    $16, DI
	SUBQ    $16, CX
	CMPQ    CX, $16
	JGE     uniform_avx2_sse2_tail

uniform_scalar_loop_avx2_tail:
	TESTQ   CX, CX
	JZ      uniform_done
	MOVBLZX (SI), AX
	MOVBLZX (DX), BX
	IMULQ   R9, AX
	IMULQ   R8, BX
	ADDQ    BX, AX
	SHRQ    $8, AX
	MOVB    AX, (DI)
	INCQ    SI
	INCQ    DX
	INCQ    DI
	DECQ    CX
	JNZ     uniform_scalar_loop_avx2_tail

uniform_done:
	RET


// ============================================================================
// func blendFadeConst(dst, src *byte, n, gain, constTerm int)
// ============================================================================
// dst[i] = (src[i]*gain + constTerm) >> 8
// Needs 32-bit math (max sum = 98048).
// SSE2 lacks PMULLD so falls back to scalar. AVX2 uses VPMULLD.
TEXT ·blendFadeConst(SB), NOSPLIT, $0-40
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ n+16(FP), CX
	MOVQ gain+24(FP), R8
	MOVQ constTerm+32(FP), R9

	TESTQ CX, CX
	JLE   fadeconst_done

	// Check AVX2 availability
	CMPB ·avx2Available(SB), $1
	JE   fadeconst_avx2

	// --- SSE2 path: no PMULLD, use scalar ---
fadeconst_scalar:
	TESTQ   CX, CX
	JZ      fadeconst_done

fadeconst_scalar_loop:
	MOVBLZX (SI), AX
	IMULQ   R8, AX              // src * gain
	ADDQ    R9, AX              // + constTerm
	SHRQ    $8, AX              // >> 8
	MOVB    AX, (DI)
	INCQ    SI
	INCQ    DI
	DECQ    CX
	JNZ     fadeconst_scalar_loop
	JMP     fadeconst_done

fadeconst_avx2:
	// --- AVX2 path (16 bytes/iteration) ---
	// Broadcast gain and constTerm as dwords for VPMULLD
	MOVQ    R8, X6
	VPBROADCASTD X6, Y6         // Y6 = [gain] × 8 dwords
	MOVQ    R9, X7
	VPBROADCASTD X7, Y7         // Y7 = [constTerm] × 8 dwords

	CMPQ    CX, $16
	JLT     fadeconst_avx2_tail

fadeconst_avx2_loop:
	VMOVDQU (SI), X0            // load 16 bytes

	// Widen bytes → 16 words
	VPMOVZXBW X0, Y1            // Y1 = 16 words from 16 bytes

	// Group 1: lower 8 words → 8 dwords
	VEXTRACTI128 $0, Y1, X2     // X2 = lower 8 words
	VPMOVZXWD X2, Y2            // Y2 = 8 dwords
	VPMULLD Y6, Y2, Y2          // Y2 = src * gain (32-bit)
	VPADDD  Y7, Y2, Y2          // Y2 += constTerm
	VPSRLD  $8, Y2, Y2          // Y2 >>= 8

	// Group 2: upper 8 words → 8 dwords
	VEXTRACTI128 $1, Y1, X3     // X3 = upper 8 words
	VPMOVZXWD X3, Y3            // Y3 = 8 dwords
	VPMULLD Y6, Y3, Y3
	VPADDD  Y7, Y3, Y3
	VPSRLD  $8, Y3, Y3

	// Double narrow: 8 dwords → 8 words, then 16 words → 16 bytes
	VPACKUSDW Y3, Y2, Y2        // Y2 = 16 words (lane-interleaved)
	VPERMQ  $0xD8, Y2, Y2       // fix VPACKUSDW lane ordering
	// Extract upper lane FIRST to avoid aliasing: X2 = lower Y2,
	// so writing X2 via VEX zeros Y2[255:128].
	VEXTRACTI128 $1, Y2, X3
	VEXTRACTI128 $0, Y2, X2
	PACKUSWB X3, X2             // X2 = 16 result bytes
	MOVOU   X2, (DI)

	ADDQ    $16, SI
	ADDQ    $16, DI
	SUBQ    $16, CX
	CMPQ    CX, $16
	JGE     fadeconst_avx2_loop

fadeconst_avx2_tail:
	VZEROUPPER
	TESTQ   CX, CX
	JZ      fadeconst_done
	JMP     fadeconst_scalar_loop

fadeconst_done:
	RET


// ============================================================================
// func blendAlpha(dst, a, b, alpha *byte, n int)
// ============================================================================
// dst[i] = (a[i]*(256-w) + b[i]*w) >> 8, w = alpha[i]+(alpha[i]>>7)
TEXT ·blendAlpha(SB), NOSPLIT, $0-40
	MOVQ dst+0(FP), DI
	MOVQ a+8(FP), SI
	MOVQ b+16(FP), DX
	MOVQ alpha+24(FP), R10
	MOVQ n+32(FP), CX

	TESTQ CX, CX
	JLE   alpha_done

	// Check AVX2
	CMPB ·avx2Available(SB), $1
	JE   alpha_avx2

	// --- SSE2 path ---
	// X7 = [256] × 8 words
	MOVQ    $256, AX
	MOVQ    AX, X7
	PSHUFLW $0, X7, X7
	PUNPCKLQDQ X7, X7

	PXOR    X6, X6              // zero

	CMPQ    CX, $16
	JLT     alpha_sse2_tail

alpha_sse2_loop:
	MOVOU   (SI), X0            // a
	MOVOU   (DX), X1            // b
	MOVOU   (R10), X2           // alpha

	// --- Lower 8 bytes ---
	MOVO    X2, X3
	PUNPCKLBW X6, X3            // X3 = alpha_lo (words)
	MOVO    X3, X4
	PSRLW   $7, X4              // X4 = alpha >> 7
	PADDW   X4, X3              // X3 = w = alpha + (alpha >> 7)
	MOVO    X7, X4
	PSUBW   X3, X4              // X4 = inv = 256 - w

	MOVO    X0, X8
	PUNPCKLBW X6, X8            // X8 = a_lo
	MOVO    X1, X9
	PUNPCKLBW X6, X9            // X9 = b_lo
	PMULLW  X4, X8              // X8 = a_lo * inv
	PMULLW  X3, X9              // X9 = b_lo * w
	PADDW   X9, X8              // X8 = sum_lo
	PSRLW   $8, X8

	// --- Upper 8 bytes ---
	MOVO    X2, X3
	PUNPCKHBW X6, X3            // X3 = alpha_hi
	MOVO    X3, X4
	PSRLW   $7, X4
	PADDW   X4, X3
	MOVO    X7, X4
	PSUBW   X3, X4

	MOVO    X0, X9
	PUNPCKHBW X6, X9            // X9 = a_hi
	MOVO    X1, X10
	PUNPCKHBW X6, X10           // X10 = b_hi
	PMULLW  X4, X9
	PMULLW  X3, X10
	PADDW   X10, X9
	PSRLW   $8, X9

	PACKUSWB X9, X8
	MOVOU   X8, (DI)

	ADDQ    $16, SI
	ADDQ    $16, DX
	ADDQ    $16, R10
	ADDQ    $16, DI
	SUBQ    $16, CX
	CMPQ    CX, $16
	JGE     alpha_sse2_loop

alpha_sse2_tail:
	TESTQ   CX, CX
	JZ      alpha_done

alpha_scalar_loop:
	MOVBLZX (SI), AX            // a
	MOVBLZX (DX), BX            // b
	MOVBLZX (R10), R11          // alpha
	MOVQ    R11, R12
	SHRQ    $7, R12             // alpha >> 7
	ADDQ    R12, R11            // w
	MOVQ    $256, R12
	SUBQ    R11, R12            // inv
	IMULQ   R12, AX             // a * inv
	IMULQ   R11, BX             // b * w
	ADDQ    BX, AX
	SHRQ    $8, AX
	MOVB    AX, (DI)
	INCQ    SI
	INCQ    DX
	INCQ    R10
	INCQ    DI
	DECQ    CX
	JNZ     alpha_scalar_loop
	JMP     alpha_done

alpha_avx2:
	// --- AVX2 path (32 bytes/iteration) ---
	MOVQ    $256, AX
	MOVQ    AX, X7
	VPBROADCASTW X7, Y7         // Y7 = [256] × 16 words
	VPXOR   Y6, Y6, Y6

	CMPQ    CX, $32
	JLT     alpha_avx2_tail

alpha_avx2_loop:
	VMOVDQU (SI), Y0            // a
	VMOVDQU (DX), Y1            // b
	VMOVDQU (R10), Y2           // alpha

	// Lower 16: widen
	VPUNPCKLBW Y6, Y2, Y3       // Y3 = alpha_lo
	VPSRLW  $7, Y3, Y4          // Y4 = alpha >> 7
	VPADDW  Y4, Y3, Y3          // Y3 = w
	VPSUBW  Y3, Y7, Y4          // Y4 = inv

	VPUNPCKLBW Y6, Y0, Y8       // Y8 = a_lo
	VPUNPCKLBW Y6, Y1, Y9       // Y9 = b_lo
	VPMULLW Y4, Y8, Y8          // a_lo * inv
	VPMULLW Y3, Y9, Y9          // b_lo * w
	VPADDW  Y9, Y8, Y8
	VPSRLW  $8, Y8, Y8

	// Upper 16: widen
	VPUNPCKHBW Y6, Y2, Y3
	VPSRLW  $7, Y3, Y4
	VPADDW  Y4, Y3, Y3
	VPSUBW  Y3, Y7, Y4

	VPUNPCKHBW Y6, Y0, Y9
	VPUNPCKHBW Y6, Y1, Y10
	VPMULLW Y4, Y9, Y9
	VPMULLW Y3, Y10, Y10
	VPADDW  Y10, Y9, Y9
	VPSRLW  $8, Y9, Y9

	// VPUNPCKLBW/VPUNPCKHBW + VPACKUSWB already produces correct order.
	VPACKUSWB Y9, Y8, Y8
	VMOVDQU Y8, (DI)

	ADDQ    $32, SI
	ADDQ    $32, DX
	ADDQ    $32, R10
	ADDQ    $32, DI
	SUBQ    $32, CX
	CMPQ    CX, $32
	JGE     alpha_avx2_loop

alpha_avx2_tail:
	VZEROUPPER
	TESTQ   CX, CX
	JZ      alpha_done
	// Fall through to SSE2 for remaining 16+ bytes, or scalar for < 16
	CMPQ    CX, $16
	JLT     alpha_scalar_loop

	// Set up SSE2 constants for tail
	MOVQ    $256, AX
	MOVQ    AX, X7
	PSHUFLW $0, X7, X7
	PUNPCKLQDQ X7, X7
	PXOR    X6, X6
	JMP     alpha_sse2_loop

alpha_done:
	RET
