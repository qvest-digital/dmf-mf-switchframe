#include "textflag.h"

// ARM64 NEON kernels for float32 vector operations.
// Processes 4 float32s per iteration (128-bit NEON registers).
//
// The Go ARM64 assembler lacks several NEON float instructions, so we encode
// them via WORD macros. Register encoding uses 5-bit fields (V0=0 .. V31=31).

// --- Float32 NEON macros ---
// FADD Vd.4S, Vn.4S, Vm.4S — vector float add
#define FADD_4S(Vd, Vn, Vm) WORD $(0x4E20D400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
// FMUL Vd.4S, Vn.4S, Vm.4S — vector float multiply
#define FMUL_4S(Vd, Vn, Vm) WORD $(0x6E20DC00 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
// DUP Vd.4S, Vn.S[0] — broadcast element 0 of Vn to all 4 lanes of Vd
#define VDUP_S0_4S(Vd, Vn) WORD $(0x4E040400 | ((Vn)<<5) | (Vd))

// ============================================================================
// func AddFloat32(dst, src *float32, n int)
// ============================================================================
// dst[i] += src[i] for n elements.
TEXT ·AddFloat32(SB), NOSPLIT, $0-24
	MOVD dst+0(FP), R0
	MOVD src+8(FP), R1
	MOVD n+16(FP), R2

	CMP  $0, R2
	BLE  add_done

	CMP  $4, R2
	BLT  add_scalar

add_neon_loop:
	VLD1 (R0), [V0.S4]       // dst[i..i+3]
	VLD1 (R1), [V1.S4]       // src[i..i+3]
	FADD_4S(0, 0, 1)         // V0 = V0 + V1
	VST1 [V0.S4], (R0)       // store result

	ADD  $16, R0
	ADD  $16, R1
	SUB  $4, R2, R2
	CMP  $4, R2
	BGE  add_neon_loop

add_scalar:
	CMP  $0, R2
	BLE  add_done

add_scalar_loop:
	FMOVS (R0), F0
	FMOVS (R1), F1
	FADDS F1, F0, F0
	FMOVS F0, (R0)

	ADD  $4, R0
	ADD  $4, R1
	SUB  $1, R2, R2
	CMP  $0, R2
	BGT  add_scalar_loop

add_done:
	RET

// ============================================================================
// func ScaleFloat32(dst *float32, scale float32, n int)
// ============================================================================
// dst[i] *= scale for n elements.
// scale is at offset +8 (after dst pointer), n at offset +16.
TEXT ·ScaleFloat32(SB), NOSPLIT, $0-24
	MOVD  dst+0(FP), R0
	FMOVS scale+8(FP), F5    // F5 = scale (scalar in V5.S[0])
	MOVD  n+16(FP), R2

	CMP  $0, R2
	BLE  scale_done

	// Broadcast V5.S[0] to all 4 lanes of V5.4S
	VDUP_S0_4S(5, 5)          // V5.4S = [scale, scale, scale, scale]

	CMP  $4, R2
	BLT  scale_scalar

scale_neon_loop:
	VLD1 (R0), [V0.S4]       // dst[i..i+3]
	FMUL_4S(0, 0, 5)         // V0 = V0 * V5
	VST1 [V0.S4], (R0)       // store result

	ADD  $16, R0
	SUB  $4, R2, R2
	CMP  $4, R2
	BGE  scale_neon_loop

scale_scalar:
	CMP  $0, R2
	BLE  scale_done

	// Reload scalar scale from V5.S[0] (VDUP modified the vector but F5 is V5.S[0])
scale_scalar_loop:
	FMOVS (R0), F0
	FMULS F5, F0, F0
	FMOVS F0, (R0)

	ADD  $4, R0
	SUB  $1, R2, R2
	CMP  $0, R2
	BGT  scale_scalar_loop

scale_done:
	RET

// ============================================================================
// func MulAddFloat32(dst, a, x, b, y *float32, n int)
// ============================================================================
// dst[i] = a[i]*x[i] + b[i]*y[i] for n elements.
// 6 args: dst+0(FP), a+8(FP), x+16(FP), b+24(FP), y+32(FP), n+40(FP)
TEXT ·MulAddFloat32(SB), NOSPLIT, $0-48
	MOVD dst+0(FP), R0         // R0 = dst
	MOVD a+8(FP), R1           // R1 = a
	MOVD x+16(FP), R2          // R2 = x
	MOVD b+24(FP), R3          // R3 = b
	MOVD y+32(FP), R4          // R4 = y
	MOVD n+40(FP), R5          // R5 = n

	CMP  $0, R5
	BLE  muladd_done

	CMP  $4, R5
	BLT  muladd_scalar

muladd_neon_loop:
	VLD1 (R1), [V0.S4]        // V0 = a[i..i+3]
	VLD1 (R2), [V1.S4]        // V1 = x[i..i+3]
	FMUL_4S(0, 0, 1)          // V0 = a * x
	VLD1 (R3), [V2.S4]        // V2 = b[i..i+3]
	VLD1 (R4), [V3.S4]        // V3 = y[i..i+3]
	FMUL_4S(2, 2, 3)          // V2 = b * y
	FADD_4S(0, 0, 2)          // V0 = a*x + b*y
	VST1 [V0.S4], (R0)        // store to dst

	ADD  $16, R0
	ADD  $16, R1
	ADD  $16, R2
	ADD  $16, R3
	ADD  $16, R4
	SUB  $4, R5, R5
	CMP  $4, R5
	BGE  muladd_neon_loop

muladd_scalar:
	CMP  $0, R5
	BLE  muladd_done

muladd_scalar_loop:
	FMOVS (R1), F0             // a[i]
	FMOVS (R2), F1             // x[i]
	FMULS F1, F0, F0           // a*x
	FMOVS (R3), F2             // b[i]
	FMOVS (R4), F3             // y[i]
	FMULS F3, F2, F2           // b*y
	FADDS F2, F0, F0           // a*x + b*y
	FMOVS F0, (R0)             // store to dst

	ADD  $4, R0
	ADD  $4, R1
	ADD  $4, R2
	ADD  $4, R3
	ADD  $4, R4
	SUB  $1, R5, R5
	CMP  $0, R5
	BGT  muladd_scalar_loop

muladd_done:
	RET
