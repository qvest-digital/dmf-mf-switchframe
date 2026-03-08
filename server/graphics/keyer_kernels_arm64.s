#include "textflag.h"

// ARM64 scalar assembly for chroma key mask computation.
//
// func chromaKeyMaskChroma(mask *byte, cbPlane, crPlane *byte,
//     keyCb, keyCr int, simThreshSq, totalThreshSq int, invRange int, n int)
//
// For each pixel i:
//   dCb = int(cbPlane[i]) - keyCb
//   dCr = int(crPlane[i]) - keyCr
//   distSq = dCb*dCb + dCr*dCr
//   if distSq < simThreshSq:     mask[i] = 0
//   elif distSq < totalThreshSq: mask[i] = (distSq - simThreshSq) * invRange >> 16
//   else:                        mask[i] = 255
//
// Register allocation:
//   R0 = mask ptr
//   R1 = cbPlane ptr
//   R2 = crPlane ptr
//   R3 = keyCb
//   R4 = keyCr
//   R5 = simThreshSq
//   R6 = totalThreshSq
//   R7 = invRange
//   R8 = n (loop counter)
//   R9, R10, R11, R12 = temporaries

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

	CMP  $0, R8
	BLE  ckm_done

ckm_loop:
	// Load cb and cr bytes, zero-extend
	MOVBU (R1), R9             // R9 = cbPlane[i]
	MOVBU (R2), R10            // R10 = crPlane[i]

	// dCb = cb - keyCb (signed)
	SUB  R3, R9, R9            // R9 = dCb

	// dCr = cr - keyCr (signed)
	SUB  R4, R10, R10          // R10 = dCr

	// distSq = dCb*dCb + dCr*dCr
	// Use SMULL (signed multiply) for correct sign handling
	MUL  R9, R9, R11           // R11 = dCb * dCb
	MUL  R10, R10, R12         // R12 = dCr * dCr
	ADD  R12, R11, R11         // R11 = distSq

	// Branch: distSq < simThreshSq?
	CMP  R5, R11
	BLT  ckm_transparent

	// Branch: distSq < totalThreshSq?
	CMP  R6, R11
	BLT  ckm_smooth

	// Opaque: mask[i] = 255
	MOVD $255, R12
	MOVB R12, (R0)
	B    ckm_next

ckm_transparent:
	MOVB ZR, (R0)
	B    ckm_next

ckm_smooth:
	// mask[i] = (distSq - simThreshSq) * invRange >> 16
	SUB  R5, R11, R11          // R11 = distSq - simThreshSq
	MUL  R7, R11, R11          // R11 = (distSq - simThreshSq) * invRange
	LSR  $16, R11, R11         // R11 >>= 16
	// Clamp to 255
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
