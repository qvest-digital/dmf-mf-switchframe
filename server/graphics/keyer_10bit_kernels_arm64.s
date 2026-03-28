#include "textflag.h"

// ARM64 NEON kernel for 10-bit luma key mask generation.
//
// lumaKey10Row generates a luma key mask for count uint16 LE Y samples.
// For each sample: mask = 255 if y >= lowThresh && y <= highThresh, else 0.
//
// NEON path: 8 samples per iteration (128-bit = 8 x uint16)
//   CMHS: unsigned compare higher-or-same (produces 0xFFFF for true, 0x0000 for false)
//   AND:  combine both conditions
//   XTN:  narrow 16-bit → 8-bit (0xFFFF → 0xFF, 0x0000 → 0x00)
//
// Register allocation:
//   R0 = mask ptr
//   R1 = y ptr (uint16 LE samples)
//   R2 = count (number of uint16 samples)
//   R3 = lowThresh (int, but only low 16 bits matter)
//   R4 = highThresh (int, but only low 16 bits matter)
//
// NEON registers:
//   V0  = loaded Y samples (8 x uint16)
//   V1  = CMHS result: y >= lowThresh
//   V2  = CMHS result: highThresh >= y
//   V3  = AND(V1, V2) = combined mask (16-bit)
//   V4  = narrowed mask (8-bit, lower 64 bits)
//   V5  = broadcast lowThresh (8 x uint16)
//   V6  = broadcast highThresh (8 x uint16)

// CMHS Vd.8H, Vn.8H, Vm.8H — unsigned compare higher-or-same (halfword)
// Encoding: 0x6E60_3C00 | Rm<<16 | Rn<<5 | Rd
#define CMHS_8H(Vd, Vn, Vm) WORD $(0x6E603C00 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// XTN Vd.8B, Vn.8H — extract narrow: 8 halfwords → lower 8 bytes
// Encoding: 0x0E212800 | Rn<<5 | Rd
#define XTN_8B(Vd, Vn) WORD $(0x0E212800 | ((Vn)<<5) | (Vd))

// ============================================================================
// func lumaKey10Row(mask *byte, y *byte, count int, lowThresh, highThresh int)
// ============================================================================
// Stack layout (all int = 8 bytes each):
//   mask:       0(FP)  [8 bytes]
//   y:          8(FP)  [8 bytes]
//   count:     16(FP)  [8 bytes]
//   lowThresh: 24(FP)  [8 bytes]
//   highThresh:32(FP)  [8 bytes]
// Total: 40 bytes
TEXT ·lumaKey10Row(SB), NOSPLIT, $0-40
	MOVD mask+0(FP), R0        // mask *byte
	MOVD y+8(FP), R1           // y *byte (uint16 LE samples)
	MOVD count+16(FP), R2      // count (number of uint16 samples)
	MOVD lowThresh+24(FP), R3  // lowThresh (int)
	MOVD highThresh+32(FP), R4 // highThresh (int)

	CMP  $0, R2
	BLE  lk10_done

	// Broadcast thresholds as 8 x uint16
	VDUP R3, V5.H8             // V5 = [lowThresh] x 8
	VDUP R4, V6.H8             // V6 = [highThresh] x 8

	// Process 8 samples per iteration
	CMP  $8, R2
	BLT  lk10_tail

lk10_loop8:
	// Load 8 uint16 samples (16 bytes)
	VLD1.P 16(R1), [V0.B16]   // V0 = y[i..i+7] as 8 x uint16

	// y >= lowThresh: CMHS produces 0xFFFF if true, 0x0000 if false
	CMHS_8H(1, 0, 5)          // V1 = (y >= low) ? 0xFFFF : 0x0000

	// highThresh >= y: CMHS with operands swapped
	CMHS_8H(2, 6, 0)          // V2 = (high >= y) ? 0xFFFF : 0x0000

	// Combine: both conditions must be true
	VAND V2.B16, V1.B16, V3.B16 // V3 = V1 & V2

	// Narrow from 8 x uint16 to 8 x uint8
	// 0xFFFF → 0xFF, 0x0000 → 0x00
	XTN_8B(4, 3)              // V4.8B = narrow(V3.8H)

	// Store 8 bytes of mask (V4 lower 64 bits = 8 x uint8)
	FMOVD F4, R5              // move lower 64 bits of V4 to GPR
	MOVD  R5, (R0)            // store 8 bytes

	ADD  $8, R0
	SUB  $8, R2, R2
	CMP  $8, R2
	BGE  lk10_loop8

lk10_tail:
	CBZ  R2, lk10_done

	// Scalar tail: process one sample at a time
	MOVD $255, R6             // constant for keyed output

lk10_tail_loop:
	// Load uint16 LE sample
	MOVHU (R1), R5            // R5 = y[i] (uint16, zero-extended)

	// Check: y >= lowThresh && y <= highThresh
	CMP   R3, R5              // compare y with lowThresh
	BLO   lk10_tail_zero      // unsigned lower → y < low → mask = 0
	CMP   R4, R5              // compare y with highThresh
	BHI   lk10_tail_zero      // unsigned higher → y > high → mask = 0

	// In range: mask = 255
	MOVB  R6, (R0)
	B     lk10_tail_next

lk10_tail_zero:
	MOVB  ZR, (R0)            // mask = 0

lk10_tail_next:
	ADD  $2, R1               // advance y ptr (2 bytes per uint16)
	ADD  $1, R0               // advance mask ptr (1 byte)
	SUB  $1, R2, R2
	CBNZ R2, lk10_tail_loop

lk10_done:
	RET
