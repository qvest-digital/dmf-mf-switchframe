#include "textflag.h"

// ARM64 NEON V210 <-> YUV422P10 lossless 10-bit conversion kernels.
//
// v210UnpackRow10Ptr: V210 -> planar YUV422P10 (uint16)
// v210PackRow10Ptr:   planar YUV422P10 (uint16) -> V210
//
// Per group: 4 x uint32 words (16 bytes) <-> 6 Y + 3 Cb + 3 Cr (uint16 each).
// All 10-bit values are preserved losslessly.

// ============================================================================
// func v210UnpackRow10Ptr(yOut, cbOut, crOut *uint16, v210In *byte, groups int)
// ============================================================================
// Extracts 10-bit fields from V210 words, stores as uint16 LE.
// Uses scalar register operations with efficient bitfield extraction.
TEXT ·v210UnpackRow10Ptr(SB), NOSPLIT, $0-40
	MOVD yOut+0(FP), R0      // Y output pointer (uint16*)
	MOVD cbOut+8(FP), R1     // Cb output pointer (uint16*)
	MOVD crOut+16(FP), R2    // Cr output pointer (uint16*)
	MOVD v210In+24(FP), R3   // V210 input pointer (byte*)
	MOVD groups+32(FP), R4   // group count

	CMP  $0, R4
	BLE  unpack10_done

	MOVD $0x3FF, R10         // 10-bit mask

unpack10_loop:
	// Load 4 words (16 bytes)
	MOVWU (R3), R5           // w0
	MOVWU 4(R3), R6          // w1
	MOVWU 8(R3), R7          // w2
	MOVWU 12(R3), R8         // w3

	// === Word 0: Cb0=[9:0], Y0=[19:10], Cr0=[29:20] ===

	// Cb0 = w0 & 0x3FF
	AND   R10, R5, R9
	MOVH  R9, (R1)           // store Cb0 as uint16

	// Y0 = (w0 >> 10) & 0x3FF
	LSR   $10, R5, R9
	AND   R10, R9, R9
	MOVH  R9, (R0)           // store Y0 as uint16

	// Cr0 = (w0 >> 20) & 0x3FF
	LSR   $20, R5, R9
	AND   R10, R9, R9
	MOVH  R9, (R2)           // store Cr0 as uint16

	// === Word 1: Y1=[9:0], Cb2=[19:10], Y2=[29:20] ===

	// Y1 = w1 & 0x3FF
	AND   R10, R6, R9
	MOVH  R9, 2(R0)          // store Y1

	// Cb2 = (w1 >> 10) & 0x3FF
	LSR   $10, R6, R9
	AND   R10, R9, R9
	MOVH  R9, 2(R1)          // store Cb2

	// Y2 = (w1 >> 20) & 0x3FF
	LSR   $20, R6, R9
	AND   R10, R9, R9
	MOVH  R9, 4(R0)          // store Y2

	// === Word 2: Cr2=[9:0], Y3=[19:10], Cb4=[29:20] ===

	// Cr2 = w2 & 0x3FF
	AND   R10, R7, R9
	MOVH  R9, 2(R2)          // store Cr2

	// Y3 = (w2 >> 10) & 0x3FF
	LSR   $10, R7, R9
	AND   R10, R9, R9
	MOVH  R9, 6(R0)          // store Y3

	// Cb4 = (w2 >> 20) & 0x3FF
	LSR   $20, R7, R9
	AND   R10, R9, R9
	MOVH  R9, 4(R1)          // store Cb4

	// === Word 3: Y4=[9:0], Cr4=[19:10], Y5=[29:20] ===

	// Y4 = w3 & 0x3FF
	AND   R10, R8, R9
	MOVH  R9, 8(R0)          // store Y4

	// Cr4 = (w3 >> 10) & 0x3FF
	LSR   $10, R8, R9
	AND   R10, R9, R9
	MOVH  R9, 4(R2)          // store Cr4

	// Y5 = (w3 >> 20) & 0x3FF
	LSR   $20, R8, R9
	AND   R10, R9, R9
	MOVH  R9, 10(R0)         // store Y5

	// Advance pointers
	ADD   $16, R3, R3        // v210: 16 bytes per group
	ADD   $12, R0, R0        // Y: 6 uint16 = 12 bytes per group
	ADD   $6, R1, R1         // Cb: 3 uint16 = 6 bytes per group
	ADD   $6, R2, R2         // Cr: 3 uint16 = 6 bytes per group
	SUB   $1, R4, R4
	CBNZ  R4, unpack10_loop

unpack10_done:
	RET


// ============================================================================
// func v210PackRow10Ptr(v210Out *byte, yIn, cbIn, crIn *uint16, groups int)
// ============================================================================
// Packs uint16 Y/Cb/Cr into V210 words. Values are masked to 10-bit.
// Pad bits [31:30] are zeroed automatically by masking.
TEXT ·v210PackRow10Ptr(SB), NOSPLIT, $0-40
	MOVD v210Out+0(FP), R0   // V210 output pointer (byte*)
	MOVD yIn+8(FP), R1       // Y input pointer (uint16*)
	MOVD cbIn+16(FP), R2     // Cb input pointer (uint16*)
	MOVD crIn+24(FP), R3     // Cr input pointer (uint16*)
	MOVD groups+32(FP), R4   // group count

	CMP  $0, R4
	BLE  pack10_done

	MOVD $0x3FF, R10         // 10-bit mask

pack10_loop:
	// Load 6 Y values (uint16)
	MOVHU (R1), R5            // Y0
	AND   R10, R5, R5
	MOVHU 2(R1), R6           // Y1
	AND   R10, R6, R6
	MOVHU 4(R1), R7           // Y2
	AND   R10, R7, R7
	MOVHU 6(R1), R8           // Y3
	AND   R10, R8, R8
	MOVHU 8(R1), R9           // Y4
	AND   R10, R9, R9
	MOVHU 10(R1), R11         // Y5
	AND   R10, R11, R11

	// Load 3 Cb values (uint16)
	MOVHU (R2), R12           // Cb0
	AND   R10, R12, R12
	MOVHU 2(R2), R13          // Cb2
	AND   R10, R13, R13
	MOVHU 4(R2), R14          // Cb4
	AND   R10, R14, R14

	// Load 3 Cr values (uint16)
	MOVHU (R3), R15           // Cr0
	AND   R10, R15, R15
	MOVHU 2(R3), R16          // Cr2
	AND   R10, R16, R16
	MOVHU 4(R3), R17          // Cr4
	AND   R10, R17, R17

	// === Word 0: Cb0 | (Y0 << 10) | (Cr0 << 20) ===
	LSL   $10, R5, R19       // Y0 << 10
	ORR   R12, R19, R19      // Cb0 | (Y0 << 10)
	LSL   $20, R15, R20      // Cr0 << 20
	ORR   R20, R19, R19      // complete w0
	MOVW  R19, (R0)

	// === Word 1: Y1 | (Cb2 << 10) | (Y2 << 20) ===
	LSL   $10, R13, R19      // Cb2 << 10
	ORR   R6, R19, R19       // Y1 | (Cb2 << 10)
	LSL   $20, R7, R20       // Y2 << 20
	ORR   R20, R19, R19
	MOVW  R19, 4(R0)

	// === Word 2: Cr2 | (Y3 << 10) | (Cb4 << 20) ===
	LSL   $10, R8, R19       // Y3 << 10
	ORR   R16, R19, R19      // Cr2 | (Y3 << 10)
	LSL   $20, R14, R20      // Cb4 << 20
	ORR   R20, R19, R19
	MOVW  R19, 8(R0)

	// === Word 3: Y4 | (Cr4 << 10) | (Y5 << 20) ===
	LSL   $10, R17, R19      // Cr4 << 10
	ORR   R9, R19, R19       // Y4 | (Cr4 << 10)
	LSL   $20, R11, R20      // Y5 << 20
	ORR   R20, R19, R19
	MOVW  R19, 12(R0)

	// Advance pointers
	ADD   $16, R0, R0        // v210: 16 bytes per group
	ADD   $12, R1, R1        // Y: 6 uint16 = 12 bytes per group
	ADD   $6, R2, R2         // Cb: 3 uint16 = 6 bytes per group
	ADD   $6, R3, R3         // Cr: 3 uint16 = 6 bytes per group
	SUB   $1, R4, R4
	CBNZ  R4, pack10_loop

pack10_done:
	RET
