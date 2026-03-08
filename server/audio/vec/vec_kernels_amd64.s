#include "textflag.h"

// AMD64 SIMD kernels for float32 vector operations.
// AVX (8 float32/iter) with SSE2 fallback (4 float32/iter).

// ============================================================================
// func AddFloat32(dst, src *float32, n int)
// ============================================================================
// dst[i] += src[i] for n elements.
// Each float32 = 4 bytes. AVX operates on 32 bytes (8 floats).
TEXT ·AddFloat32(SB), NOSPLIT, $0-24
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ n+16(FP), CX

	TESTQ CX, CX
	JLE   add_done

	// Check AVX2 availability (also implies AVX)
	CMPB ·avx2Available(SB), $1
	JE   add_avx

	// --- SSE2 path (4 float32/iteration) ---
	CMPQ CX, $4
	JLT  add_scalar_tail

add_sse2_loop:
	MOVUPS (DI), X0           // dst[i..i+3]
	MOVUPS (SI), X1           // src[i..i+3]
	ADDPS  X1, X0             // dst += src
	MOVUPS X0, (DI)

	ADDQ $16, DI
	ADDQ $16, SI
	SUBQ $4, CX
	CMPQ CX, $4
	JGE  add_sse2_loop
	JMP  add_scalar_tail

add_avx:
	// --- AVX path (8 float32/iteration) ---
	CMPQ CX, $8
	JLT  add_avx_cleanup

add_avx_loop:
	VMOVUPS (DI), Y0          // dst[i..i+7]
	VMOVUPS (SI), Y1          // src[i..i+7]
	VADDPS  Y1, Y0, Y0        // dst += src
	VMOVUPS Y0, (DI)

	ADDQ $32, DI
	ADDQ $32, SI
	SUBQ $8, CX
	CMPQ CX, $8
	JGE  add_avx_loop

add_avx_cleanup:
	VZEROUPPER
	// Fall through to SSE2 for remaining 4-7 elements
	CMPQ CX, $4
	JLT  add_scalar_tail

add_avx_sse2:
	MOVUPS (DI), X0
	MOVUPS (SI), X1
	ADDPS  X1, X0
	MOVUPS X0, (DI)

	ADDQ $16, DI
	ADDQ $16, SI
	SUBQ $4, CX
	CMPQ CX, $4
	JGE  add_avx_sse2

add_scalar_tail:
	TESTQ CX, CX
	JLE   add_done

add_scalar_loop:
	MOVSS (DI), X0
	MOVSS (SI), X1
	ADDSS X1, X0
	MOVSS X0, (DI)

	ADDQ $4, DI
	ADDQ $4, SI
	DECQ CX
	JNZ  add_scalar_loop

add_done:
	RET

// ============================================================================
// func ScaleFloat32(dst *float32, scale float32, n int)
// ============================================================================
// dst[i] *= scale for n elements.
// scale is at offset 8 on the stack (8 bytes for dst pointer).
// Go ABI: float32 occupies 8 bytes on the stack (padded to word size).
TEXT ·ScaleFloat32(SB), NOSPLIT, $0-24
	MOVQ  dst+0(FP), DI
	MOVSS scale+8(FP), X5     // X5 = scale (scalar)
	MOVQ  n+16(FP), CX

	TESTQ CX, CX
	JLE   scale_done

	// Check AVX2 availability
	CMPB ·avx2Available(SB), $1
	JE   scale_avx

	// --- SSE2 path (4 float32/iteration) ---
	// Broadcast scale to all 4 lanes of X6
	MOVAPS X5, X6
	SHUFPS $0, X6, X6         // X6 = [scale, scale, scale, scale]

	CMPQ CX, $4
	JLT  scale_scalar_tail

scale_sse2_loop:
	MOVUPS (DI), X0           // dst[i..i+3]
	MULPS  X6, X0             // dst *= scale
	MOVUPS X0, (DI)

	ADDQ $16, DI
	SUBQ $4, CX
	CMPQ CX, $4
	JGE  scale_sse2_loop
	JMP  scale_scalar_tail

scale_avx:
	// --- AVX path (8 float32/iteration) ---
	// Broadcast scale to all 8 lanes of Y6
	VBROADCASTSS X5, Y6       // Y6 = [scale] x 8

	CMPQ CX, $8
	JLT  scale_avx_cleanup

scale_avx_loop:
	VMOVUPS (DI), Y0          // dst[i..i+7]
	VMULPS  Y6, Y0, Y0        // dst *= scale
	VMOVUPS Y0, (DI)

	ADDQ $32, DI
	SUBQ $8, CX
	CMPQ CX, $8
	JGE  scale_avx_loop

scale_avx_cleanup:
	VZEROUPPER
	// Use SSE2 broadcast for remaining elements
	MOVAPS X5, X6
	SHUFPS $0, X6, X6

	CMPQ CX, $4
	JLT  scale_scalar_tail

scale_avx_sse2:
	MOVUPS (DI), X0
	MULPS  X6, X0
	MOVUPS X0, (DI)

	ADDQ $16, DI
	SUBQ $4, CX
	CMPQ CX, $4
	JGE  scale_avx_sse2

scale_scalar_tail:
	TESTQ CX, CX
	JLE   scale_done

scale_scalar_loop:
	MOVSS (DI), X0
	MULSS X5, X0
	MOVSS X0, (DI)

	ADDQ $4, DI
	DECQ CX
	JNZ  scale_scalar_loop

scale_done:
	RET

// ============================================================================
// func MulAddFloat32(dst, a, x, b, y *float32, n int)
// ============================================================================
// dst[i] = a[i]*x[i] + b[i]*y[i] for n elements.
// 6 args: dst+0(FP), a+8(FP), x+16(FP), b+24(FP), y+32(FP), n+40(FP)
TEXT ·MulAddFloat32(SB), NOSPLIT, $0-48
	MOVQ dst+0(FP), DI        // DI = dst
	MOVQ a+8(FP), SI          // SI = a
	MOVQ x+16(FP), DX         // DX = x
	MOVQ b+24(FP), R8         // R8 = b
	MOVQ y+32(FP), R9         // R9 = y
	MOVQ n+40(FP), CX         // CX = n

	TESTQ CX, CX
	JLE   muladd_done

	// Check AVX2 availability (also implies AVX)
	CMPB ·avx2Available(SB), $1
	JE   muladd_avx

	// --- SSE2 path (4 float32/iteration) ---
	CMPQ CX, $4
	JLT  muladd_scalar_tail

muladd_sse2_loop:
	MOVUPS (SI), X0            // X0 = a[i..i+3]
	MOVUPS (DX), X1            // X1 = x[i..i+3]
	MULPS  X1, X0              // X0 = a[i]*x[i]
	MOVUPS (R8), X2            // X2 = b[i..i+3]
	MOVUPS (R9), X3            // X3 = y[i..i+3]
	MULPS  X3, X2              // X2 = b[i]*y[i]
	ADDPS  X2, X0              // X0 = a*x + b*y
	MOVUPS X0, (DI)            // store to dst

	ADDQ $16, DI
	ADDQ $16, SI
	ADDQ $16, DX
	ADDQ $16, R8
	ADDQ $16, R9
	SUBQ $4, CX
	CMPQ CX, $4
	JGE  muladd_sse2_loop
	JMP  muladd_scalar_tail

muladd_avx:
	// --- AVX path (8 float32/iteration) ---
	CMPQ CX, $8
	JLT  muladd_avx_cleanup

muladd_avx_loop:
	VMOVUPS (SI), Y0           // Y0 = a[i..i+7]
	VMOVUPS (DX), Y1           // Y1 = x[i..i+7]
	VMULPS  Y1, Y0, Y0        // Y0 = a*x
	VMOVUPS (R8), Y2           // Y2 = b[i..i+7]
	VMOVUPS (R9), Y3           // Y3 = y[i..i+7]
	VMULPS  Y3, Y2, Y2        // Y2 = b*y
	VADDPS  Y2, Y0, Y0        // Y0 = a*x + b*y
	VMOVUPS Y0, (DI)           // store to dst

	ADDQ $32, DI
	ADDQ $32, SI
	ADDQ $32, DX
	ADDQ $32, R8
	ADDQ $32, R9
	SUBQ $8, CX
	CMPQ CX, $8
	JGE  muladd_avx_loop

muladd_avx_cleanup:
	VZEROUPPER
	// Fall through to SSE2 for remaining 4-7 elements
	CMPQ CX, $4
	JLT  muladd_scalar_tail

muladd_avx_sse2:
	MOVUPS (SI), X0
	MOVUPS (DX), X1
	MULPS  X1, X0
	MOVUPS (R8), X2
	MOVUPS (R9), X3
	MULPS  X3, X2
	ADDPS  X2, X0
	MOVUPS X0, (DI)

	ADDQ $16, DI
	ADDQ $16, SI
	ADDQ $16, DX
	ADDQ $16, R8
	ADDQ $16, R9
	SUBQ $4, CX
	CMPQ CX, $4
	JGE  muladd_avx_sse2

muladd_scalar_tail:
	TESTQ CX, CX
	JLE   muladd_done

muladd_scalar_loop:
	MOVSS (SI), X0             // a[i]
	MOVSS (DX), X1             // x[i]
	MULSS X1, X0               // a*x
	MOVSS (R8), X2             // b[i]
	MOVSS (R9), X3             // y[i]
	MULSS X3, X2               // b*y
	ADDSS X2, X0               // a*x + b*y
	MOVSS X0, (DI)             // store to dst

	ADDQ $4, DI
	ADDQ $4, SI
	ADDQ $4, DX
	ADDQ $4, R8
	ADDQ $4, R9
	DECQ CX
	JNZ  muladd_scalar_loop

muladd_done:
	RET
