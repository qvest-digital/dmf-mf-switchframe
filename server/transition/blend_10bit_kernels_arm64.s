#include "textflag.h"

// ARM64 NEON kernel for 10-bit (uint16 LE) blend.
//
// blendMix10Row blends count uint16 LE samples:
//   dst[i] = clamp10((a[i] * (256-pos) + b[i] * pos + 128) >> 8)
//
// Max intermediate: 1023 * 256 + 128 = 262016, fits in uint32.
// Uses widening multiply (UMULL/UMULL2) to compute in 32-bit, then
// saturating narrow (UQXTN/UQXTN2) back to 16-bit, followed by UMIN
// to clamp to 1023.
//
// 8 samples per NEON iteration (128-bit = 8 x uint16 source, widened to 2 x 4 x uint32).

// --- Widening / narrowing macros (same style as existing blend_kernels_arm64.s) ---

// UXTL Vd.4S, Vn.4H — zero-extend lower 4 halfwords → 4 words (USHLL #0)
#define UXTL_4S(Vd, Vn) WORD $(0x2F10A400 | ((Vn)<<5) | (Vd))

// UXTL2 Vd.4S, Vn.8H — zero-extend upper 4 halfwords → 4 words (USHLL2 #0)
#define UXTL2_4S(Vd, Vn) WORD $(0x6F10A400 | ((Vn)<<5) | (Vd))

// UMULL Vd.4S, Vn.4H, Vm.4H — unsigned widening multiply lower 4 halfwords → 4 words
#define UMULL_4S(Vd, Vn, Vm) WORD $(0x2E60C000 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// UMULL2 Vd.4S, Vn.8H, Vm.8H — unsigned widening multiply upper 4 halfwords → 4 words
#define UMULL2_4S(Vd, Vn, Vm) WORD $(0x6E60C000 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// UQXTN Vd.4H, Vn.4S — unsigned saturating extract narrow: 4 words → lower 4 halfwords
// size=01 for 32→16 narrowing (destination element size = 16-bit)
#define UQXTN_4H(Vd, Vn) WORD $(0x2E614800 | ((Vn)<<5) | (Vd))

// UQXTN2 Vd.8H, Vn.4S — unsigned saturating extract narrow: 4 words → upper 4 halfwords
// size=01 for 32→16 narrowing, Q=1 for UQXTN2
#define UQXTN2_8H(Vd, Vn) WORD $(0x6E614800 | ((Vn)<<5) | (Vd))

// UMIN Vd.8H, Vn.8H, Vm.8H — unsigned minimum (8 halfwords, size=01)
#define UMIN_8H(Vd, Vn, Vm) WORD $(0x6E606C00 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// MUL Vd.4S, Vn.4S, Vm.4S — 32-bit vector multiply
#define VMUL_4S(Vd, Vn, Vm) WORD $(0x4EA09C00 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// ============================================================================
// func blendMix10Row(dst, a, b *byte, count, pos256 int)
// ============================================================================
// dst[i] = clamp((a[i]*(256-pos) + b[i]*pos + 128) >> 8, 0, 1023)
// All samples are uint16 LE. count is the number of uint16 samples.
TEXT ·blendMix10Row(SB), NOSPLIT, $0-40
	MOVD dst+0(FP), R0         // dst *byte
	MOVD a+8(FP), R1           // a *byte
	MOVD b+16(FP), R2          // b *byte
	MOVD count+24(FP), R3      // count (number of uint16 samples)
	MOVD pos256+32(FP), R4     // pos256 [0..256]

	CMP  $0, R3
	BLE  done

	// Compute inv = 256 - pos
	MOVD $256, R5
	SUB  R4, R5, R5            // R5 = inv

	// Broadcast inv and pos as 4 x uint32 for widening multiply path
	VDUP R5, V6.S4             // V6 = [inv] x 4 (uint32)
	VDUP R4, V7.S4             // V7 = [pos] x 4 (uint32)

	// V16 = [128] x 4 (rounding constant)
	MOVD $128, R6
	VDUP R6, V16.S4

	// V17 = [1023] x 8 (clamp constant, uint16)
	MOVD $1023, R7
	VDUP R7, V17.H8

	// Process 8 samples per iteration
	CMP  $8, R3
	BLT  tail

loop8:
	// Load 8 uint16 samples (16 bytes) from a and b
	VLD1.P 16(R1), [V0.B16]   // V0 = a[i..i+7] as 8 x uint16
	VLD1.P 16(R2), [V1.B16]   // V1 = b[i..i+7] as 8 x uint16

	// Widen lower 4 halfwords to 4 words
	UXTL_4S(2, 0)             // V2.4S = a[0..3] zero-extended to uint32
	UXTL_4S(3, 1)             // V3.4S = b[0..3]

	// Multiply: a_lo * inv, b_lo * pos (32-bit)
	VMUL_4S(2, 2, 6)          // V2 = a_lo * inv
	VMUL_4S(3, 3, 7)          // V3 = b_lo * pos

	// Sum + rounding: (a*inv + b*pos + 128)
	VADD V3.S4, V2.S4, V2.S4  // V2 = a*inv + b*pos
	VADD V16.S4, V2.S4, V2.S4 // V2 += 128

	// Shift right by 8
	VUSHR $8, V2.S4, V2.S4    // V2 >>= 8

	// Widen upper 4 halfwords to 4 words
	UXTL2_4S(4, 0)            // V4.4S = a[4..7]
	UXTL2_4S(5, 1)            // V5.4S = b[4..7]

	// Multiply: a_hi * inv, b_hi * pos
	VMUL_4S(4, 4, 6)          // V4 = a_hi * inv
	VMUL_4S(5, 5, 7)          // V5 = b_hi * pos

	// Sum + rounding
	VADD V5.S4, V4.S4, V4.S4
	VADD V16.S4, V4.S4, V4.S4

	// Shift right by 8
	VUSHR $8, V4.S4, V4.S4

	// Narrow 4S → 4H (saturating), combining lo and hi
	UQXTN_4H(8, 2)            // V8.lower4H = narrow(V2.4S) [results 0..3]
	UQXTN2_8H(8, 4)           // V8.upper4H = narrow(V4.4S) [results 4..7]

	// Clamp to [0, 1023]
	UMIN_8H(8, 8, 17)         // V8 = min(V8, 1023)

	// Store 8 results
	VST1.P [V8.B16], 16(R0)

	SUB  $8, R3, R3
	CMP  $8, R3
	BGE  loop8

tail:
	CBZ  R3, done

	// Scalar tail: process one sample at a time
tail_loop:
	// Load uint16 LE sample from a
	MOVHU (R1), R8             // R8 = a[i] (uint16, zero-extended)
	MOVHU (R2), R9             // R9 = b[i]

	// result = (a * inv + b * pos + 128) >> 8
	MUL   R5, R8, R8          // R8 = a * inv
	MUL   R4, R9, R9          // R9 = b * pos
	ADD   R9, R8, R8          // R8 = a*inv + b*pos
	ADD   R6, R8, R8          // R8 += 128
	LSR   $8, R8, R8          // R8 >>= 8

	// Clamp to 1023
	CMP   R7, R8
	CSEL  LT, R8, R7, R8     // R8 = min(R8, 1023)

	// Store uint16 LE
	MOVHU R8, (R0)

	ADD  $2, R0
	ADD  $2, R1
	ADD  $2, R2
	SUB  $1, R3, R3
	CBNZ R3, tail_loop

done:
	RET
