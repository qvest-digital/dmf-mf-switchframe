#include "textflag.h"

// ARM64 NEON kernel for 10-bit bilinear vertical row blend.
//
// blendRows10 blends two rows of uint16 LE samples with 16.16 fixed-point weight:
//   dst[i] = clamp10((top[i] * (65536-fy) + bot[i] * fy + 32768) >> 16)
//
// Max intermediate: 1023 * 65536 + 32768 = 67,076,096, fits in uint32.
// Uses widening multiply to 32-bit, then saturating narrow back to 16-bit.
//
// 8 samples per NEON iteration.

// --- Macros (same style as blend_10bit_kernels_arm64.s) ---

// UXTL Vd.4S, Vn.4H — zero-extend lower 4 halfwords → 4 words
#define UXTL_4S(Vd, Vn) WORD $(0x2F10A400 | ((Vn)<<5) | (Vd))

// UXTL2 Vd.4S, Vn.8H — zero-extend upper 4 halfwords → 4 words
#define UXTL2_4S(Vd, Vn) WORD $(0x6F10A400 | ((Vn)<<5) | (Vd))

// MUL Vd.4S, Vn.4S, Vm.4S — 32-bit vector multiply
#define VMUL_4S(Vd, Vn, Vm) WORD $(0x4EA09C00 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// UQXTN Vd.4H, Vn.4S — unsigned saturating narrow: 4 words → lower 4 halfwords
#define UQXTN_4H(Vd, Vn) WORD $(0x2E614800 | ((Vn)<<5) | (Vd))

// UQXTN2 Vd.8H, Vn.4S — unsigned saturating narrow: 4 words → upper 4 halfwords
#define UQXTN2_8H(Vd, Vn) WORD $(0x6E614800 | ((Vn)<<5) | (Vd))

// UMIN Vd.8H, Vn.8H, Vm.8H — unsigned minimum
#define UMIN_8H(Vd, Vn, Vm) WORD $(0x6E606C00 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// ============================================================================
// func blendRows10(dst, topRow, botRow *byte, fy, count int)
// ============================================================================
// dst[i] = clamp10((top[i] * invFy + bot[i] * fy + 32768) >> 16)
// All samples are uint16 LE. count is the number of uint16 samples.
TEXT ·blendRows10(SB), NOSPLIT, $0-40
	MOVD dst+0(FP), R0         // dst *byte
	MOVD topRow+8(FP), R1      // topRow *byte
	MOVD botRow+16(FP), R2     // botRow *byte
	MOVD fy+24(FP), R3         // fy [0..65536]
	MOVD count+32(FP), R4      // count (number of uint16 samples)

	CMP  $0, R4
	BLE  br10_done

	// Compute invFy = 65536 - fy
	MOVD $65536, R5
	SUB  R3, R5, R5            // R5 = invFy

	// Broadcast invFy and fy as 4 x uint32
	VDUP R5, V6.S4             // V6 = [invFy] x 4
	VDUP R3, V7.S4             // V7 = [fy] x 4

	// V16 = [32768] x 4 (rounding constant)
	MOVD $32768, R6
	VDUP R6, V16.S4

	// V17 = [1023] x 8 (clamp constant, uint16)
	MOVD $1023, R7
	VDUP R7, V17.H8

	// Process 8 samples per iteration
	CMP  $8, R4
	BLT  br10_tail

br10_loop8:
	// Load 8 uint16 samples from top and bottom rows
	VLD1.P 16(R1), [V0.B16]   // V0 = top[i..i+7]
	VLD1.P 16(R2), [V1.B16]   // V1 = bot[i..i+7]

	// Widen lower 4 halfwords to 4 words
	UXTL_4S(2, 0)             // V2 = top[0..3] as uint32
	UXTL_4S(3, 1)             // V3 = bot[0..3] as uint32

	// Multiply: top_lo * invFy, bot_lo * fy
	VMUL_4S(2, 2, 6)          // V2 = top_lo * invFy
	VMUL_4S(3, 3, 7)          // V3 = bot_lo * fy

	// Sum + rounding
	VADD V3.S4, V2.S4, V2.S4  // V2 = top*invFy + bot*fy
	VADD V16.S4, V2.S4, V2.S4 // V2 += 32768

	// Shift right by 16
	VUSHR $16, V2.S4, V2.S4

	// Widen upper 4 halfwords
	UXTL2_4S(4, 0)            // V4 = top[4..7]
	UXTL2_4S(5, 1)            // V5 = bot[4..7]

	// Multiply: top_hi * invFy, bot_hi * fy
	VMUL_4S(4, 4, 6)
	VMUL_4S(5, 5, 7)

	// Sum + rounding
	VADD V5.S4, V4.S4, V4.S4
	VADD V16.S4, V4.S4, V4.S4

	// Shift right by 16
	VUSHR $16, V4.S4, V4.S4

	// Narrow to uint16 (saturating)
	UQXTN_4H(8, 2)            // V8.lower = narrow(V2)
	UQXTN2_8H(8, 4)           // V8.upper = narrow(V4)

	// Clamp to [0, 1023]
	UMIN_8H(8, 8, 17)

	// Store 8 results
	VST1.P [V8.B16], 16(R0)

	SUB  $8, R4, R4
	CMP  $8, R4
	BGE  br10_loop8

br10_tail:
	CBZ  R4, br10_done

br10_tail_loop:
	// Load uint16 LE samples
	MOVHU (R1), R8             // top[i]
	MOVHU (R2), R9             // bot[i]

	// result = (top * invFy + bot * fy + 32768) >> 16
	MUL   R5, R8, R8          // R8 = top * invFy
	MUL   R3, R9, R9          // R9 = bot * fy
	ADD   R9, R8, R8          // R8 = top*invFy + bot*fy
	ADD   R6, R8, R8          // R8 += 32768
	LSR   $16, R8, R8         // R8 >>= 16

	// Clamp to 1023
	CMP   R7, R8
	CSEL  LT, R8, R7, R8

	// Store uint16 LE
	MOVHU R8, (R0)

	ADD  $2, R0
	ADD  $2, R1
	ADD  $2, R2
	SUB  $1, R4, R4
	CBNZ R4, br10_tail_loop

br10_done:
	RET
