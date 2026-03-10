#include "textflag.h"

// ARM64 NEON kernel for RGBA-to-YUV alpha blending (Y plane, one row).
//
// func alphaBlendRGBARowY(yRow *byte, rgba *byte, width int, alphaScale256 int)
//
// NEON path processes 8 pixels per iteration using LD4 deinterleave.
// overlayY computed at uint16 width (fits: 54*255+183*255+19*255+128=65408 < 65535).
// Blend computed at uint32 width (Y*inv + overlayY*a256 can reach 130560).

// --- NEON instruction macros ---
#define VDUP_8H(Vd, Rn) WORD $(0x4E060C00 | ((Rn)<<5) | (Vd))
#define VDUP_4S(Vd, Rn) WORD $(0x4E040C00 | ((Rn)<<5) | (Vd))
#define USHLL_8H(Vd, Vn) WORD $(0x2F08A400 | ((Vn)<<5) | (Vd))
#define USHLL_4S(Vd, Vn) WORD $(0x2F10A400 | ((Vn)<<5) | (Vd))
#define USHLL2_4S(Vd, Vn) WORD $(0x6F10A400 | ((Vn)<<5) | (Vd))
#define VMUL_8H(Vd, Vn, Vm) WORD $(0x4E609C00 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
#define VMLA_8H(Vd, Vn, Vm) WORD $(0x4E609400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
#define VADD_8H(Vd, Vn, Vm) WORD $(0x4E608400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
#define VUSHR_8H(Vd, Vn, shift) WORD $(0x6F000400 | ((32-(shift))<<16) | ((Vn)<<5) | (Vd))
#define VMUL_4S(Vd, Vn, Vm) WORD $(0x4EA09C00 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
#define VMLA_4S(Vd, Vn, Vm) WORD $(0x4EA09400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
#define VSUB_4S(Vd, Vn, Vm) WORD $(0x6EA08400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
#define VADD_4S(Vd, Vn, Vm) WORD $(0x4EA08400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
#define VUSHR_4S(Vd, Vn, shift) WORD $(0x6F200400 | ((64-(shift))<<16) | ((Vn)<<5) | (Vd))
#define VXTN_4H(Vd, Vn) WORD $(0x0E612800 | ((Vn)<<5) | (Vd))
#define VXTN2_8H(Vd, Vn) WORD $(0x4E612800 | ((Vn)<<5) | (Vd))
#define VXTN_8B(Vd, Vn) WORD $(0x0E212800 | ((Vn)<<5) | (Vd))
// LD4 {Vt.8B-Vt+3.8B}, [Xn], #32 — deinterleave RGBA into 4 channels
#define VLD4_8B_POST(Vt, Rn) WORD $(0x0CDF0000 | ((Rn)<<5) | (Vt))
// LD1 {Vt.8B}, [Xn] — load 8 bytes, no post-increment
#define VLD1_8B(Vt, Rn) WORD $(0x0C407000 | ((Rn)<<5) | (Vt))
// ST1 {Vt.8B}, [Xn], #8 — store 8 bytes with post-increment
#define VST1_8B_POST(Vt, Rn) WORD $(0x0C9F7000 | ((Rn)<<5) | (Vt))

// Register allocation:
//   R0 = yRow ptr, R1 = rgba ptr, R2 = width (loop counter), R3 = alphaScale256
//
// NEON constants:
//   V24.8H = 54 (R coeff), V25.8H = 183 (G coeff), V26.8H = 18 (B coeff)
//   V27.8H = 128 (rounding for overlayY)
//   V28.4S = alphaScale256, V29.4S = 128 (rounding for blend), V30.4S = 256

TEXT ·alphaBlendRGBARowY(SB), NOSPLIT, $0-32
	MOVD yRow+0(FP), R0
	MOVD rgba+8(FP), R1
	MOVD width+16(FP), R2
	MOVD alphaScale256+24(FP), R3

	CMP  $0, R2
	BLE  aby_done

	CMP  $8, R2
	BLT  aby_scalar

	// Set up NEON constants
	MOVD $54, R4
	VDUP_8H(24, 4)            // V24.8H = 54
	MOVD $183, R4
	VDUP_8H(25, 4)            // V25.8H = 183
	MOVD $19, R4
	VDUP_8H(26, 4)            // V26.8H = 19
	MOVD $128, R4
	VDUP_8H(27, 4)            // V27.8H = 128
	VDUP_4S(29, 4)            // V29.4S = 128
	VDUP_4S(28, 3)            // V28.4S = alphaScale256
	MOVD $256, R4
	VDUP_4S(30, 4)            // V30.4S = 256

aby_neon8:
	// Load 8 RGBA pixels, deinterleave: V0=R, V1=G, V2=B, V3=A
	VLD4_8B_POST(0, 1)       // 32 bytes from [R1], R1 += 32

	// Load existing Y[8] (don't advance yet — store back later)
	VLD1_8B(4, 0)             // V4.8B = Y[0..7]

	// Widen R, G, B, A, Y to uint16
	USHLL_8H(5, 0)            // V5.8H = R
	USHLL_8H(6, 1)            // V6.8H = G
	USHLL_8H(7, 2)            // V7.8H = B
	USHLL_8H(8, 3)            // V8.8H = A
	USHLL_8H(9, 4)            // V9.8H = Y

	// overlayY.8H = (54*R + 183*G + 19*B + 128) >> 8
	VMUL_8H(10, 5, 24)        // V10 = 54*R
	VMLA_8H(10, 6, 25)        // V10 += 183*G
	VMLA_8H(10, 7, 26)        // V10 += 19*B
	VADD_8H(10, 10, 27)       // V10 += 128
	VUSHR_8H(10, 10, 8)       // V10 = overlayY.8H

	// a256 = ((A + (A >> 7)) * alphaScale) >> 8
	VUSHR_8H(11, 8, 7)        // V11 = A >> 7
	VADD_8H(8, 8, 11)         // V8 = A' = A + (A >> 7)

	// --- Lower 4 pixels (widen to .4S for blend) ---
	USHLL_4S(12, 8)            // V12.4S = A'_lo
	VMUL_4S(12, 12, 28)       // * alphaScale
	VUSHR_4S(12, 12, 8)       // a256_lo

	VSUB_4S(13, 30, 12)       // inv_lo = 256 - a256_lo

	USHLL_4S(14, 10)           // overlayY_lo.4S
	USHLL_4S(15, 9)            // Y_lo.4S

	VMUL_4S(15, 15, 13)       // Y_lo * inv_lo
	VMLA_4S(15, 14, 12)       // + overlayY_lo * a256_lo
	VADD_4S(15, 15, 29)       // + 128
	VUSHR_4S(15, 15, 8)       // >> 8 → result_lo

	// --- Upper 4 pixels ---
	USHLL2_4S(16, 8)           // A'_hi.4S
	VMUL_4S(16, 16, 28)
	VUSHR_4S(16, 16, 8)       // a256_hi

	VSUB_4S(17, 30, 16)       // inv_hi

	USHLL2_4S(18, 10)          // overlayY_hi.4S
	USHLL2_4S(19, 9)           // Y_hi.4S

	VMUL_4S(19, 19, 17)       // Y_hi * inv_hi
	VMLA_4S(19, 18, 16)       // + overlayY_hi * a256_hi
	VADD_4S(19, 19, 29)       // + 128
	VUSHR_4S(19, 19, 8)       // >> 8 → result_hi

	// Narrow: 4S → 4H → 8B
	VXTN_4H(15, 15)           // V15.4H from V15.4S
	VXTN2_8H(15, 19)          // V15.8H = [lo, hi]
	VXTN_8B(15, 15)           // V15.8B from V15.8H

	// Store 8 result bytes, advance Y pointer
	VST1_8B_POST(15, 0)       // store, R0 += 8

	SUB  $8, R2, R2
	CMP  $8, R2
	BGE  aby_neon8

aby_scalar:
	CMP  $0, R2
	BLE  aby_done

aby_loop:
	// Load alpha byte and map 0-255 to 0-256
	MOVBU 3(R1), R4            // R4 = rgba[3] (alpha = A)
	LSR   $7, R4, R5           // R5 = A >> 7
	ADD   R5, R4, R4           // R4 = A' = A + (A >> 7)
	MUL   R3, R4, R4           // R4 = A' * alphaScale256
	LSR   $8, R4, R4           // R4 = a256

	CBZ   R4, aby_skip         // skip if transparent

	// Load R, G, B
	MOVBU (R1), R5             // R5 = R
	MOVBU 1(R1), R6            // R6 = G
	MOVBU 2(R1), R7            // R7 = B

	// overlayY = (54*R + 183*G + 19*B + 128) >> 8
	MOVD  $54, R8
	MUL   R8, R5, R8           // R8 = 54*R
	MOVD  $183, R9
	MADD  R9, R8, R6, R8       // R8 = 54*R + 183*G
	MOVD  $19, R9
	MADD  R9, R8, R7, R8       // R8 = 54*R + 183*G + 19*B
	ADD   $128, R8, R8
	LSR   $8, R8, R8           // R8 = overlayY

	// inv = 256 - a256
	MOVD  $256, R9
	SUB   R4, R9, R9           // R9 = inv

	// yRow[i] = (yRow[i]*inv + overlayY*a256 + 128) >> 8
	MOVBU (R0), R10            // R10 = yRow[i]
	MUL   R9, R10, R10         // R10 = yRow[i] * inv
	MUL   R4, R8, R8           // R8 = overlayY * a256
	ADD   R8, R10, R10
	ADD   $128, R10, R10
	LSR   $8, R10, R10
	MOVB  R10, (R0)            // store result

aby_skip:
	ADD   $4, R1, R1           // next RGBA pixel
	ADD   $1, R0, R0           // next Y pixel
	SUB   $1, R2, R2
	CBNZ  R2, aby_loop

aby_done:
	RET
