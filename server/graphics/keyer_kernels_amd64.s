#include "textflag.h"

// AMD64 scalar assembly for chroma key mask computation.
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
//   DI = mask ptr
//   SI = cbPlane ptr
//   DX = crPlane ptr
//   R8 = keyCb
//   R9 = keyCr
//   R10 = simThreshSq
//   R11 = totalThreshSq
//   R12 = invRange
//   CX = n (loop counter)
//   AX, BX, R13, R14 = temporaries

TEXT ·chromaKeyMaskChroma(SB), NOSPLIT, $0-72
	MOVQ mask+0(FP), DI
	MOVQ cbPlane+8(FP), SI
	MOVQ crPlane+16(FP), DX
	MOVQ keyCb+24(FP), R8
	MOVQ keyCr+32(FP), R9
	MOVQ simThreshSq+40(FP), R10
	MOVQ totalThreshSq+48(FP), R11
	MOVQ invRange+56(FP), R12
	MOVQ n+64(FP), CX

	TESTQ CX, CX
	JLE   ckm_done

ckm_loop:
	// Load cb and cr bytes, zero-extend to 64-bit
	MOVBLZX (SI), AX           // AX = cbPlane[i]
	MOVBLZX (DX), BX           // BX = crPlane[i]

	// dCb = cb - keyCb (signed)
	SUBQ R8, AX                // AX = dCb

	// dCr = cr - keyCr (signed)
	SUBQ R9, BX                // BX = dCr

	// distSq = dCb*dCb + dCr*dCr
	MOVQ AX, R13
	IMULQ AX, R13              // R13 = dCb * dCb
	MOVQ BX, R14
	IMULQ BX, R14              // R14 = dCr * dCr
	ADDQ R14, R13              // R13 = distSq

	// Branch: distSq < simThreshSq?
	CMPQ R13, R10
	JL   ckm_transparent

	// Branch: distSq < totalThreshSq?
	CMPQ R13, R11
	JL   ckm_smooth

	// Opaque: mask[i] = 255
	MOVB $255, (DI)
	JMP  ckm_next

ckm_transparent:
	MOVB $0, (DI)
	JMP  ckm_next

ckm_smooth:
	// mask[i] = (distSq - simThreshSq) * invRange >> 16
	SUBQ R10, R13              // R13 = distSq - simThreshSq
	IMULQ R12, R13             // R13 = (distSq - simThreshSq) * invRange
	SHRQ $16, R13              // R13 >>= 16
	// Clamp to 255
	CMPQ R13, $255
	JLE  ckm_smooth_store
	MOVQ $255, R13

ckm_smooth_store:
	MOVB R13, (DI)

ckm_next:
	INCQ SI
	INCQ DX
	INCQ DI
	DECQ CX
	JNZ  ckm_loop

ckm_done:
	RET
