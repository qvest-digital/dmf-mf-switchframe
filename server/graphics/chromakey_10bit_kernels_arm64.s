#include "textflag.h"

// ARM64 scalar kernel for 10-bit chroma key mask generation.
//
// chromaKey10Row generates a chroma key mask for count uint16 LE Cb/Cr pairs.
// Uses integer squared-distance for threshold comparison and fixed-point
// inverse range for the soft edge transition zone.
//
// For each sample:
//   dCb = Cb - targetCb, dCr = Cr - targetCr
//   distSq = dCb*dCb + dCr*dCr
//   if distSq <= simDistSq: mask = 255
//   elif distSq < totalDistSq && rangeSq > 0: mask = 255 - (distSq-simDistSq)*invRange>>16
//   else: mask = 0

// ============================================================================
// func chromaKey10Row(mask *byte, cb *byte, cr *byte, count int,
//     targetCb, targetCr int, simDistSq, totalDistSq, invRange int)
// ============================================================================
// Stack: mask(0) cb(8) cr(16) count(24) targetCb(32) targetCr(40) simDistSq(48) totalDistSq(56) invRange(64)
TEXT ·chromaKey10Row(SB), NOSPLIT, $0-72
	MOVD mask+0(FP), R0         // mask ptr
	MOVD cb+8(FP), R1           // cb ptr (uint16 LE)
	MOVD cr+16(FP), R2          // cr ptr (uint16 LE)
	MOVD count+24(FP), R3       // count
	MOVD targetCb+32(FP), R4    // targetCb
	MOVD targetCr+40(FP), R5    // targetCr
	MOVD simDistSq+48(FP), R6   // simDistSq
	MOVD totalDistSq+56(FP), R7 // totalDistSq
	MOVD invRange+64(FP), R8    // invRange

	CMP  $0, R3
	BLE  ck10_done

	MOVD $255, R9              // constant

ck10_loop:
	// Load Cb and Cr uint16 samples
	MOVHU (R1), R10            // Cb
	MOVHU (R2), R11            // Cr

	// dCb = Cb - targetCb, dCr = Cr - targetCr (signed)
	SUB   R4, R10, R12        // dCb
	SUB   R5, R11, R13        // dCr

	// distSq = dCb*dCb + dCr*dCr
	MUL   R12, R12, R14       // dCb*dCb
	MUL   R13, R13, R15       // dCr*dCr
	ADD   R15, R14, R14       // distSq

	// if distSq <= simDistSq: mask = 255
	CMP   R6, R14
	BLS   ck10_fully_keyed

	// if distSq >= totalDistSq: mask = 0
	CMP   R7, R14
	BHS   ck10_opaque

	// Soft edge: mask = 255 - (distSq - simDistSq) * invRange >> 16
	SUB   R6, R14, R14        // distSq - simDistSq
	MUL   R8, R14, R14        // * invRange
	LSR   $16, R14, R14       // >> 16
	SUB   R14, R9, R14        // 255 - result

	// Clamp to [0, 255]
	CMP   $0, R14
	CSEL  GT, R14, ZR, R14

	MOVB  R14, (R0)
	B     ck10_next

ck10_fully_keyed:
	MOVB  R9, (R0)            // mask = 255
	B     ck10_next

ck10_opaque:
	MOVB  ZR, (R0)            // mask = 0

ck10_next:
	ADD  $2, R1               // advance Cb ptr
	ADD  $2, R2               // advance Cr ptr
	ADD  $1, R0               // advance mask ptr
	SUB  $1, R3, R3
	CBNZ R3, ck10_loop

ck10_done:
	RET
