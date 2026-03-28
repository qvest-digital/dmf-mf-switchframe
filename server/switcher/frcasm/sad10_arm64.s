#include "textflag.h"

// ARM64 NEON 10-bit SAD kernel for block-matching motion estimation.
//
// Computes sum(|a[i] - b[i]|) for uint16 LE samples across a bw x bh block.
// Processes 8 uint16 per iteration using NEON halfword operations.

// --- NEON instruction macros (Go assembler lacks these) ---

// UABD Vd.8H, Vn.8H, Vm.8H — unsigned absolute difference of 8 halfwords
// Encoding: 0x6E607400 | Rm<<16 | Rn<<5 | Rd
#define UABD_8H(Vd, Vn, Vm) WORD $(0x6E607400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// UADDLP Vd.4S, Vn.8H — unsigned add long pairwise: pairs of uint16 → uint32
// Encoding: 0x6E602800 | Rn<<5 | Rd
#define UADDLP_4S(Vd, Vn) WORD $(0x6E602800 | ((Vn)<<5) | (Vd))

// UADALP Vd.4S, Vn.8H — unsigned add and accumulate long pairwise
// Encoding: 0x6E606800 | Rn<<5 | Rd
#define UADALP_4S(Vd, Vn) WORD $(0x6E606800 | ((Vn)<<5) | (Vd))

// UADDLV Dd, Vn.4S — unsigned add across long vector (sum 4 uint32 → 1 uint64 in Dd)
// Encoding: 0x6EB03800 | Rn<<5 | Rd
#define UADDLV_S4(Vd, Vn) WORD $(0x6EB03800 | ((Vn)<<5) | (Vd))

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
//   R0 = a ptr, R1 = b ptr, R2 = stride (bytes)
//   R3 = bw (samples), R4 = bh (row counter)
//   R5 = a row ptr, R6 = b row ptr, R7 = x counter
//   R8 = simd_width (bw & ~7), R9 = scalar accumulator
//   V4.4S = SIMD accumulator (uint32 lanes)
//
TEXT ·Sad10Block(SB), NOSPLIT, $0-48
	MOVD a+0(FP), R0
	MOVD b+8(FP), R1
	MOVD stride+16(FP), R2
	MOVD bw+24(FP), R3
	MOVD bh+32(FP), R4

	// Zero SIMD accumulator
	VEOR V4.B16, V4.B16, V4.B16

	// Scalar accumulator for tail elements
	MOVD ZR, R9

	// Compute simd_width = bw & ~7 (number of samples processable in SIMD)
	AND  $~7, R3, R8

	// Check if we have any rows
	CBZ  R4, sad10_done

sad10_row_loop:
	// Set up row pointers
	MOVD R0, R5          // a_row = a
	MOVD R1, R6          // b_row = b
	MOVD ZR, R7          // x = 0

	// SIMD loop: process 8 uint16 per iteration
	CMP  R8, R7
	BGE  sad10_scalar_tail

sad10_simd_loop:
	// Load 8 uint16 from a and b (16 bytes each)
	VLD1 (R5), [V0.H8]
	VLD1 (R6), [V1.H8]

	// Unsigned absolute difference: V2.8H = |a - b|
	UABD_8H(2, 0, 1)

	// Pairwise add-long and accumulate into V4.4S
	// This converts pairs of uint16 diffs to uint32 sums and accumulates
	UADALP_4S(4, 2)

	// Advance pointers by 16 bytes (8 samples * 2 bytes)
	ADD  $16, R5
	ADD  $16, R6
	ADD  $8, R7
	CMP  R8, R7
	BLT  sad10_simd_loop

sad10_scalar_tail:
	// Process remaining samples (bw % 8) one at a time
	CMP  R3, R7
	BGE  sad10_next_row

sad10_scalar_loop:
	// Load one uint16 from each buffer
	MOVHU (R5), R10
	MOVHU (R6), R11

	// Compute |a - b| branchlessly
	SUB  R11, R10, R12     // R12 = a - b (signed)
	ASR  $63, R12, R13     // R13 = sign mask
	EOR  R13, R12, R12
	SUB  R13, R12, R12     // R12 = |a - b|
	ADD  R12, R9, R9       // accumulate

	ADD  $2, R5
	ADD  $2, R6
	ADD  $1, R7
	CMP  R3, R7
	BLT  sad10_scalar_loop

sad10_next_row:
	// Advance to next row
	ADD  R2, R0            // a += stride
	ADD  R2, R1            // b += stride
	SUB  $1, R4, R4
	CBNZ R4, sad10_row_loop

sad10_done:
	// Reduce SIMD accumulator: sum 4 uint32 lanes
	UADDLV_S4(4, 4)       // V4.D[0] = sum of 4 uint32 lanes
	VMOV V4.D[0], R10
	ADD  R9, R10, R0       // total = SIMD + scalar

	MOVD R0, ret+40(FP)
	RET
