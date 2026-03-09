#include "textflag.h"

// ARM64 NEON kernel for chroma spill suppression.
//
// func spillSuppressChroma(cbPlane *byte, crPlane *byte,
//     keyCb float32, keyCr float32, spillSuppress float32,
//     invSpillDistSq float32, replaceCb float32, replaceCr float32, n int)

// --- NEON macros ---
// UCVTF Vd.4S, Vn.4S — unsigned int32 to float32
#define UCVTF_4S(Vd, Vn)   WORD $(0x6E21D800 | ((Vn)<<5) | (Vd))
// FCVTZU Vd.4S, Vn.4S — float32 to unsigned int32 (round toward zero)
#define FCVTZU_4S(Vd, Vn)  WORD $(0x6EA1B800 | ((Vn)<<5) | (Vd))
// FMUL Vd.4S, Vn.4S, Vm.4S — float32 multiply
#define FMUL_4S(Vd, Vn, Vm)  WORD $(0x6E20DC00 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
// FMLA Vd.4S, Vn.4S, Vm.4S — Vd += Vn * Vm (fused multiply-add)
#define FMLA_4S(Vd, Vn, Vm)  WORD $(0x4E20CC00 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
// FMLS Vd.4S, Vn.4S, Vm.4S — Vd -= Vn * Vm (fused multiply-sub)
#define FMLS_4S(Vd, Vn, Vm)  WORD $(0x4EA0CC00 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
// FSUB Vd.4S, Vn.4S, Vm.4S — float32 subtract
#define FSUB_4S(Vd, Vn, Vm)  WORD $(0x4EA0D400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
// FADD Vd.4S, Vn.4S, Vm.4S — float32 add
#define FADD_4S(Vd, Vn, Vm)  WORD $(0x4E20D400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
// FCMGT Vd.4S, Vn.4S, Vm.4S — per-lane Vn > Vm ? all-ones : 0
#define FCMGT_4S(Vd, Vn, Vm) WORD $(0x6E20E400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
// FCMGT Vd.4S, Vn.4S, #0.0 — per-lane Vn > 0 ? all-ones : 0
#define FCMGT_4S_Z(Vd, Vn)   WORD $(0x4EA0C800 | ((Vn)<<5) | (Vd))
// FMIN Vd.4S, Vn.4S, Vm.4S
#define FMIN_4S(Vd, Vn, Vm)  WORD $(0x4EA0F400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
// FMAX Vd.4S, Vn.4S, Vm.4S
#define FMAX_4S(Vd, Vn, Vm)  WORD $(0x4E20F400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
// BIF Vd, Vn, Vm — bit insert if false: Vd[i] = Vm[i]==0 ? Vn[i] : Vd[i]
#define BIF(Vd, Vn, Vm)      WORD $(0x6EE01C00 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
// BSL Vd, Vn, Vm — bit select: Vd = (Vd & Vn) | (~Vd & Vm)
#define BSL(Vd, Vn, Vm)      WORD $(0x6E601C00 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
// BIT Vd, Vn, Vm — bit insert if true: Vd[i] = Vm[i]==1 ? Vn[i] : Vd[i]
#define BIT(Vd, Vn, Vm)      WORD $(0x6EA01C00 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
// USHLL Vd.8H, Vn.8B, #0 — zero-extend bytes to uint16
#define USHLL_8H(Vd, Vn) WORD $(0x2F08A400 | ((Vn)<<5) | (Vd))
// USHLL Vd.4S, Vn.4H, #0 — zero-extend uint16 to uint32
#define USHLL_4S(Vd, Vn) WORD $(0x2F10A400 | ((Vn)<<5) | (Vd))
// UQXTN Vd.4H, Vn.4S — unsigned saturating narrow uint32→uint16
#define UQXTN_4H(Vd, Vn) WORD $(0x2E614800 | ((Vn)<<5) | (Vd))
// UQXTN Vd.8B, Vn.8H — unsigned saturating narrow uint16→uint8
#define UQXTN_8B(Vd, Vn) WORD $(0x2E214800 | ((Vn)<<5) | (Vd))

// Registers:
//   R0 = cbPlane, R1 = crPlane
//   F0 = keyCb, F1 = keyCr, F2 = spillSuppress, F3 = invSpillDistSq
//   F4 = replaceCb, F5 = replaceCr
//   R2 = n
//
// Vector constants (set up once):
//   V20 = keyCb broadcast
//   V21 = keyCr broadcast
//   V22 = spillSuppress broadcast
//   V23 = invSpillDistSq broadcast
//   V24 = replaceCb broadcast
//   V25 = replaceCr broadcast
//   V26 = 1.0 broadcast
//   V27 = 255.0 broadcast
//   V28 = 0.0 (zero)
TEXT ·spillSuppressChroma(SB), NOSPLIT, $0-48
	MOVD cbPlane+0(FP), R0
	MOVD crPlane+8(FP), R1
	FMOVS keyCb+16(FP), F0
	FMOVS keyCr+20(FP), F1
	FMOVS spillSuppress+24(FP), F2
	FMOVS invSpillDistSq+28(FP), F3
	FMOVS replaceCb+32(FP), F4
	FMOVS replaceCr+36(FP), F5
	MOVD n+40(FP), R2

	CMP  $0, R2
	BLE  spill_done

	// Broadcast scalar constants to vectors
	VDUP V0.S[0], V20.S4    // keyCb
	VDUP V1.S[0], V21.S4    // keyCr
	VDUP V2.S[0], V22.S4    // spillSuppress
	VDUP V3.S[0], V23.S4    // invSpillDistSq
	VDUP V4.S[0], V24.S4    // replaceCb
	VDUP V5.S[0], V25.S4    // replaceCr

	// Set up 1.0 and 255.0 constants
	FMOVS $1.0, F6
	VDUP V6.S[0], V26.S4    // 1.0

	MOVD  $0x437F0000, R3   // IEEE 754 float32 for 255.0
	VMOV  R3, V27.S[0]
	VDUP  V27.S[0], V27.S4  // 255.0

	VEOR  V28.B16, V28.B16, V28.B16  // 0.0

	CMP  $4, R2
	BLT  spill_scalar

spill_neon4:
	// Load 4 Cb bytes, widen to uint32, save original, convert to float32
	MOVWU (R0), R3
	VMOV  R3, V0.S[0]
	USHLL_8H(0, 0)
	USHLL_4S(0, 0)
	VMOV  V0.B16, V11.B16   // V11 = original cb as uint32 (saved)
	UCVTF_4S(0, 0)          // V0 = cb[0..3] as float32

	// Load 4 Cr bytes, widen to uint32, save original, convert to float32
	MOVWU (R1), R3
	VMOV  R3, V1.S[0]
	USHLL_8H(1, 1)
	USHLL_4S(1, 1)
	VMOV  V1.B16, V12.B16   // V12 = original cr as uint32 (saved)
	UCVTF_4S(1, 1)          // V1 = cr[0..3] as float32

	// dCb = cb - keyCb, dCr = cr - keyCr
	FSUB_4S(2, 0, 20)       // V2 = dCb
	FSUB_4S(3, 1, 21)       // V3 = dCr

	// distSq = dCb*dCb + dCr*dCr
	FMUL_4S(4, 2, 2)        // V4 = dCb*dCb
	FMLA_4S(4, 3, 3)        // V4 += dCr*dCr = distSq

	// ratio = distSq * invSpillDistSq
	FMUL_4S(5, 4, 23)       // V5 = ratio

	// mask = ratio < 1.0 ? (1.0 > ratio)
	FCMGT_4S(6, 26, 5)      // V6 = (1.0 > ratio) mask: all-ones where inside

	// Quick check: if no pixels are inside spill zone, skip
	VMOV V6.D[0], R3
	VMOV V6.D[1], R4
	ORR  R4, R3, R3
	CBZ  R3, spill_skip4

	// spillAmount = spillSuppress * (1.0 - ratio)
	FSUB_4S(7, 26, 5)       // V7 = 1.0 - ratio
	FMUL_4S(7, 7, 22)       // V7 = spillAmount

	// mask2 = spillAmount > 0
	FCMGT_4S_Z(8, 7)        // V8 = spillAmount > 0

	// Combined mask: inside spill zone AND spillAmount > 0
	VAND V6.B16, V8.B16, V6.B16  // V6 = combined mask

	// newCb = cb + (replaceCb - cb) * spillAmount
	FSUB_4S(9, 24, 0)       // V9 = replaceCb - cb
	FMUL_4S(9, 9, 7)        // V9 = (replaceCb - cb) * spillAmount
	FADD_4S(9, 0, 9)        // V9 = cb + delta = newCb

	// newCr = cr + (replaceCr - cr) * spillAmount
	FSUB_4S(10, 25, 1)      // V10 = replaceCr - cr
	FMUL_4S(10, 10, 7)      // V10 = (replaceCr - cr) * spillAmount
	FADD_4S(10, 1, 10)      // V10 = cr + delta = newCr

	// Clamp newCb to [0, 255]
	FMAX_4S(9, 9, 28)       // max(newCb, 0)
	FMIN_4S(9, 9, 27)       // min(newCb, 255)

	// Clamp newCr to [0, 255]
	FMAX_4S(10, 10, 28)     // max(newCr, 0)
	FMIN_4S(10, 10, 27)     // min(newCr, 255)

	// Convert float32 back to uint32
	FCVTZU_4S(9, 9)         // V9 = newCb as uint32
	FCVTZU_4S(10, 10)       // V10 = newCr as uint32

	// BIT: insert new where mask is true, keep original (V11/V12) where false
	BIT(11, 9, 6)           // V11[i] = mask ? newCb : origCb
	BIT(12, 10, 6)          // V12[i] = mask ? newCr : origCr

	// Narrow uint32 → uint16 → uint8 and store
	UQXTN_4H(11, 11)
	UQXTN_8B(11, 11)
	VMOV V11.S[0], R3
	MOVW R3, (R0)

	UQXTN_4H(12, 12)
	UQXTN_8B(12, 12)
	VMOV V12.S[0], R3
	MOVW R3, (R1)

spill_skip4:
	ADD  $4, R0
	ADD  $4, R1
	SUB  $4, R2, R2
	CMP  $4, R2
	BGE  spill_neon4

spill_scalar:
	CMP  $0, R2
	BLE  spill_done

spill_scalar_loop:
	// Load Cb, Cr byte
	MOVBU (R0), R3
	MOVBU (R1), R4

	// Convert to float32
	UCVTFWD R3, F6          // F6 = cb
	UCVTFWD R4, F7          // F7 = cr

	// dCb = cb - keyCb
	FMOVS keyCb+16(FP), F8
	FSUBS F8, F6, F8        // F8 = dCb
	// dCr = cr - keyCr
	FMOVS keyCr+20(FP), F9
	FSUBS F9, F7, F9        // F9 = dCr

	// distSq = dCb*dCb + dCr*dCr
	FMULS F8, F8, F10       // F10 = dCb*dCb
	FMADDS F9, F10, F9, F10 // F10 = F9*F9 + F10 = dCr*dCr + dCb*dCb

	// ratio = distSq * invSpillDistSq
	FMOVS invSpillDistSq+28(FP), F11
	FMULS F11, F10, F11     // F11 = ratio

	// if ratio >= 1.0, skip
	FMOVS $1.0, F12
	FCMPS F12, F11
	BGE   spill_scalar_next // ratio >= 1.0, skip

	// spillAmount = spillSuppress * (1.0 - ratio)
	FSUBS F11, F12, F12     // F12 = 1.0 - ratio
	FMOVS spillSuppress+24(FP), F13
	FMULS F13, F12, F12     // F12 = spillAmount

	// if spillAmount <= 0, skip
	FCMPS $0.0, F12
	BLE   spill_scalar_next

	// newCb = cb + (replaceCb - cb) * spillAmount
	FMOVS replaceCb+32(FP), F13
	FSUBS F6, F13, F13      // F13 = replaceCb - cb
	FMULS F12, F13, F13     // F13 = (replaceCb - cb) * spillAmount
	FADDS F13, F6, F13      // F13 = newCb

	// newCr = cr + (replaceCr - cr) * spillAmount
	FMOVS replaceCr+36(FP), F14
	FSUBS F7, F14, F14      // F14 = replaceCr - cr
	FMULS F12, F14, F14     // F14 = (replaceCr - cr) * spillAmount
	FADDS F14, F7, F14      // F14 = newCr

	// Clamp to [0, 255]
	MOVD  $0x437F0000, R5   // 255.0
	FMOVS R5, F15
	FMOVS $0.0, F16
	FMAXS F16, F13, F13
	FMINS F15, F13, F13
	FMAXS F16, F14, F14
	FMINS F15, F14, F14

	// Convert to uint32 and store as byte
	FCVTZUSW F13, R3
	MOVB  R3, (R0)
	FCVTZUSW F14, R4
	MOVB  R4, (R1)

spill_scalar_next:
	ADD  $1, R0
	ADD  $1, R1
	SUB  $1, R2, R2
	CBNZ R2, spill_scalar_loop

spill_done:
	RET
