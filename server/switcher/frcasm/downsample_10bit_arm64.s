#include "textflag.h"

// ARM64 NEON kernel for 10-bit Y plane 2x downsample with 10→8 bit conversion.
//
// func DownsampleY10_2x(dst, src *byte, srcStride, dstW, dstH int)
//
// For each 2×2 block of uint16 LE source samples:
//   avg = (p00 + p10 + p01 + p11 + 2) >> 2   (10-bit average)
//   dst = avg >> 2                             (10→8 bit conversion)
//
// Combined: dst = (p00 + p10 + p01 + p11 + 2) >> 4
// But to match the reference exactly (which does >>2 then >>2), we replicate
// that two-step approach.
//
// NEON approach per 8 output pixels:
//   Load 16 uint16 from row0 (32 bytes): pairwise add → 8 x uint16 hsum0
//   Load 16 uint16 from row1 (32 bytes): pairwise add → 8 x uint16 hsum1
//   vsum = hsum0 + hsum1 + 2  (vertical sum with rounding)
//   avg10 = vsum >> 2         (10-bit average)
//   dst = avg10 >> 2          (narrow to 8-bit)

// UADDLP Vd.4S, Vn.8H — unsigned pairwise add + widen 8 halfwords → 4 words
#define UADDLP_4S(Vd, Vn) WORD $(0x6E602800 | ((Vn)<<5) | (Vd))

// ADDP Vd.8H, Vn.8H, Vm.8H — pairwise add (no widening, 16-bit)
#define ADDP_8H(Vd, Vn, Vm) WORD $(0x4E60BC00 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// SHRN Vd.8B, Vn.8H, #shift — shift right narrow, 8 halfwords → 8 bytes
// Encoding for 8B variant (Q=0): immh:immb encodes shift amount for 8H→8B (immh=0001, shift=16-shift)
#define SHRN_8B(Vd, Vn, shift) WORD $(0x0F008400 | (((16-(shift))&0xF)<<16) | ((Vn)<<5) | (Vd))

// USHR Vd.8H, Vn.8H, #shift — unsigned shift right 8 halfwords
// For 8H: immh:immb bits, Q=1
#define VUSHR_8H(Vd, Vn, shift) WORD $(0x6F100400 | (((16-(shift))&0xF)<<16) | ((Vn)<<5) | (Vd))

// Registers:
//   R0 = dst, R1 = src (row0), R2 = srcStride (bytes), R3 = dstW, R4 = dstH

TEXT ·DownsampleY10_2x(SB), NOSPLIT, $0-40
	MOVD dst+0(FP), R0
	MOVD src+8(FP), R1
	MOVD srcStride+16(FP), R2
	MOVD dstW+24(FP), R3
	MOVD dstH+32(FP), R4

	CBZ  R4, ds10_done
	CBZ  R3, ds10_done

	// R5 = row1 ptr (row0 + srcStride)
	ADD  R2, R1, R5             // R5 = row1

	// R7 = srcStride * 2 (to advance to next row pair)
	LSL  $1, R2, R7             // R7 = srcStride * 2

	// Rounding constant: 2 in each uint16 lane
	MOVD $2, R8
	VDUP R8, V31.H8             // V31.8H = [2] x 8

ds10_row_loop:
	MOVD R3, R6                 // R6 = remaining output cols
	MOVD R1, R9                 // R9 = row0 temp ptr
	MOVD R5, R10                // R10 = row1 temp ptr

	CMP  $8, R6
	BLT  ds10_scalar_cols

ds10_neon8:
	// Load 16 uint16 from row0 (32 bytes = 8 output pixel pairs)
	VLD1.P 32(R9), [V0.B16, V1.B16]
	// Load 16 uint16 from row1
	VLD1.P 32(R10), [V2.B16, V3.B16]

	// Horizontal pairwise add: 16 uint16 → 8 uint16 per row
	// ADDP adds adjacent pairs within each register and concatenates
	ADDP_8H(0, 0, 1)           // V0.8H = pairwise sums from row0
	ADDP_8H(2, 2, 3)           // V2.8H = pairwise sums from row1

	// Vertical sum: row0 + row1 + 2 (rounding)
	VADD V2.H8, V0.H8, V0.H8   // V0 = hsum0 + hsum1
	VADD V31.H8, V0.H8, V0.H8  // V0 += 2

	// Divide by 4 to get 10-bit average
	VUSHR_8H(0, 0, 2)          // V0 >>= 2 → 10-bit avg in uint16

	// Divide by 4 again to convert 10→8 bit, and narrow to uint8
	// Total: >>4 from source, but we split as >>2 then >>2 to match reference
	SHRN_8B(0, 0, 2)           // V0.8B = (V0.8H >> 2) narrowed to bytes

	// Store 8 output bytes
	VST1 [V0.B8], (R0)
	ADD  $8, R0

	SUB  $8, R6, R6
	CMP  $8, R6
	BGE  ds10_neon8

ds10_scalar_cols:
	CBZ  R6, ds10_row_advance

ds10_scalar_loop:
	// Read 2 uint16 from row0
	MOVHU (R9), R11             // p00
	MOVHU 2(R9), R12            // p10
	ADD   $4, R9                // advance by 2 uint16 = 4 bytes

	// Read 2 uint16 from row1
	MOVHU (R10), R13            // p01
	MOVHU 2(R10), R14           // p11
	ADD   $4, R10

	// avg = (p00 + p10 + p01 + p11 + 2) >> 2
	ADD  R11, R12, R11
	ADD  R13, R11, R11
	ADD  R14, R11, R11
	ADD  $2, R11, R11
	LSR  $2, R11, R11           // 10-bit avg

	// 10→8 bit: >> 2
	LSR  $2, R11, R11

	MOVB R11, (R0)
	ADD  $1, R0

	SUB  $1, R6, R6
	CBNZ R6, ds10_scalar_loop

ds10_row_advance:
	// Advance row pointers by 2 source rows
	ADD  R7, R1, R1             // row0 += srcStride * 2
	ADD  R2, R1, R5             // row1 = new row0 + srcStride

	SUB  $1, R4, R4
	CBNZ R4, ds10_row_loop

ds10_done:
	RET
