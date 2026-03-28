#include "textflag.h"

// ARM64 NEON kernel for 10-bit key mask blending.
//
// func keyMaskBlend10Row(bg, fill, mask *byte, count int)
//
// For each uint16 sample:
//   w = mask[i] + (mask[i] >> 7)    // 0-255 → 0-256
//   bg[i] = (bg[i] * (256-w) + fill[i] * w + 128) >> 8
//   clamp to [0, 1023]
//
// Max intermediate: 1023 * 256 + 128 = 262016, fits in uint32.
// Processing 4 samples per iteration (widened to 4 x uint32 for safe multiply).

// --- NEON instruction macros ---
// UXTL Vd.4S, Vn.4H — zero-extend lower 4 halfwords -> 4 words (USHLL #0)
#define UXTL_4S(Vd, Vn) WORD $(0x2F10A400 | ((Vn)<<5) | (Vd))

// USHLL Vd.8H, Vn.8B, #0 — zero-extend lower 8 bytes -> 8 halfwords
#define USHLL_8H(Vd, Vn) WORD $(0x2F08A400 | ((Vn)<<5) | (Vd))

// UQXTN Vd.4H, Vn.4S — unsigned saturating narrow 4 words -> lower 4 halfwords
#define UQXTN_4H(Vd, Vn) WORD $(0x2E614800 | ((Vn)<<5) | (Vd))

// UMIN Vd.4H, Vn.4H, Vm.4H — unsigned min (halfword, 64-bit form Q=0)
#define UMIN_4H(Vd, Vn, Vm) WORD $(0x2E606C00 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// MUL Vd.4S, Vn.4S, Vm.4S — 32-bit vector multiply
#define VMUL_4S(Vd, Vn, Vm) WORD $(0x4EA09C00 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// LD1 {Vt.4H}, [Xn] — load 64 bits (4 halfwords) into Vt
// Encoding: Q=0 L=1 opcode=0111 size=01: 0 0 001101 0 1 000000 0111 10 Xn Vt
#define VLD1_4H(Vt, Xn) WORD $(0x0C407800 | ((Xn)<<5) | (Vt))

// ST1 {Vt.4H}, [Xn] — store 64 bits (4 halfwords) from Vt
// Encoding: Q=0 L=0 opcode=0111 size=01: 0 0 001101 0 0 000000 0111 10 Xn Vt
#define VST1_4H(Vt, Xn) WORD $(0x0C007800 | ((Xn)<<5) | (Vt))

// Registers:
//   R0 = bg ptr, R1 = fill ptr, R2 = mask ptr, R3 = count

TEXT ·keyMaskBlend10Row(SB), NOSPLIT, $0-32
	MOVD bg+0(FP), R0
	MOVD fill+8(FP), R1
	MOVD mask+16(FP), R2
	MOVD count+24(FP), R3

	CMP  $0, R3
	BLE  km10_done

	// Constants
	MOVD $256, R4
	VDUP R4, V28.S4             // V28 = [256] x 4 (uint32)
	MOVD $128, R5
	VDUP R5, V29.S4             // V29 = [128] x 4 (uint32)
	MOVD $1023, R6
	VDUP R6, V30.H4             // V30 = [1023] x 4 (uint16, 64-bit)

	CMP  $4, R3
	BLT  km10_scalar

km10_loop4:
	// Load 4 uint16 bg samples (8 bytes) using encoded LD1
	VLD1_4H(0, 0)              // V0.4H = bg[0..3]
	VLD1_4H(1, 1)              // V1.4H = fill[0..3]

	// Load 4 mask bytes
	MOVWU (R2), R7              // R7 = 4 mask bytes packed

	// Move mask bytes into V2 and widen
	FMOVS R7, F2               // V2 lower 32 bits = 4 mask bytes
	USHLL_8H(2, 2)              // V2.8H = mask bytes zero-extended to uint16

	// w = mask + (mask >> 7) maps 0-255 to 0-256
	VUSHR $7, V2.H4, V3.H4     // V3 = mask >> 7
	VADD  V3.H4, V2.H4, V2.H4  // V2 = w (uint16)

	// Widen everything to uint32 for safe multiply
	UXTL_4S(4, 0)              // V4 = bg (4 x uint32)
	UXTL_4S(5, 1)              // V5 = fill (4 x uint32)
	UXTL_4S(6, 2)              // V6 = w (4 x uint32)

	// inv = 256 - w
	VSUB V6.S4, V28.S4, V7.S4  // V7 = inv (4 x uint32)

	// result = (bg * inv + fill * w + 128) >> 8
	VMUL_4S(4, 4, 7)           // V4 = bg * inv
	VMUL_4S(5, 5, 6)           // V5 = fill * w
	VADD V5.S4, V4.S4, V4.S4   // V4 = bg*inv + fill*w
	VADD V29.S4, V4.S4, V4.S4  // V4 += 128
	VUSHR $8, V4.S4, V4.S4     // V4 >>= 8

	// Narrow uint32 -> uint16 (saturating)
	UQXTN_4H(4, 4)             // V4.4H = narrow(V4.4S)

	// Clamp to 1023
	UMIN_4H(4, 4, 30)          // V4 = min(V4, 1023)

	// Store 4 uint16 results (8 bytes) back to bg
	VST1_4H(4, 0)

	ADD  $8, R0
	ADD  $8, R1
	ADD  $4, R2
	SUB  $4, R3, R3
	CMP  $4, R3
	BGE  km10_loop4

km10_scalar:
	CBZ  R3, km10_done

km10_scalar_loop:
	// w = mask + (mask >> 7)
	MOVBU (R2), R7              // R7 = mask[i]
	LSR   $7, R7, R8            // R8 = mask >> 7
	ADD   R8, R7, R7            // R7 = w

	CBZ   R7, km10_skip         // skip if transparent

	// inv = 256 - w
	MOVD  $256, R8
	SUB   R7, R8, R8            // R8 = inv

	// bg[i] = (bg * inv + fill * w + 128) >> 8
	MOVHU (R0), R9              // R9 = bg[i] (uint16)
	MOVHU (R1), R10             // R10 = fill[i] (uint16)
	MUL   R8, R9, R9            // R9 = bg * inv
	MUL   R7, R10, R10          // R10 = fill * w
	ADD   R10, R9, R9
	ADD   R5, R9, R9            // R9 += 128
	LSR   $8, R9, R9            // R9 >>= 8

	// Clamp to 1023
	CMP   R6, R9
	CSEL  LT, R9, R6, R9
	MOVHU R9, (R0)

km10_skip:
	ADD  $2, R0
	ADD  $2, R1
	ADD  $1, R2
	SUB  $1, R3, R3
	CBNZ R3, km10_scalar_loop

km10_done:
	RET
