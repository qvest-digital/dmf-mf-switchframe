#include "textflag.h"

// AMD64 10-bit SAD kernel for block-matching motion estimation.
//
// Computes sum(|a[i] - b[i]|) for uint16 LE samples across a bw x bh block.
// SSE2 path: processes 8 uint16 per iteration.
// No PSADBW shortcut for 16-bit — uses saturating subtract + OR for abs diff,
// then PMADDWD with 1s to widen to 32-bit before accumulating.

// ============================================================================
// func Sad10Block(a, b *byte, stride, bw, bh int) int
// ============================================================================
// Stack layout (Go ABI0):
//   a       +0(FP)     *byte
//   b       +8(FP)     *byte
//   stride  +16(FP)    int     (row stride in bytes)
//   bw      +24(FP)    int     (block width in samples)
//   bh      +32(FP)    int     (block height in samples)
//   ret     +40(FP)    int
//
// Registers:
//   SI = a ptr, DI = b ptr, R8 = stride (bytes)
//   R9 = bw (samples), R10 = bh (row counter)
//   R11 = a row ptr, R12 = b row ptr, R13 = x counter
//   R14 = simd_width (bw & ~7), AX = scalar accumulator
//   X4 = SIMD accumulator (4 x int32), X5 = ones constant (8 x int16, all 1)
//
TEXT ·Sad10Block(SB), NOSPLIT, $0-48
	MOVQ a+0(FP), SI
	MOVQ b+8(FP), DI
	MOVQ stride+16(FP), R8
	MOVQ bw+24(FP), R9
	MOVQ bh+32(FP), R10

	// Zero SIMD accumulator
	PXOR X4, X4

	// Scalar accumulator
	XORQ AX, AX

	// Prepare ones constant for PMADDWD: 8 x int16(1)
	// PMADDWD(v, ones) = pairwise horizontal add of int16 to int32
	PCMPEQL X5, X5          // all 1-bits
	PSRLW   $15, X5         // each word = 0x0001

	// Compute simd_width = bw & ~7
	MOVQ R9, R14
	ANDQ $~7, R14

	// Check if we have any rows
	TESTQ R10, R10
	JZ    sad10_done

sad10_row_loop:
	// Set up row pointers
	MOVQ SI, R11            // a_row = a
	MOVQ DI, R12            // b_row = b
	XORQ R13, R13           // x = 0

	// SIMD loop: process 8 uint16 per iteration
	CMPQ R13, R14
	JGE  sad10_scalar_tail

sad10_simd_loop:
	// Load 8 uint16 from a and b (16 bytes each)
	MOVOU (R11), X0         // a[x..x+7]
	MOVOU (R12), X1         // b[x..x+7]

	// Compute |a - b| for unsigned uint16:
	// PSUBUSW gives saturating (a-b) — zero if b > a
	// Do both directions and OR: one is always zero, other is |diff|
	MOVOU X0, X2
	PSUBUSW X1, X2          // X2 = sat(a - b)
	MOVOU X1, X3
	PSUBUSW X0, X3          // X3 = sat(b - a)
	POR   X3, X2            // X2 = |a - b| (8 x uint16)

	// Widen to 32-bit and accumulate:
	// PMADDWD with ones: pairs of uint16 → uint32 sums
	PMADDWL X5, X2          // X2 = 4 x int32 (pairwise sums)
	PADDD   X2, X4          // accumulate

	// Advance pointers by 16 bytes (8 samples * 2 bytes)
	ADDQ $16, R11
	ADDQ $16, R12
	ADDQ $8, R13
	CMPQ R13, R14
	JLT  sad10_simd_loop

sad10_scalar_tail:
	// Process remaining samples (bw % 8) one at a time
	CMPQ R13, R9
	JGE  sad10_next_row

sad10_scalar_loop:
	// Load one uint16 from each buffer
	MOVWLZX (R11), R15
	MOVWLZX (R12), CX

	// Compute |a - b|
	SUBQ CX, R15
	// Branchless abs: mask = R15 >> 63, abs = (R15 ^ mask) - mask
	MOVQ R15, CX
	SARQ $63, CX
	XORQ CX, R15
	SUBQ CX, R15
	ADDQ R15, AX

	ADDQ $2, R11
	ADDQ $2, R12
	INCQ R13
	CMPQ R13, R9
	JLT  sad10_scalar_loop

sad10_next_row:
	// Advance to next row
	ADDQ R8, SI             // a += stride
	ADDQ R8, DI             // b += stride
	DECQ R10
	JNZ  sad10_row_loop

sad10_done:
	// Reduce SIMD accumulator: sum 4 int32 lanes
	// X4 = [d, c, b, a] (4 x int32)
	MOVOU  X4, X0
	PSRLDQ $8, X0           // X0 = [0, 0, d, c]
	PADDD  X0, X4           // X4 = [_, _, b+d, a+c]
	MOVOU  X4, X0
	PSRLDQ $4, X0           // X0 = [_, _, _, b+d]
	PADDD  X0, X4           // X4[0] = a+b+c+d
	MOVL   X4, CX
	// Sign-extend 32-bit to 64-bit (values always positive, but be safe)
	MOVLQSX CX, CX
	ADDQ   CX, AX           // total = SIMD + scalar

	MOVQ AX, ret+40(FP)
	RET
