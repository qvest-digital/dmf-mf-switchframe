#include "textflag.h"

// ARM64 scalar kernel for 10-bit spill suppression.
//
// spillSuppress10Row applies spill suppression on uint16 LE Cb/Cr chroma planes.
// For each sample, computes squared distance from key color in float32.
// If within spill zone, lerps toward replacement color.

// ============================================================================
// func spillSuppress10Row(cbPlane, crPlane *byte, count int,
//     keyCb, keyCr, spillSuppress, invSpillDistSq, replaceCb, replaceCr float32)
// ============================================================================
// Stack: cbPlane(0) crPlane(8) count(16) keyCb(24) keyCr(28) spillSuppress(32)
//        invSpillDistSq(36) replaceCb(40) replaceCr(44)
TEXT ·spillSuppress10Row(SB), NOSPLIT, $0-48
	MOVD  cbPlane+0(FP), R0
	MOVD  crPlane+8(FP), R1
	MOVD  count+16(FP), R2
	FMOVS keyCb+24(FP), F0
	FMOVS keyCr+28(FP), F1
	FMOVS spillSuppress+32(FP), F2
	FMOVS invSpillDistSq+36(FP), F3
	FMOVS replaceCb+40(FP), F4
	FMOVS replaceCr+44(FP), F5

	CMP  $0, R2
	BLE  sp10_done

	// Constants
	FMOVS $1.0, F16
	MOVD  $0x447FC000, R3     // 1023.0f
	FMOVS R3, F17
	FMOVS $0.0, F18

sp10_loop:
	// Load uint16 LE Cb, Cr and convert to float32
	MOVHU (R0), R3
	UCVTFWS R3, F6            // F6 = cb
	MOVHU (R1), R4
	UCVTFWS R4, F7            // F7 = cr

	// dCb = cb - keyCb, dCr = cr - keyCr
	FSUBS F0, F6, F8          // F8 = dCb
	FSUBS F1, F7, F9          // F9 = dCr

	// distSq = dCb*dCb + dCr*dCr
	FMULS F8, F8, F10         // dCb*dCb
	FMADDS F9, F10, F9, F10   // F10 = dCr*dCr + dCb*dCb = distSq

	// ratio = distSq * invSpillDistSq
	FMULS F3, F10, F11        // F11 = ratio

	// if ratio >= 1.0, skip
	FCMPS F16, F11
	BGE   sp10_next            // ratio >= 1.0

	// spillAmount = spillSuppress * (1.0 - ratio)
	FSUBS F11, F16, F12       // 1.0 - ratio
	FMULS F2, F12, F12        // spillAmount

	// if spillAmount <= 0, skip
	FCMPS F18, F12
	BLE   sp10_next

	// newCb = cb + (replaceCb - cb) * spillAmount
	FSUBS F6, F4, F13         // replaceCb - cb
	FMULS F12, F13, F13       // * spillAmount
	FADDS F13, F6, F13        // newCb

	// newCr = cr + (replaceCr - cr) * spillAmount
	FSUBS F7, F5, F14         // replaceCr - cr
	FMULS F12, F14, F14       // * spillAmount
	FADDS F14, F7, F14        // newCr

	// Clamp to [0, 1023]
	FMAXS F18, F13, F13
	FMINS F17, F13, F13
	FMAXS F18, F14, F14
	FMINS F17, F14, F14

	// Convert back to uint16 and store
	FCVTZUSW F13, R3
	MOVHU R3, (R0)
	FCVTZUSW F14, R4
	MOVHU R4, (R1)

sp10_next:
	ADD  $2, R0               // advance Cb ptr
	ADD  $2, R1               // advance Cr ptr
	SUB  $1, R2, R2
	CBNZ R2, sp10_loop

sp10_done:
	RET
