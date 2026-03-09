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

// ============================================================================
// func PeakAbsFloat32(data *float32, n int) float32
// ============================================================================
// Returns max(|data[i]|) for i in [0, n).
// Uses ANDPS with sign-bit mask for absolute value, MAXPS for element-wise max.
// AVX path processes 8 float32s/iter, SSE2 fallback processes 4.
//
// abs_mask = 0x7FFFFFFF (clear sign bit) replicated to all lanes.
TEXT ·PeakAbsFloat32(SB), NOSPLIT, $0-20
	MOVQ data+0(FP), DI       // DI = data
	MOVQ n+8(FP), CX          // CX = n

	// Initialize result register to 0
	XORPS X4, X4               // X4 = [0, 0, 0, 0] (accumulator)

	TESTQ CX, CX
	JLE   peak_store_amd64

	// Build abs mask: 0x7FFFFFFF in all 4 lanes
	PCMPEQL X5, X5             // X5 = all ones
	PSRLL   $1, X5             // X5 = [0x7FFFFFFF, ...] (clear sign bit)

	// Check AVX2 availability
	CMPB ·avx2Available(SB), $1
	JE   peak_avx

	// --- SSE2 path ---
	CMPQ CX, $4
	JLT  peak_scalar_amd64

peak_sse2_loop:
	MOVUPS (DI), X0            // load 4 floats
	ANDPS  X5, X0              // absolute value
	MAXPS  X0, X4              // X4 = max(X4, |data|)

	ADDQ $16, DI
	SUBQ $4, CX
	CMPQ CX, $4
	JGE  peak_sse2_loop
	JMP  peak_hmax_sse

peak_avx:
	// Widen abs mask to YMM
	// Y5 already has X5 in low 128; broadcast to high 128
	VINSERTF128 $1, X5, Y5, Y5
	VXORPS Y4, Y4, Y4         // Y4 = accumulator

	CMPQ CX, $8
	JLT  peak_avx_cleanup

peak_avx_loop:
	VMOVUPS (DI), Y0           // load 8 floats
	VANDPS  Y5, Y0, Y0        // absolute value
	VMAXPS  Y0, Y4, Y4        // accumulate max

	ADDQ $32, DI
	SUBQ $8, CX
	CMPQ CX, $8
	JGE  peak_avx_loop

peak_avx_cleanup:
	// Reduce Y4 to X4: max of high and low 128-bit halves
	VEXTRACTF128 $1, Y4, X0
	VMAXPS X0, X4, X4
	VZEROUPPER

	// Fall through to SSE horizontal max

peak_hmax_sse:
	// Horizontal max: X4 has 4 candidates
	MOVAPS X4, X0
	SHUFPS $0x4E, X0, X0      // X0 = [X4[2], X4[3], X4[0], X4[1]]
	MAXPS  X0, X4
	MOVAPS X4, X0
	SHUFPS $0xB1, X0, X0      // X0 = [X4[1], X4[0], X4[3], X4[2]]
	MAXPS  X0, X4
	// X4[0] now contains the max

	// Process remaining < 4 elements
peak_scalar_amd64:
	TESTQ CX, CX
	JLE   peak_store_amd64

peak_scalar_loop_amd64:
	MOVSS (DI), X0
	ANDPS X5, X0              // absolute value
	MAXSS X0, X4              // accumulate max

	ADDQ $4, DI
	DECQ CX
	JNZ  peak_scalar_loop_amd64

peak_store_amd64:
	MOVSS X4, ret+16(FP)
	RET

// ============================================================================
// func PeakAbsStereoFloat32(data *float32, n int) (peakL, peakR float32)
// ============================================================================
// Returns max(|data[2i]|) and max(|data[2i+1]|) for interleaved stereo data.
// n = total number of samples (must be even).
// SSE2 path uses shuffle to deinterleave left/right channels.
TEXT ·PeakAbsStereoFloat32(SB), NOSPLIT, $0-24
	MOVQ data+0(FP), DI       // DI = data
	MOVQ n+8(FP), CX          // CX = total samples

	// Initialize accumulators to 0
	XORPS X4, X4               // left max
	XORPS X5, X5               // right max

	CMPQ CX, $2
	JLT  stereo_store_amd64

	// Build abs mask: 0x7FFFFFFF
	PCMPEQL X6, X6
	PSRLL   $1, X6             // X6 = abs mask

	// Process 2 stereo pairs (4 floats) per iteration
	CMPQ CX, $4
	JLT  stereo_scalar_amd64

stereo_sse2_loop:
	MOVUPS (DI), X0            // X0 = [L0 R0 L1 R1]
	MOVAPS X0, X1              // X1 = copy
	SHUFPS $0x88, X0, X0       // X0 = [L0 L1 L0 L1] (even elements)
	SHUFPS $0xDD, X1, X1       // X1 = [R0 R1 R0 R1] (odd elements)
	ANDPS  X6, X0              // abs(left)
	ANDPS  X6, X1              // abs(right)
	MAXPS  X0, X4              // accumulate left max
	MAXPS  X1, X5              // accumulate right max

	ADDQ $16, DI
	SUBQ $4, CX
	CMPQ CX, $4
	JGE  stereo_sse2_loop

	// Horizontal max for left (X4)
	MOVAPS X4, X0
	SHUFPS $0x4E, X0, X0
	MAXPS  X0, X4
	MOVAPS X4, X0
	SHUFPS $0xB1, X0, X0
	MAXPS  X0, X4

	// Horizontal max for right (X5)
	MOVAPS X5, X0
	SHUFPS $0x4E, X0, X0
	MAXPS  X0, X5
	MOVAPS X5, X0
	SHUFPS $0xB1, X0, X0
	MAXPS  X0, X5

stereo_scalar_amd64:
	CMPQ CX, $2
	JLT  stereo_store_amd64

stereo_scalar_loop_amd64:
	MOVSS (DI), X0             // left
	MOVSS 4(DI), X1            // right
	ANDPS X6, X0
	ANDPS X6, X1
	MAXSS X0, X4
	MAXSS X1, X5

	ADDQ $8, DI
	SUBQ $2, CX
	CMPQ CX, $2
	JGE  stereo_scalar_loop_amd64

stereo_store_amd64:
	MOVSS X4, peakL+16(FP)
	MOVSS X5, peakR+20(FP)
	RET
