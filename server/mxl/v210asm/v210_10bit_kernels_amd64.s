#include "textflag.h"

// AMD64 V210 <-> YUV422P10 lossless 10-bit conversion kernels.
//
// V210UnpackRow10: V210 -> planar YUV422P10 (uint16)
// V210PackRow10:   planar YUV422P10 (uint16) -> V210
//
// Per group: 4 x uint32 words (16 bytes) <-> 6 Y + 3 Cb + 3 Cr (uint16 each).
// All 10-bit values are preserved losslessly.
// Uses scalar per-group processing (V210's irregular bit layout prevents useful SIMD).

// ============================================================================
// func v210UnpackRow10Ptr(yOut, cbOut, crOut *uint16, v210In *byte, groups int)
// ============================================================================
// Extracts 10-bit fields from V210 words, stores as uint16 LE.
TEXT ·V210UnpackRow10(SB), NOSPLIT, $0-40
	MOVQ yOut+0(FP), DI       // Y output (uint16*)
	MOVQ cbOut+8(FP), SI      // Cb output (uint16*)
	MOVQ crOut+16(FP), DX     // Cr output (uint16*)
	MOVQ v210In+24(FP), R8    // V210 input (byte*)
	MOVQ groups+32(FP), CX    // group count

	TESTQ CX, CX
	JLE   unpack10_done

unpack10_loop:
	// Load 4 words
	MOVL  (R8), AX            // w0
	MOVL  4(R8), BX           // w1
	MOVL  8(R8), R9           // w2
	MOVL  12(R8), R10         // w3

	// === Word 0: Cb0=[9:0], Y0=[19:10], Cr0=[29:20] ===
	MOVL  AX, R11
	ANDL  $0x3FF, R11         // Cb0
	MOVW  R11, (SI)           // store Cb0 as uint16

	MOVL  AX, R11
	SHRL  $10, R11
	ANDL  $0x3FF, R11         // Y0
	MOVW  R11, (DI)           // store Y0 as uint16

	SHRL  $20, AX
	ANDL  $0x3FF, AX          // Cr0
	MOVW  AX, (DX)            // store Cr0 as uint16

	// === Word 1: Y1=[9:0], Cb2=[19:10], Y2=[29:20] ===
	MOVL  BX, R11
	ANDL  $0x3FF, R11         // Y1
	MOVW  R11, 2(DI)          // store Y1

	MOVL  BX, R11
	SHRL  $10, R11
	ANDL  $0x3FF, R11         // Cb2
	MOVW  R11, 2(SI)          // store Cb2

	SHRL  $20, BX
	ANDL  $0x3FF, BX          // Y2
	MOVW  BX, 4(DI)           // store Y2

	// === Word 2: Cr2=[9:0], Y3=[19:10], Cb4=[29:20] ===
	MOVL  R9, R11
	ANDL  $0x3FF, R11         // Cr2
	MOVW  R11, 2(DX)          // store Cr2

	MOVL  R9, R11
	SHRL  $10, R11
	ANDL  $0x3FF, R11         // Y3
	MOVW  R11, 6(DI)          // store Y3

	SHRL  $20, R9
	ANDL  $0x3FF, R9          // Cb4
	MOVW  R9, 4(SI)           // store Cb4

	// === Word 3: Y4=[9:0], Cr4=[19:10], Y5=[29:20] ===
	MOVL  R10, R11
	ANDL  $0x3FF, R11         // Y4
	MOVW  R11, 8(DI)          // store Y4

	MOVL  R10, R11
	SHRL  $10, R11
	ANDL  $0x3FF, R11         // Cr4
	MOVW  R11, 4(DX)          // store Cr4

	SHRL  $20, R10
	ANDL  $0x3FF, R10         // Y5
	MOVW  R10, 10(DI)         // store Y5

	// Advance pointers
	ADDQ  $16, R8             // v210: 16 bytes per group
	ADDQ  $12, DI             // Y: 6 uint16 = 12 bytes per group
	ADDQ  $6, SI              // Cb: 3 uint16 = 6 bytes per group
	ADDQ  $6, DX              // Cr: 3 uint16 = 6 bytes per group
	DECQ  CX
	JNZ   unpack10_loop

unpack10_done:
	RET


// ============================================================================
// func v210PackRow10Ptr(v210Out *byte, yIn, cbIn, crIn *uint16, groups int)
// ============================================================================
// Packs uint16 Y/Cb/Cr into V210 words. Values are masked to 10-bit.
TEXT ·V210PackRow10(SB), NOSPLIT, $0-40
	MOVQ v210Out+0(FP), DI    // V210 output (byte*)
	MOVQ yIn+8(FP), SI        // Y input (uint16*)
	MOVQ cbIn+16(FP), DX      // Cb input (uint16*)
	MOVQ crIn+24(FP), R8      // Cr input (uint16*)
	MOVQ groups+32(FP), CX    // group count

	TESTQ CX, CX
	JLE   pack10_done

pack10_loop:
	// === Word 0: Cb0 | (Y0 << 10) | (Cr0 << 20) ===
	MOVWLZX (DX), AX          // Cb0
	ANDL    $0x3FF, AX

	MOVWLZX (SI), BX          // Y0
	ANDL    $0x3FF, BX
	SHLL    $10, BX
	ORL     BX, AX

	MOVWLZX (R8), BX          // Cr0
	ANDL    $0x3FF, BX
	SHLL    $20, BX
	ORL     BX, AX
	MOVL    AX, (DI)           // store w0

	// === Word 1: Y1 | (Cb2 << 10) | (Y2 << 20) ===
	MOVWLZX 2(SI), AX         // Y1
	ANDL    $0x3FF, AX

	MOVWLZX 2(DX), BX         // Cb2
	ANDL    $0x3FF, BX
	SHLL    $10, BX
	ORL     BX, AX

	MOVWLZX 4(SI), BX         // Y2
	ANDL    $0x3FF, BX
	SHLL    $20, BX
	ORL     BX, AX
	MOVL    AX, 4(DI)          // store w1

	// === Word 2: Cr2 | (Y3 << 10) | (Cb4 << 20) ===
	MOVWLZX 2(R8), AX         // Cr2
	ANDL    $0x3FF, AX

	MOVWLZX 6(SI), BX         // Y3
	ANDL    $0x3FF, BX
	SHLL    $10, BX
	ORL     BX, AX

	MOVWLZX 4(DX), BX         // Cb4
	ANDL    $0x3FF, BX
	SHLL    $20, BX
	ORL     BX, AX
	MOVL    AX, 8(DI)          // store w2

	// === Word 3: Y4 | (Cr4 << 10) | (Y5 << 20) ===
	MOVWLZX 8(SI), AX         // Y4
	ANDL    $0x3FF, AX

	MOVWLZX 4(R8), BX         // Cr4
	ANDL    $0x3FF, BX
	SHLL    $10, BX
	ORL     BX, AX

	MOVWLZX 10(SI), BX        // Y5
	ANDL    $0x3FF, BX
	SHLL    $20, BX
	ORL     BX, AX
	MOVL    AX, 12(DI)         // store w3

	// Advance pointers
	ADDQ  $16, DI             // v210: 16 bytes per group
	ADDQ  $12, SI             // Y: 6 uint16 = 12 bytes per group
	ADDQ  $6, DX              // Cb: 3 uint16 = 6 bytes per group
	ADDQ  $6, R8              // Cr: 3 uint16 = 6 bytes per group
	DECQ  CX
	JNZ   pack10_loop

pack10_done:
	RET
