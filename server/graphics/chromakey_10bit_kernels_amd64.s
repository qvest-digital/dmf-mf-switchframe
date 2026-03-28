#include "textflag.h"

// AMD64 scalar kernel for 10-bit chroma key mask generation.
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
// Stack layout:
//   mask:        0(FP)  [8 bytes]
//   cb:          8(FP)  [8 bytes]
//   cr:         16(FP)  [8 bytes]
//   count:      24(FP)  [8 bytes]
//   targetCb:   32(FP)  [8 bytes]
//   targetCr:   40(FP)  [8 bytes]
//   simDistSq:  48(FP)  [8 bytes]
//   totalDistSq:56(FP)  [8 bytes]
//   invRange:   64(FP)  [8 bytes]
// Total: 72 bytes
TEXT ·chromaKey10Row(SB), NOSPLIT, $0-72
	MOVQ mask+0(FP), DI          // mask ptr
	MOVQ cb+8(FP), SI            // cb ptr (uint16 LE)
	MOVQ cr+16(FP), DX           // cr ptr (uint16 LE)
	MOVQ count+24(FP), CX        // count
	MOVQ targetCb+32(FP), R8     // targetCb
	MOVQ targetCr+40(FP), R9     // targetCr
	MOVQ simDistSq+48(FP), R10   // simDistSq
	MOVQ totalDistSq+56(FP), R11 // totalDistSq
	MOVQ invRange+64(FP), R12    // invRange

	TESTQ CX, CX
	JLE   ck10_done

ck10_loop:
	// Load Cb and Cr uint16 samples
	MOVWLZX (SI), AX             // AX = Cb
	MOVWLZX (DX), BX             // BX = Cr

	// dCb = Cb - targetCb (signed)
	SUBQ    R8, AX               // AX = dCb
	// dCr = Cr - targetCr (signed)
	SUBQ    R9, BX               // BX = dCr

	// distSq = dCb*dCb + dCr*dCr
	IMULQ   AX, AX               // AX = dCb*dCb
	IMULQ   BX, BX               // BX = dCr*dCr
	ADDQ    BX, AX               // AX = distSq

	// if distSq <= simDistSq: mask = 255
	CMPQ    AX, R10
	JLE     ck10_fully_keyed

	// if distSq >= totalDistSq: mask = 0
	CMPQ    AX, R11
	JGE     ck10_opaque

	// Soft edge: mask = 255 - (distSq - simDistSq) * invRange >> 16
	SUBQ    R10, AX              // AX = distSq - simDistSq
	IMULQ   R12, AX              // AX = * invRange
	SHRQ    $16, AX              // AX >>= 16
	MOVQ    $255, R13
	SUBQ    AX, R13              // R13 = 255 - result

	// Clamp to [0, 255]
	TESTQ   R13, R13
	JGE     ck10_soft_store
	XORQ    R13, R13             // clamp to 0

ck10_soft_store:
	MOVB    R13, (DI)
	JMP     ck10_next

ck10_fully_keyed:
	MOVB    $255, (DI)           // mask = 255
	JMP     ck10_next

ck10_opaque:
	MOVB    $0, (DI)             // mask = 0

ck10_next:
	ADDQ    $2, SI               // advance Cb ptr
	ADDQ    $2, DX               // advance Cr ptr
	ADDQ    $1, DI               // advance mask ptr
	DECQ    CX
	JNZ     ck10_loop

ck10_done:
	RET
