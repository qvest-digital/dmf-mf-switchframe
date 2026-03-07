#include "textflag.h"

// ARM64 NEON V210 conversion kernels.
//
// chromaVAvg: uses URHADD for single-instruction rounding average (16 bytes/iter).
// v210UnpackRow/v210PackRow: scalar per-group processing with bitfield extraction.
// The V210 format's irregular field layout (3 x 10-bit in 32-bit words) makes
// SIMD difficult, but the scalar loop still benefits from assembly by eliminating
// bounds checks and using efficient register allocation.

// URHADD Vd.16B, Vn.16B, Vm.16B — unsigned rounding halving add
// Computes (a + b + 1) >> 1 in a single instruction.
#define URHADD_16B(Vd, Vn, Vm) WORD $(0x6E201400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))


// ============================================================================
// func chromaVAvg(dst, top, bot *byte, n int)
// ============================================================================
// dst[i] = (top[i] + bot[i] + 1) >> 1
// URHADD does this in a single instruction for 16 bytes.
TEXT ·chromaVAvg(SB), NOSPLIT, $0-32
	MOVD dst+0(FP), R0
	MOVD top+8(FP), R1
	MOVD bot+16(FP), R2
	MOVD n+24(FP), R3

	CMP  $0, R3
	BLE  cavg_done

	CMP  $16, R3
	BLT  cavg_tail

cavg_loop16:
	VLD1.P 16(R1), [V0.B16]    // top[i..i+15]
	VLD1.P 16(R2), [V1.B16]    // bot[i..i+15]
	URHADD_16B(2, 0, 1)        // V2 = (V0 + V1 + 1) >> 1
	VST1.P [V2.B16], 16(R0)

	SUB  $16, R3, R3
	CMP  $16, R3
	BGE  cavg_loop16

cavg_tail:
	CBZ  R3, cavg_done

cavg_tail_loop:
	MOVBU (R1), R4
	MOVBU (R2), R5
	ADD   R5, R4, R4
	ADD   $1, R4, R4
	LSR   $1, R4, R4
	MOVB  R4, (R0)
	ADD   $1, R0
	ADD   $1, R1
	ADD   $1, R2
	SUB   $1, R3, R3
	CBNZ  R3, cavg_tail_loop

cavg_done:
	RET


// ============================================================================
// func v210UnpackRow(yOut, cbOut, crOut, v210In *byte, groups int)
// ============================================================================
// Extracts 10-bit fields from V210 words, converts to 8-bit (>>2).
// Per group: 4 x uint32 -> 6 Y + 3 Cb + 3 Cr bytes.
// Scalar per-group with efficient register usage (no bounds checks).
TEXT ·v210UnpackRow(SB), NOSPLIT, $0-40
	MOVD yOut+0(FP), R0      // Y output pointer
	MOVD cbOut+8(FP), R1     // Cb output pointer
	MOVD crOut+16(FP), R2    // Cr output pointer
	MOVD v210In+24(FP), R3   // V210 input pointer
	MOVD groups+32(FP), R4   // group count

	CMP  $0, R4
	BLE  unpack_done

	MOVD $0x3FF, R10         // 10-bit mask

unpack_loop:
	// Load 4 words (16 bytes)
	MOVWU (R3), R5           // w0
	MOVWU 4(R3), R6          // w1
	MOVWU 8(R3), R7          // w2
	MOVWU 12(R3), R8         // w3

	// Word 0: Cb0=[9:0], Y0=[19:10], Cr0=[29:20]
	AND   R10, R5, R9        // Cb0 (10-bit)
	LSR   $2, R9, R9         // Cb0 >> 2 = 8-bit
	MOVB  R9, (R1)           // store Cb0

	LSR   $10, R5, R9
	AND   R10, R9, R9        // Y0 (10-bit)
	LSR   $2, R9, R9
	MOVB  R9, (R0)           // store Y0

	LSR   $20, R5, R9
	AND   R10, R9, R9        // Cr0 (10-bit)
	LSR   $2, R9, R9
	MOVB  R9, (R2)           // store Cr0

	// Word 1: Y1=[9:0], Cb2=[19:10], Y2=[29:20]
	AND   R10, R6, R9        // Y1
	LSR   $2, R9, R9
	MOVB  R9, 1(R0)

	LSR   $10, R6, R9
	AND   R10, R9, R9        // Cb2
	LSR   $2, R9, R9
	MOVB  R9, 1(R1)

	LSR   $20, R6, R9
	AND   R10, R9, R9        // Y2
	LSR   $2, R9, R9
	MOVB  R9, 2(R0)

	// Word 2: Cr2=[9:0], Y3=[19:10], Cb4=[29:20]
	AND   R10, R7, R9        // Cr2
	LSR   $2, R9, R9
	MOVB  R9, 1(R2)

	LSR   $10, R7, R9
	AND   R10, R9, R9        // Y3
	LSR   $2, R9, R9
	MOVB  R9, 3(R0)

	LSR   $20, R7, R9
	AND   R10, R9, R9        // Cb4
	LSR   $2, R9, R9
	MOVB  R9, 2(R1)

	// Word 3: Y4=[9:0], Cr4=[19:10], Y5=[29:20]
	AND   R10, R8, R9        // Y4
	LSR   $2, R9, R9
	MOVB  R9, 4(R0)

	LSR   $10, R8, R9
	AND   R10, R9, R9        // Cr4
	LSR   $2, R9, R9
	MOVB  R9, 2(R2)

	LSR   $20, R8, R9
	AND   R10, R9, R9        // Y5
	LSR   $2, R9, R9
	MOVB  R9, 5(R0)

	// Advance pointers
	ADD   $16, R3, R3        // v210: 16 bytes per group
	ADD   $6, R0, R0         // Y: 6 bytes per group
	ADD   $3, R1, R1         // Cb: 3 bytes per group
	ADD   $3, R2, R2         // Cr: 3 bytes per group
	SUB   $1, R4, R4
	CBNZ  R4, unpack_loop

unpack_done:
	RET


// ============================================================================
// func v210PackRow(v210Out, yIn, cbIn, crIn *byte, groups int)
// ============================================================================
// Packs 8-bit Y/Cb/Cr into V210 words (<<2 to 10-bit).
// Per group: 6 Y + 3 Cb + 3 Cr -> 4 x uint32 (16 bytes).
TEXT ·v210PackRow(SB), NOSPLIT, $0-40
	MOVD v210Out+0(FP), R0   // V210 output pointer
	MOVD yIn+8(FP), R1       // Y input pointer
	MOVD cbIn+16(FP), R2     // Cb input pointer
	MOVD crIn+24(FP), R3     // Cr input pointer
	MOVD groups+32(FP), R4   // group count

	CMP  $0, R4
	BLE  pack_done

pack_loop:
	// Load Y values and shift left by 2
	MOVBU (R1), R5            // Y0
	LSL   $2, R5, R5
	MOVBU 1(R1), R6           // Y1
	LSL   $2, R6, R6
	MOVBU 2(R1), R7           // Y2
	LSL   $2, R7, R7
	MOVBU 3(R1), R8           // Y3
	LSL   $2, R8, R8
	MOVBU 4(R1), R9           // Y4
	LSL   $2, R9, R9
	MOVBU 5(R1), R10          // Y5
	LSL   $2, R10, R10

	// Load Cb values and shift left by 2
	MOVBU (R2), R11           // Cb0
	LSL   $2, R11, R11
	MOVBU 1(R2), R12          // Cb2
	LSL   $2, R12, R12
	MOVBU 2(R2), R13          // Cb4
	LSL   $2, R13, R13

	// Load Cr values and shift left by 2
	MOVBU (R3), R14           // Cr0
	LSL   $2, R14, R14
	MOVBU 1(R3), R15          // Cr2
	LSL   $2, R15, R15
	MOVBU 2(R3), R16          // Cr4
	LSL   $2, R16, R16

	// Word 0: Cb0 | (Y0 << 10) | (Cr0 << 20)
	LSL   $10, R5, R17       // Y0 << 10
	ORR   R11, R17, R17      // Cb0 | (Y0 << 10)
	LSL   $20, R14, R19      // Cr0 << 20
	ORR   R19, R17, R17      // complete w0
	MOVW  R17, (R0)

	// Word 1: Y1 | (Cb2 << 10) | (Y2 << 20)
	LSL   $10, R12, R17      // Cb2 << 10
	ORR   R6, R17, R17       // Y1 | (Cb2 << 10)
	LSL   $20, R7, R19       // Y2 << 20
	ORR   R19, R17, R17
	MOVW  R17, 4(R0)

	// Word 2: Cr2 | (Y3 << 10) | (Cb4 << 20)
	LSL   $10, R8, R17       // Y3 << 10
	ORR   R15, R17, R17      // Cr2 | (Y3 << 10)
	LSL   $20, R13, R19      // Cb4 << 20
	ORR   R19, R17, R17
	MOVW  R17, 8(R0)

	// Word 3: Y4 | (Cr4 << 10) | (Y5 << 20)
	LSL   $10, R16, R17      // Cr4 << 10
	ORR   R9, R17, R17       // Y4 | (Cr4 << 10)
	LSL   $20, R10, R19      // Y5 << 20
	ORR   R19, R17, R17
	MOVW  R17, 12(R0)

	// Advance pointers
	ADD   $16, R0, R0
	ADD   $6, R1, R1
	ADD   $3, R2, R2
	ADD   $3, R3, R3
	SUB   $1, R4, R4
	CBNZ  R4, pack_loop

pack_done:
	RET
