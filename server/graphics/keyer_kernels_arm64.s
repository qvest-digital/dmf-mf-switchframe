#include "textflag.h"

// ARM64 NEON + scalar assembly for chroma key mask computation.
//
// func chromaKeyMaskChroma(mask *byte, cbPlane, crPlane *byte,
//     keyCb, keyCr int, simThreshSq, totalThreshSq int, invRange int, n int)
//
// NEON path processes 16 pixels per iteration (two groups of 8).
// Two groups are interleaved to hide SMULL latency.

// --- NEON instruction macros ---
#define VDUP_8H(Vd, Rn) WORD $(0x4E060C00 | ((Rn)<<5) | (Vd))
#define VDUP_4S(Vd, Rn) WORD $(0x4E040C00 | ((Rn)<<5) | (Vd))
#define USHLL_8H(Vd, Vn) WORD $(0x2F08A400 | ((Vn)<<5) | (Vd))
#define VSUB_8H(Vd, Vn, Vm) WORD $(0x6E608400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
#define VSMULL_4S(Vd, Vn, Vm) WORD $(0x0E60C000 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
#define VSMULL2_4S(Vd, Vn, Vm) WORD $(0x4E60C000 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
#define VSMLAL_4S(Vd, Vn, Vm) WORD $(0x0E608000 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
#define VSMLAL2_4S(Vd, Vn, Vm) WORD $(0x4E608000 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
#define VCMHS_4S(Vd, Vn, Vm) WORD $(0x6EA03C00 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
#define VBIC_16B(Vd, Vn, Vm) WORD $(0x4E601C00 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
#define VBIT_16B(Vd, Vn, Vm) WORD $(0x6EA01C00 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
#define VSUB_4S(Vd, Vn, Vm) WORD $(0x6EA08400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
#define VMUL_4S(Vd, Vn, Vm) WORD $(0x4EA09C00 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
#define VUSHR_4S(Vd, Vn, shift) WORD $(0x6F200400 | ((64-(shift))<<16) | ((Vn)<<5) | (Vd))
#define VUMIN_4S(Vd, Vn, Vm) WORD $(0x6EA06C00 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
#define VXTN_4H(Vd, Vn) WORD $(0x0E612800 | ((Vn)<<5) | (Vd))
#define VXTN2_8H(Vd, Vn) WORD $(0x4E612800 | ((Vn)<<5) | (Vd))
#define VXTN_8B(Vd, Vn) WORD $(0x0E212800 | ((Vn)<<5) | (Vd))
// VLD1 {Vt.8B}, [Xn], #8 — load 8B with post-increment
#define VLD1_8B_POST(Vt, Rn) WORD $(0x0CDF7000 | ((Rn)<<5) | (Vt))
// VST1 {Vt.8B}, [Xn], #8 — store 8B with post-increment
#define VST1_8B_POST(Vt, Rn) WORD $(0x0C9F7000 | ((Rn)<<5) | (Vt))
// VLD1 {Vt.16B}, [Xn], #16 — load 16B with post-increment
#define VLD1_16B_POST(Vt, Rn) WORD $(0x4CDF7000 | ((Rn)<<5) | (Vt))
// VST1 {Vt.16B}, [Xn], #16 — store 16B with post-increment
#define VST1_16B_POST(Vt, Rn) WORD $(0x4C9F7000 | ((Rn)<<5) | (Vt))

// Register allocation:
//   R0 = mask ptr,  R1 = cbPlane ptr,  R2 = crPlane ptr
//   R3 = keyCb,     R4 = keyCr
//   R5 = simThreshSq,   R6 = totalThreshSq,   R7 = invRange
//   R8 = n (loop counter)
//
// NEON constants (V24-V29):
//   V24 = keyCb.8H,   V25 = keyCr.8H
//   V26 = simThreshSq.4S,   V27 = totalThreshSq.4S
//   V28 = invRange.4S,   V29 = 255.4S

TEXT ·chromaKeyMaskChroma(SB), NOSPLIT, $0-72
	MOVD mask+0(FP), R0
	MOVD cbPlane+8(FP), R1
	MOVD crPlane+16(FP), R2
	MOVD keyCb+24(FP), R3
	MOVD keyCr+32(FP), R4
	MOVD simThreshSq+40(FP), R5
	MOVD totalThreshSq+48(FP), R6
	MOVD invRange+56(FP), R7
	MOVD n+64(FP), R8

	CMP  $16, R8
	BLT  ckm_8_check

	// Set up NEON broadcast constants in V24-V29
	VDUP_8H(24, 3)           // V24.8H = keyCb
	VDUP_8H(25, 4)           // V25.8H = keyCr
	VDUP_4S(26, 5)           // V26.4S = simThreshSq
	VDUP_4S(27, 6)           // V27.4S = totalThreshSq
	VDUP_4S(28, 7)           // V28.4S = invRange
	MOVD $255, R9
	VDUP_4S(29, 9)           // V29.4S = 255

	// Process 16 pixels per iteration (two interleaved groups of 8)
ckm_neon16_loop:
	// --- Group A: load 8 Cb/Cr ---
	VLD1_8B_POST(0, 1)      // V0 = Cb[0..7], R1 += 8
	VLD1_8B_POST(1, 2)      // V1 = Cr[0..7], R2 += 8
	USHLL_8H(0, 0)          // V0.8H
	USHLL_8H(1, 1)          // V1.8H
	VSUB_8H(0, 0, 24)       // V0 = dCb_A
	VSUB_8H(1, 1, 25)       // V1 = dCr_A

	// --- Group B: load next 8 Cb/Cr (interleaved to hide A's latency) ---
	VLD1_8B_POST(2, 1)      // V2 = Cb[8..15], R1 += 8
	VLD1_8B_POST(3, 2)      // V3 = Cr[8..15], R2 += 8
	USHLL_8H(2, 2)
	USHLL_8H(3, 3)
	VSUB_8H(2, 2, 24)       // V2 = dCb_B
	VSUB_8H(3, 3, 25)       // V3 = dCr_B

	// --- Group A: distSq ---
	VSMULL_4S(4, 0, 0)      // V4 = dCb_A_lo^2
	VSMULL2_4S(5, 0, 0)     // V5 = dCb_A_hi^2
	// --- Group B: distSq (interleaved) ---
	VSMULL_4S(6, 2, 2)      // V6 = dCb_B_lo^2
	VSMULL2_4S(7, 2, 2)     // V7 = dCb_B_hi^2
	// --- Group A: accumulate dCr^2 ---
	VSMLAL_4S(4, 1, 1)      // V4 += dCr_A_lo^2
	VSMLAL2_4S(5, 1, 1)     // V5 += dCr_A_hi^2
	// --- Group B: accumulate dCr^2 ---
	VSMLAL_4S(6, 3, 3)      // V6 += dCr_B_lo^2
	VSMLAL2_4S(7, 3, 3)     // V7 += dCr_B_hi^2

	// --- All comparisons (A then B) ---
	VCMHS_4S(8, 4, 26)      // V8 = A_lo >= sim
	VCMHS_4S(9, 5, 26)      // V9 = A_hi >= sim
	VCMHS_4S(10, 4, 27)     // V10 = A_lo >= total
	VCMHS_4S(11, 5, 27)     // V11 = A_hi >= total
	VCMHS_4S(12, 6, 26)     // V12 = B_lo >= sim
	VCMHS_4S(13, 7, 26)     // V13 = B_hi >= sim
	VCMHS_4S(14, 6, 27)     // V14 = B_lo >= total
	VCMHS_4S(15, 7, 27)     // V15 = B_hi >= total

	// --- Smooth masks ---
	VBIC_16B(0, 8, 10)      // V0 = A smooth_lo
	VBIC_16B(1, 9, 11)      // V1 = A smooth_hi
	VBIC_16B(2, 12, 14)     // V2 = B smooth_lo
	VBIC_16B(3, 13, 15)     // V3 = B smooth_hi

	// --- Smooth values A ---
	VSUB_4S(16, 4, 26)      // V16 = A distSq_lo - sim
	VSUB_4S(17, 5, 26)      // V17 = A distSq_hi - sim
	VMUL_4S(16, 16, 28)     // *= invRange
	VMUL_4S(17, 17, 28)
	VUSHR_4S(16, 16, 16)    // >>= 16
	VUSHR_4S(17, 17, 16)
	VUMIN_4S(16, 16, 29)    // clamp 255
	VUMIN_4S(17, 17, 29)

	// --- Smooth values B ---
	VSUB_4S(18, 6, 26)      // V18 = B distSq_lo - sim
	VSUB_4S(19, 7, 26)      // V19 = B distSq_hi - sim
	VMUL_4S(18, 18, 28)
	VMUL_4S(19, 19, 28)
	VUSHR_4S(18, 18, 16)
	VUSHR_4S(19, 19, 16)
	VUMIN_4S(18, 18, 29)
	VUMIN_4S(19, 19, 29)

	// --- Build result A using BIT: start at 0, set 255 where opaque, smooth where smooth ---
	VEOR V4.B16, V4.B16, V4.B16   // result A lo = 0
	VEOR V5.B16, V5.B16, V5.B16   // result A hi = 0
	VBIT_16B(4, 29, 10)     // A_lo: 255 where opaque
	VBIT_16B(5, 29, 11)     // A_hi: 255 where opaque
	VBIT_16B(4, 16, 0)      // A_lo: smooth where smooth
	VBIT_16B(5, 17, 1)      // A_hi: smooth where smooth

	// --- Build result B ---
	VEOR V6.B16, V6.B16, V6.B16   // result B lo = 0
	VEOR V7.B16, V7.B16, V7.B16   // result B hi = 0
	VBIT_16B(6, 29, 14)     // B_lo: 255 where opaque
	VBIT_16B(7, 29, 15)     // B_hi: 255 where opaque
	VBIT_16B(6, 18, 2)      // B_lo: smooth where smooth
	VBIT_16B(7, 19, 3)      // B_hi: smooth where smooth

	// --- Narrow A: 4S → 4H → 8B ---
	VXTN_4H(4, 4)           // V4.4H = narrow(V4.4S)
	VXTN2_8H(4, 5)          // V4.8H = concat
	VXTN_8B(4, 4)           // V4.8B = narrow(V4.8H)

	// --- Narrow B: 4S → 4H → 8B ---
	VXTN_4H(6, 6)
	VXTN2_8H(6, 7)
	VXTN_8B(6, 6)

	// --- Store 16 result bytes (8 + 8) ---
	VST1_8B_POST(4, 0)      // store A, R0 += 8
	VST1_8B_POST(6, 0)      // store B, R0 += 8

	SUB  $16, R8, R8
	CMP  $16, R8
	BGE  ckm_neon16_loop

ckm_8_check:
	CMP  $8, R8
	BLT  ckm_scalar_check

	// Process remaining 8 pixels
	VLD1_8B_POST(0, 1)
	VLD1_8B_POST(1, 2)
	USHLL_8H(0, 0)
	USHLL_8H(1, 1)
	VSUB_8H(0, 0, 24)
	VSUB_8H(1, 1, 25)
	VSMULL_4S(4, 0, 0)
	VSMULL2_4S(5, 0, 0)
	VSMLAL_4S(4, 1, 1)
	VSMLAL2_4S(5, 1, 1)
	VCMHS_4S(8, 4, 26)
	VCMHS_4S(9, 5, 26)
	VCMHS_4S(10, 4, 27)
	VCMHS_4S(11, 5, 27)
	VBIC_16B(0, 8, 10)
	VBIC_16B(1, 9, 11)
	VSUB_4S(16, 4, 26)
	VSUB_4S(17, 5, 26)
	VMUL_4S(16, 16, 28)
	VMUL_4S(17, 17, 28)
	VUSHR_4S(16, 16, 16)
	VUSHR_4S(17, 17, 16)
	VUMIN_4S(16, 16, 29)
	VUMIN_4S(17, 17, 29)
	VEOR V4.B16, V4.B16, V4.B16
	VEOR V5.B16, V5.B16, V5.B16
	VBIT_16B(4, 29, 10)
	VBIT_16B(5, 29, 11)
	VBIT_16B(4, 16, 0)
	VBIT_16B(5, 17, 1)
	VXTN_4H(4, 4)
	VXTN2_8H(4, 5)
	VXTN_8B(4, 4)
	VST1_8B_POST(4, 0)
	SUB  $8, R8, R8

ckm_scalar_check:
	CMP  $0, R8
	BLE  ckm_done

ckm_loop:
	MOVBU (R1), R9
	MOVBU (R2), R10
	SUB  R3, R9, R9
	SUB  R4, R10, R10
	MUL  R9, R9, R11
	MUL  R10, R10, R12
	ADD  R12, R11, R11
	CMP  R5, R11
	BLT  ckm_transparent
	CMP  R6, R11
	BLT  ckm_smooth
	MOVD $255, R12
	MOVB R12, (R0)
	B    ckm_next

ckm_transparent:
	MOVB ZR, (R0)
	B    ckm_next

ckm_smooth:
	SUB  R5, R11, R11
	MUL  R7, R11, R11
	LSR  $16, R11, R11
	CMP  $255, R11
	BLE  ckm_smooth_store
	MOVD $255, R11

ckm_smooth_store:
	MOVB R11, (R0)

ckm_next:
	ADD  $1, R0
	ADD  $1, R1
	ADD  $1, R2
	SUB  $1, R8, R8
	CBNZ R8, ckm_loop

ckm_done:
	RET
