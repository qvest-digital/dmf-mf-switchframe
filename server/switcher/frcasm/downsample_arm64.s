#include "textflag.h"

// ARM64 NEON 2× Y-plane downsample using box filter.
//
// Algorithm: For each 2×2 source block, compute:
//   avg(avg(row0[2c], row0[2c+1]), avg(row1[2c], row1[2c+1]))
// using URHADD for (a+b+1)>>1 rounding (matches PAVGB hardware semantics).
//
// NEON processes 16 output pixels per iteration:
//   Load 32 bytes from row0 (V0, V1) and row1 (V2, V3).
//   UZP1/UZP2 deinterleave even/odd bytes.
//   URHADD for horizontal average, then vertical average.

// --- NEON instruction macros ---

// UZP1 Vd.16B, Vn.16B, Vm.16B — unzip primary (even elements)
// Encoding: 0x4E001800 | Rm<<16 | Rn<<5 | Rd
#define UZP1_16B(Vd, Vn, Vm) WORD $(0x4E001800 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// UZP2 Vd.16B, Vn.16B, Vm.16B — unzip secondary (odd elements)
// Encoding: 0x4E005800 | Rm<<16 | Rn<<5 | Rd
#define UZP2_16B(Vd, Vn, Vm) WORD $(0x4E005800 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// URHADD Vd.16B, Vn.16B, Vm.16B — unsigned rounding halving add
// Computes (a + b + 1) >> 1 per byte.
// Encoding: 0x6E201400 | Rm<<16 | Rn<<5 | Rd
#define URHADD_16B(Vd, Vn, Vm) WORD $(0x6E201400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))


// ============================================================================
// func DownsampleY2x(dst, src *byte, srcW, srcH int)
// ============================================================================
// Stack layout (Go ABI0):
//   dst    +0(FP)    *byte
//   src    +8(FP)    *byte
//   srcW   +16(FP)   int
//   srcH   +24(FP)   int
//
// Registers:
//   R0  = dst pointer (advances)
//   R1  = src row0 pointer
//   R2  = src row1 pointer (= row0 + srcW)
//   R3  = srcW
//   R4  = dstW (= srcW / 2)
//   R5  = remaining output rows (= srcH / 2)
//   R6  = remaining output cols in current row
//   R7  = srcW * 2 (stride to next row pair)
//
TEXT ·DownsampleY2x(SB), NOSPLIT, $0-32
	MOVD dst+0(FP), R0          // dst pointer
	MOVD src+8(FP), R1          // src pointer (= row0)
	MOVD srcW+16(FP), R3        // source width
	MOVD srcH+24(FP), R5        // source height

	// dstW = srcW / 2
	LSR  $1, R3, R4             // R4 = srcW >> 1 = dstW

	// dstH = srcH / 2
	LSR  $1, R5, R5             // R5 = srcH >> 1 = dstH (= remaining rows)

	// srcW * 2 = stride for advancing src past two rows
	LSL  $1, R3, R7             // R7 = srcW * 2

	// row1 = row0 + srcW
	ADD  R3, R1, R2             // R2 = src + srcW = row1

	CBZ  R5, ds_done            // no rows to process

ds_row_loop:
	// Process one pair of source rows → one output row
	MOVD R4, R6                 // R6 = remaining output cols

	// --- NEON loop: 16 output pixels per iteration ---
	CMP  $16, R6
	BLT  ds_tail

ds_neon_loop:
	// Load 32 bytes from row0 (16 pixel pairs)
	VLD1.P 16(R1), [V0.B16]    // row0 bytes 0-15
	VLD1.P 16(R1), [V1.B16]    // row0 bytes 16-31

	// Load 32 bytes from row1 (16 pixel pairs)
	VLD1.P 16(R2), [V2.B16]    // row1 bytes 0-15
	VLD1.P 16(R2), [V3.B16]    // row1 bytes 16-31

	// Deinterleave row0: even pixels (0,2,4,...) and odd pixels (1,3,5,...)
	UZP1_16B(4, 0, 1)          // V4 = even bytes from row0
	UZP2_16B(5, 0, 1)          // V5 = odd bytes from row0

	// Horizontal average of row0
	URHADD_16B(6, 4, 5)        // V6 = avg(row0_even, row0_odd)

	// Deinterleave row1
	UZP1_16B(7, 2, 3)          // V7 = even bytes from row1
	UZP2_16B(16, 2, 3)         // V16 = odd bytes from row1

	// Horizontal average of row1
	URHADD_16B(17, 7, 16)      // V17 = avg(row1_even, row1_odd)

	// Vertical average
	URHADD_16B(18, 6, 17)      // V18 = final 2×2 box average

	// Store 16 output pixels
	VST1.P [V18.B16], 16(R0)

	SUB  $16, R6, R6
	CMP  $16, R6
	BGE  ds_neon_loop

ds_tail:
	// Scalar tail for remaining < 16 output pixels
	CBZ  R6, ds_row_advance

ds_scalar_loop:
	// Load 2 bytes from row0
	MOVBU (R1), R8              // row0[2c]
	MOVBU 1(R1), R9             // row0[2c+1]
	ADD  $2, R1                 // advance row0 by 2

	// Load 2 bytes from row1
	MOVBU (R2), R10             // row1[2c]
	MOVBU 1(R2), R11            // row1[2c+1]
	ADD  $2, R2                 // advance row1 by 2

	// Cascaded URHADD rounding: avg(avg(a,b), avg(c,d))
	ADD  R8, R9, R8             // R8 = a + b
	ADD  $1, R8, R8             // R8 = a + b + 1
	LSR  $1, R8, R8             // R8 = (a + b + 1) >> 1 = havg0

	ADD  R10, R11, R10          // R10 = c + d
	ADD  $1, R10, R10           // R10 = c + d + 1
	LSR  $1, R10, R10           // R10 = (c + d + 1) >> 1 = havg1

	ADD  R8, R10, R8            // R8 = havg0 + havg1
	ADD  $1, R8, R8             // R8 = havg0 + havg1 + 1
	LSR  $1, R8, R8             // R8 = final average

	MOVB R8, (R0)
	ADD  $1, R0                 // advance dst

	SUB  $1, R6, R6
	CBNZ R6, ds_scalar_loop

ds_row_advance:
	// After processing dstW output pixels, row0/row1 have advanced by dstW*2 = srcW bytes.
	// Need to advance to next row pair: row0 += srcW (skip the row1 we just read),
	// row1 = row0 + srcW.
	// Current R1 = original_row0 + srcW, R2 = original_row1 + srcW.
	// Next row0 = original_row0 + 2*srcW = R2 (which is current R2).
	// Next row1 = next_row0 + srcW.
	MOVD R2, R1                 // row0 = old row1 + srcW = old src + 2*srcW
	ADD  R3, R1, R2             // row1 = new row0 + srcW

	SUB  $1, R5, R5
	CBNZ R5, ds_row_loop

ds_done:
	RET
