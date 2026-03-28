#include "textflag.h"

// AMD64 AVX2/scalar kernel for 10-bit luma key mask generation.
//
// lumaKey10Row generates a luma key mask for count uint16 LE Y samples.
// For each sample: mask = 255 if y >= lowThresh && y <= highThresh, else 0.
//
// AVX2 path: 16 samples per iteration (256-bit = 16 x uint16)
//   VPMAXUW + VPCMPEQW: y >= low (max(y,low)==y iff y>=low)
//   VPMINUW + VPCMPEQW: y <= high (min(y,high)==y iff y<=high)
//   VPAND:  combine conditions
//   VPSRLW: shift 0xFFFF → 0x00FF for correct pack
//   VPACKUSWB + VPERMQ: narrow 16-bit mask to 8-bit
//
// Scalar fallback for non-AVX2 and tail.

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
	MOVQ mask+0(FP), DI         // mask *byte
	MOVQ y+8(FP), SI            // y *byte (uint16 LE samples)
	MOVQ count+16(FP), CX       // count (number of uint16 samples)
	MOVQ lowThresh+24(FP), R8   // lowThresh (int)
	MOVQ highThresh+32(FP), R9  // highThresh (int)

	TESTQ CX, CX
	JLE   lk10_done

	// Check AVX2 availability
	CMPB ·avx2Available10(SB), $1
	JE   lk10_avx2

	// --- Scalar path ---
	JMP  lk10_scalar

lk10_avx2:
	// Broadcast low and high as 16 x uint16 in YMM
	MOVQ    R8, X4
	VPBROADCASTW X4, Y4        // Y4 = [lowThresh] x 16
	MOVQ    R9, X5
	VPBROADCASTW X5, Y5        // Y5 = [highThresh] x 16

	CMPQ    CX, $16
	JLT     lk10_avx2_tail

lk10_avx2_loop:
	// Load 16 uint16 samples (32 bytes)
	VMOVDQU (SI), Y0           // Y0 = y[i..i+15] as 16 x uint16

	// y >= low: VPMAXUW(y, low) == y iff y >= low
	VPMAXUW Y4, Y0, Y1         // Y1 = max(y, low)
	VPCMPEQW Y0, Y1, Y1        // Y1 = 0xFFFF where max(y,low)==y, i.e. y >= low

	// y <= high: VPMINUW(y, high) == y iff y <= high
	VPMINUW Y5, Y0, Y2         // Y2 = min(y, high)
	VPCMPEQW Y0, Y2, Y2        // Y2 = 0xFFFF where min(y,high)==y, i.e. y <= high

	// Combine: both conditions
	VPAND   Y2, Y1, Y3         // Y3 = (y>=low) & (y<=high), 16 x uint16 mask

	// Convert 0xFFFF → 0x00FF for correct unsigned pack
	VPSRLW  $8, Y3, Y3         // Y3 >>= 8: 0xFFFF → 0x00FF, 0x0000 → 0x0000

	// Pack 16-bit → 8-bit with unsigned saturation
	VPXOR   Y6, Y6, Y6         // Y6 = zero
	VPACKUSWB Y6, Y3, Y3       // Pack: lane0=[r0..r7,0..0], lane1=[r8..r15,0..0]
	VPERMQ  $0xD8, Y3, Y3      // Fix lane order: [r0..r7,r8..r15,0..0,0..0]

	// Store 16 bytes of mask (lower 128 bits of Y3)
	VMOVDQU X3, (DI)

	ADDQ    $32, SI            // advance y ptr (16 samples * 2 bytes)
	ADDQ    $16, DI            // advance mask ptr (16 bytes)
	SUBQ    $16, CX
	CMPQ    CX, $16
	JGE     lk10_avx2_loop

lk10_avx2_tail:
	VZEROUPPER
	TESTQ   CX, CX
	JZ      lk10_done
	// Fall through to scalar for remaining samples

lk10_scalar:
	// R8 = lowThresh, R9 = highThresh

lk10_scalar_loop:
	// Load uint16 LE
	MOVWLZX (SI), AX           // AX = y[i] (uint16, zero-extended)

	// Check: y >= low && y <= high
	CMPW    AX, R8
	JB      lk10_scalar_zero   // unsigned below → y < low
	CMPW    AX, R9
	JA      lk10_scalar_zero   // unsigned above → y > high

	// In range: mask = 255
	MOVB    $255, (DI)
	JMP     lk10_scalar_next

lk10_scalar_zero:
	MOVB    $0, (DI)

lk10_scalar_next:
	ADDQ    $2, SI             // advance y ptr
	ADDQ    $1, DI             // advance mask ptr
	DECQ    CX
	JNZ     lk10_scalar_loop

lk10_done:
	RET
