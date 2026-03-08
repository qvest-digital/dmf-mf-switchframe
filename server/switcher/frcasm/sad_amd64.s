#include "textflag.h"

// AMD64 SAD kernels for block-matching motion estimation and scene detection.
// AVX2 path (32 bytes/iter) with SSE2 fallback (16 bytes/iter).
//
// PSADBW computes the sum of absolute differences of packed unsigned byte
// integers in two XMM registers and produces two 64-bit partial sums.

// ============================================================================
// func SadBlock16x16(a, b *byte, aStride, bStride int) uint32
// ============================================================================
// Processes 16 rows, each 16 bytes wide. Uses PSADBW for SAD computation.
// Each PSADBW produces two 64-bit partial sums (low 8 bytes and high 8 bytes).
TEXT ·SadBlock16x16(SB), NOSPLIT, $0-36
	MOVQ a+0(FP), SI           // block A pointer
	MOVQ b+8(FP), DI           // block B pointer
	MOVQ aStride+16(FP), R8    // A stride
	MOVQ bStride+24(FP), R9    // B stride

	PXOR X2, X2                // accumulator = 0
	MOVQ $16, CX               // 16 rows

sad16x16_loop:
	MOVOU (SI), X0              // load 16 bytes from A row
	MOVOU (DI), X1              // load 16 bytes from B row
	PSADBW X1, X0               // X0 = [sad_hi64, sad_lo64]
	PADDQ X0, X2                // accumulate both partial sums
	ADDQ R8, SI                 // advance A by stride
	ADDQ R9, DI                 // advance B by stride
	DECQ CX
	JNZ  sad16x16_loop

	// Horizontal add: X2 has two 64-bit partial sums
	MOVHLPS X2, X0              // X0 = high qword of X2
	PADDQ   X0, X2              // X2[0] = total SAD
	MOVQ    X2, AX
	MOVL    AX, ret+32(FP)     // return as uint32
	RET


// ============================================================================
// func SadRow(a, b *byte, n int) uint64
// ============================================================================
// Computes sum(|a[i] - b[i]|) across n bytes.
// AVX2 path: VPSADBW at 32 bytes/iter. SSE2: PSADBW at 16 bytes/iter.
// Scalar tail for remaining bytes.
TEXT ·SadRow(SB), NOSPLIT, $0-32
	MOVQ a+0(FP), SI           // pointer to a
	MOVQ b+8(FP), DI           // pointer to b
	MOVQ n+16(FP), CX          // byte count

	XORQ AX, AX                // scalar accumulator = 0

	TESTQ CX, CX
	JLE   sadrow_done

	// Check AVX2 availability
	CMPB ·avx2Available(SB), $1
	JE   sadrow_avx2

	// --- SSE2 path (16 bytes/iteration) ---
	PXOR X2, X2                // SIMD accumulator = 0

	CMPQ CX, $16
	JLT  sadrow_sse2_tail

sadrow_sse2_loop:
	MOVOU  (SI), X0             // load 16 bytes from a
	MOVOU  (DI), X1             // load 16 bytes from b
	PSADBW X1, X0               // X0 = two 64-bit partial SAD sums
	PADDQ  X0, X2               // accumulate
	ADDQ   $16, SI
	ADDQ   $16, DI
	SUBQ   $16, CX
	CMPQ   CX, $16
	JGE    sadrow_sse2_loop

sadrow_sse2_tail:
	// Reduce SIMD accumulator to scalar
	MOVHLPS X2, X0
	PADDQ   X0, X2
	MOVQ    X2, AX              // AX = SIMD total

	// Scalar tail for remaining < 16 bytes
	TESTQ CX, CX
	JZ    sadrow_done

sadrow_scalar_loop:
	MOVBLZX (SI), R8
	MOVBLZX (DI), R9
	SUBQ    R9, R8
	// Branchless absolute value: mask = R8 >> 63, abs = (R8 ^ mask) - mask
	MOVQ    R8, R10
	SARQ    $63, R10            // R10 = sign mask (0 or -1)
	XORQ    R10, R8
	SUBQ    R10, R8             // R8 = |a[i] - b[i]|
	ADDQ    R8, AX
	INCQ    SI
	INCQ    DI
	DECQ    CX
	JNZ     sadrow_scalar_loop
	JMP     sadrow_done

sadrow_avx2:
	// --- AVX2 path (32 bytes/iteration) ---
	VPXOR Y2, Y2, Y2           // SIMD accumulator = 0

	CMPQ CX, $32
	JLT  sadrow_avx2_tail

sadrow_avx2_loop:
	VMOVDQU (SI), Y0            // load 32 bytes from a
	VMOVDQU (DI), Y1            // load 32 bytes from b
	VPSADBW Y1, Y0, Y0          // Y0 = four 64-bit partial SAD sums
	VPADDQ  Y0, Y2, Y2          // accumulate
	ADDQ    $32, SI
	ADDQ    $32, DI
	SUBQ    $32, CX
	CMPQ    CX, $32
	JGE     sadrow_avx2_loop

sadrow_avx2_tail:
	// Reduce 256-bit accumulator: extract high 128 bits
	VEXTRACTI128 $1, Y2, X0
	VPADDQ       X0, X2, X2    // X2 = 128-bit partial sum
	VZEROUPPER

	// Reduce 128-bit to 64-bit
	MOVHLPS X2, X0
	PADDQ   X0, X2
	MOVQ    X2, AX              // AX = SIMD total

	// Fall through to SSE2/scalar for remaining bytes
	TESTQ CX, CX
	JZ    sadrow_done

	CMPQ CX, $16
	JLT  sadrow_scalar_loop

	// SSE2 path for 16-byte chunks after AVX2
	PXOR X2, X2

sadrow_avx2_sse2_tail:
	MOVOU  (SI), X0
	MOVOU  (DI), X1
	PSADBW X1, X0
	PADDQ  X0, X2
	ADDQ   $16, SI
	ADDQ   $16, DI
	SUBQ   $16, CX
	CMPQ   CX, $16
	JGE    sadrow_avx2_sse2_tail

	// Reduce SSE2 accumulator and add to scalar total
	MOVHLPS X2, X0
	PADDQ   X0, X2
	MOVQ    X2, R8
	ADDQ    R8, AX

	TESTQ CX, CX
	JZ    sadrow_done
	JMP   sadrow_scalar_loop

sadrow_done:
	MOVQ AX, ret+24(FP)
	RET
