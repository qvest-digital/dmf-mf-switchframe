#include "textflag.h"

// ARM64 NEON blend kernels for YUV420 frame blending.
//
// The Go ARM64 assembler lacks several NEON instructions, so we encode them
// via WORD macros. Register encoding uses 5-bit fields (V0=0 .. V31=31).
//
// Strategy: widen byte inputs to 16-bit halfwords, multiply at 16-bit width
// (max product 255*256 = 65280 fits uint16), then narrow results back to bytes.
// For blendFadeConst, widen further to 32-bit (constTerm can exceed uint16).

// --- Widening / narrowing macros ---

// UXTL Vd.8H, Vn.8B — zero-extend lower 8 bytes → 8 halfwords (USHLL #0)
#define UXTL_8H(Vd, Vn) WORD $(0x2F08A400 | ((Vn)<<5) | (Vd))

// UXTL2 Vd.8H, Vn.16B — zero-extend upper 8 bytes → 8 halfwords (USHLL2 #0)
#define UXTL2_8H(Vd, Vn) WORD $(0x6F08A400 | ((Vn)<<5) | (Vd))

// XTN Vd.8B, Vn.8H — narrow 8 halfwords → lower 8 bytes of Vd
#define XTN_8B(Vd, Vn) WORD $(0x0E212800 | ((Vn)<<5) | (Vd))

// XTN2 Vd.16B, Vn.8H — narrow 8 halfwords → upper 8 bytes of Vd
#define XTN2_16B(Vd, Vn) WORD $(0x4E212800 | ((Vn)<<5) | (Vd))

// --- 16-bit multiply macros ---

// MUL Vd.8H, Vn.8H, Vm.8H — 16-bit vector multiply
#define VMUL_8H(Vd, Vn, Vm) WORD $(0x4E609C00 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// MLA Vd.8H, Vn.8H, Vm.8H — 16-bit vector multiply-accumulate (Vd += Vn * Vm)
#define VMLA_8H(Vd, Vn, Vm) WORD $(0x4E609400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// --- 32-bit widening multiply macros (for blendFadeConst) ---

// UMULL Vd.4S, Vn.4H, Vm.4H — widening multiply lower 4 halfwords → 4 words
#define UMULL_4S(Vd, Vn, Vm) WORD $(0x2E60C000 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// UMULL2 Vd.4S, Vn.8H, Vm.8H — widening multiply upper 4 halfwords → 4 words
#define UMULL2_4S(Vd, Vn, Vm) WORD $(0x6E60C000 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// XTN Vd.4H, Vn.4S — narrow 4 words → lower 4 halfwords of Vd
#define XTN_4H(Vd, Vn) WORD $(0x0E612800 | ((Vn)<<5) | (Vd))

// XTN2 Vd.8H, Vn.4S — narrow 4 words → upper 4 halfwords of Vd
#define XTN2_8H(Vd, Vn) WORD $(0x4E612800 | ((Vn)<<5) | (Vd))


// ============================================================================
// func blendUniform(dst, a, b *byte, n, pos, inv int)
// ============================================================================
// dst[i] = (a[i]*inv + b[i]*pos) >> 8
// pos+inv = 256, max result = 255*256 = 65280, fits uint16.
// 16 bytes/iteration: widen to H8, VMUL+VMLA at 16-bit, narrow back.
TEXT ·blendUniform(SB), NOSPLIT, $0-48
	MOVD dst+0(FP), R0
	MOVD a+8(FP), R1
	MOVD b+16(FP), R2
	MOVD n+24(FP), R3
	MOVD pos+32(FP), R4
	MOVD inv+40(FP), R5

	CMP  $0, R3
	BLE  uniform_done

	// Broadcast pos and inv as halfwords
	VDUP R4, V6.H8     // V6 = [pos] × 8 halfwords
	VDUP R5, V7.H8     // V7 = [inv] × 8 halfwords

	CMP  $16, R3
	BLT  uniform_tail

uniform_loop16:
	VLD1.P 16(R1), [V0.B16]   // a[i..i+15]
	VLD1.P 16(R2), [V1.B16]   // b[i..i+15]

	// --- Lower 8 bytes ---
	UXTL_8H(2, 0)             // V2.8H = a_lo (bytes → halfwords)
	UXTL_8H(3, 1)             // V3.8H = b_lo
	VMUL_8H(2, 2, 7)          // V2 = a_lo * inv (16×16→16)
	VMLA_8H(2, 3, 6)          // V2 += b_lo * pos
	VUSHR $8, V2.H8, V2.H8   // >> 8

	// --- Upper 8 bytes ---
	UXTL2_8H(3, 0)            // V3.8H = a_hi
	UXTL2_8H(4, 1)            // V4.8H = b_hi
	VMUL_8H(3, 3, 7)          // V3 = a_hi * inv
	VMLA_8H(3, 4, 6)          // V3 += b_hi * pos
	VUSHR $8, V3.H8, V3.H8

	// Narrow halfwords → bytes
	XTN_8B(5, 2)              // V5.lower8 = narrow(V2)
	XTN2_16B(5, 3)            // V5.upper8 = narrow(V3)
	VST1.P [V5.B16], 16(R0)

	SUB  $16, R3, R3
	CMP  $16, R3
	BGE  uniform_loop16

uniform_tail:
	CBZ  R3, uniform_done

uniform_tail_loop:
	MOVBU (R1), R6
	MOVBU (R2), R7
	MUL   R5, R6, R6         // a * inv
	MUL   R4, R7, R7         // b * pos
	ADD   R7, R6, R6
	LSR   $8, R6, R6
	MOVB  R6, (R0)
	ADD   $1, R0
	ADD   $1, R1
	ADD   $1, R2
	SUB   $1, R3, R3
	CBNZ  R3, uniform_tail_loop

uniform_done:
	RET


// ============================================================================
// func blendFadeConst(dst, src *byte, n, gain, constTerm int)
// ============================================================================
// dst[i] = (src[i]*gain + constTerm) >> 8
// constTerm up to 128*256 = 32768; max sum = 98048. Needs 32-bit math.
// 16 bytes/iteration: bytes→halfwords→words, multiply+add at 32-bit, double narrow.
TEXT ·blendFadeConst(SB), NOSPLIT, $0-40
	MOVD dst+0(FP), R0
	MOVD src+8(FP), R1
	MOVD n+16(FP), R2
	MOVD gain+24(FP), R3
	MOVD constTerm+32(FP), R4

	CMP  $0, R2
	BLE  fadeconst_done

	// Broadcast gain (halfword) and constTerm (word)
	VDUP R3, V6.H8            // V6 = [gain] × 8 halfwords
	VDUP R4, V7.S4            // V7 = [constTerm] × 4 words

	CMP  $16, R2
	BLT  fadeconst_tail

fadeconst_loop16:
	VLD1.P 16(R1), [V0.B16]

	// Widen bytes → halfwords
	UXTL_8H(1, 0)             // V1.8H = src_lo (lower 8 bytes)
	UXTL2_8H(2, 0)            // V2.8H = src_hi (upper 8 bytes)

	// Group 1: lower 4 of V1 → 4 words via widening multiply
	UMULL_4S(3, 1, 6)         // V3.4S = src[0..3] * gain
	VADD V7.S4, V3.S4, V3.S4 // += constTerm

	// Group 2: upper 4 of V1 → 4 words
	UMULL2_4S(4, 1, 6)        // V4.4S = src[4..7] * gain
	VADD V7.S4, V4.S4, V4.S4

	// Group 3: lower 4 of V2 → 4 words
	UMULL_4S(5, 2, 6)         // V5.4S = src[8..11] * gain
	VADD V7.S4, V5.S4, V5.S4

	// Group 4: upper 4 of V2 → 4 words
	UMULL2_4S(16, 2, 6)       // V16.4S = src[12..15] * gain
	VADD V7.S4, V16.S4, V16.S4

	// Shift right by 8
	VUSHR $8, V3.S4, V3.S4
	VUSHR $8, V4.S4, V4.S4
	VUSHR $8, V5.S4, V5.S4
	VUSHR $8, V16.S4, V16.S4

	// Double narrow: 4S → 4H, then 8H → 8B
	XTN_4H(1, 3)              // V1.lower4H = narrow(V3.4S)
	XTN2_8H(1, 4)             // V1.upper4H = narrow(V4.4S)
	XTN_4H(2, 5)              // V2.lower4H = narrow(V5.4S)
	XTN2_8H(2, 16)            // V2.upper4H = narrow(V16.4S)

	XTN_8B(0, 1)              // V0.lower8B = narrow(V1.8H)
	XTN2_16B(0, 2)            // V0.upper8B = narrow(V2.8H)

	VST1.P [V0.B16], 16(R0)

	SUB  $16, R2, R2
	CMP  $16, R2
	BGE  fadeconst_loop16

fadeconst_tail:
	CBZ  R2, fadeconst_done

fadeconst_tail_loop:
	MOVBU (R1), R5
	MUL   R3, R5, R5
	ADD   R4, R5, R5
	LSR   $8, R5, R5
	MOVB  R5, (R0)
	ADD   $1, R0
	ADD   $1, R1
	SUB   $1, R2, R2
	CBNZ  R2, fadeconst_tail_loop

fadeconst_done:
	RET


// ============================================================================
// func blendAlpha(dst, a, b, alpha *byte, n int)
// ============================================================================
// dst[i] = (a[i]*(256-w) + b[i]*w) >> 8, w = alpha[i]+(alpha[i]>>7)
// w + inv = 256, max result = 65280, fits uint16. 16 bytes/iteration.
TEXT ·blendAlpha(SB), NOSPLIT, $0-40
	MOVD dst+0(FP), R0
	MOVD a+8(FP), R1
	MOVD b+16(FP), R2
	MOVD alpha+24(FP), R3
	MOVD n+32(FP), R4

	CMP  $0, R4
	BLE  alpha_done

	// V7 = [256] constant (8 × uint16)
	MOVD $256, R5
	VDUP R5, V7.H8

	CMP  $16, R4
	BLT  alpha_tail

alpha_loop16:
	VLD1.P 16(R1), [V0.B16]   // a
	VLD1.P 16(R2), [V1.B16]   // b
	VLD1.P 16(R3), [V2.B16]   // alpha

	// --- Lower 8 bytes ---
	UXTL_8H(8, 2)             // V8.8H = alpha_lo
	VUSHR $7, V8.H8, V9.H8   // V9 = alpha >> 7
	VADD  V9.H8, V8.H8, V8.H8 // V8 = w = alpha + (alpha >> 7)
	VSUB  V8.H8, V7.H8, V9.H8 // V9 = inv = 256 - w

	UXTL_8H(10, 0)            // V10.8H = a_lo
	UXTL_8H(11, 1)            // V11.8H = b_lo
	VMUL_8H(10, 10, 9)        // V10 = a_lo * inv
	VMLA_8H(10, 11, 8)        // V10 += b_lo * w
	VUSHR $8, V10.H8, V10.H8

	// --- Upper 8 bytes ---
	UXTL2_8H(8, 2)            // V8.8H = alpha_hi
	VUSHR $7, V8.H8, V9.H8
	VADD  V9.H8, V8.H8, V8.H8
	VSUB  V8.H8, V7.H8, V9.H8

	UXTL2_8H(11, 0)           // V11.8H = a_hi
	UXTL2_8H(12, 1)           // V12.8H = b_hi
	VMUL_8H(11, 11, 9)        // V11 = a_hi * inv
	VMLA_8H(11, 12, 8)        // V11 += b_hi * w
	VUSHR $8, V11.H8, V11.H8

	// Narrow
	XTN_8B(13, 10)             // V13.lower8 = narrow(V10)
	XTN2_16B(13, 11)           // V13.upper8 = narrow(V11)
	VST1.P [V13.B16], 16(R0)

	SUB  $16, R4, R4
	CMP  $16, R4
	BGE  alpha_loop16

alpha_tail:
	CBZ  R4, alpha_done

alpha_tail_loop:
	MOVBU (R1), R5            // a[i]
	MOVBU (R2), R6            // b[i]
	MOVBU (R3), R7            // alpha[i]
	LSR   $7, R7, R8
	ADD   R8, R7, R7         // w
	MOVD  $256, R8
	SUB   R7, R8, R8         // inv
	MUL   R8, R5, R5         // a * inv
	MUL   R7, R6, R6         // b * w
	ADD   R6, R5, R5
	LSR   $8, R5, R5
	MOVB  R5, (R0)
	ADD   $1, R0
	ADD   $1, R1
	ADD   $1, R2
	ADD   $1, R3
	SUB   $1, R4, R4
	CBNZ  R4, alpha_tail_loop

alpha_done:
	RET
