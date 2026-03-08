#include "textflag.h"

// AMD64 V210 conversion kernels.
// ChromaVAvg: AVX2 VPAVGB (32 bytes/iter) + SSE2 PAVGB fallback (16 bytes/iter).
// V210UnpackRow/V210PackRow: scalar per-group (V210 layout defeats SIMD gather).

// ============================================================================
// func ChromaVAvg(dst, top, bot *byte, n int)
// ============================================================================
// dst[i] = (top[i] + bot[i] + 1) >> 1
// PAVGB / VPAVGB does this in a single instruction.
TEXT ·ChromaVAvg(SB), NOSPLIT, $0-32
	MOVQ dst+0(FP), DI
	MOVQ top+8(FP), SI
	MOVQ bot+16(FP), DX
	MOVQ n+24(FP), CX

	TESTQ CX, CX
	JLE   cavg_done

	CMPB ·avx2Available(SB), $1
	JE   cavg_avx2

	// --- SSE2 path (16 bytes/iter) ---
	CMPQ CX, $16
	JLT  cavg_scalar

cavg_sse2_loop:
	MOVOU (SI), X0            // top
	MOVOU (DX), X1            // bot
	PAVGB X1, X0              // X0 = (top + bot + 1) >> 1
	MOVOU X0, (DI)

	ADDQ  $16, SI
	ADDQ  $16, DX
	ADDQ  $16, DI
	SUBQ  $16, CX
	CMPQ  CX, $16
	JGE   cavg_sse2_loop

	TESTQ CX, CX
	JZ    cavg_done
	JMP   cavg_scalar

cavg_avx2:
	// --- AVX2 path (32 bytes/iter) ---
	CMPQ CX, $32
	JLT  cavg_avx2_tail

cavg_avx2_loop:
	VMOVDQU (SI), Y0          // top
	VMOVDQU (DX), Y1          // bot
	VPAVGB  Y1, Y0, Y0        // Y0 = (top + bot + 1) >> 1
	VMOVDQU Y0, (DI)

	ADDQ  $32, SI
	ADDQ  $32, DX
	ADDQ  $32, DI
	SUBQ  $32, CX
	CMPQ  CX, $32
	JGE   cavg_avx2_loop

cavg_avx2_tail:
	VZEROUPPER
	TESTQ CX, CX
	JZ    cavg_done
	// Fall through to SSE2 for 16+ bytes
	CMPQ  CX, $16
	JLT   cavg_scalar

cavg_avx2_sse2_tail:
	MOVOU (SI), X0
	MOVOU (DX), X1
	PAVGB X1, X0
	MOVOU X0, (DI)
	ADDQ  $16, SI
	ADDQ  $16, DX
	ADDQ  $16, DI
	SUBQ  $16, CX
	CMPQ  CX, $16
	JGE   cavg_avx2_sse2_tail

cavg_scalar:
	TESTQ CX, CX
	JZ    cavg_done

cavg_scalar_loop:
	MOVBLZX (SI), AX
	MOVBLZX (DX), BX
	ADDQ    BX, AX
	ADDQ    $1, AX
	SHRQ    $1, AX
	MOVB    AX, (DI)
	INCQ    SI
	INCQ    DX
	INCQ    DI
	DECQ    CX
	JNZ     cavg_scalar_loop

cavg_done:
	RET


// ============================================================================
// func V210UnpackRow(yOut, cbOut, crOut, v210In *byte, groups int)
// ============================================================================
// Extracts 10-bit fields from V210 words, converts to 8-bit (>>2).
// Per group: 4 x uint32 -> 6 Y + 3 Cb + 3 Cr bytes.
// Scalar per-group: V210's irregular bit layout prevents useful SIMD.
TEXT ·V210UnpackRow(SB), NOSPLIT, $0-40
	MOVQ yOut+0(FP), DI       // Y output
	MOVQ cbOut+8(FP), SI      // Cb output
	MOVQ crOut+16(FP), DX     // Cr output
	MOVQ v210In+24(FP), R8    // V210 input
	MOVQ groups+32(FP), CX    // group count

	TESTQ CX, CX
	JLE   unpack_done

unpack_loop:
	// Load 4 words
	MOVL  (R8), AX            // w0
	MOVL  4(R8), BX           // w1
	MOVL  8(R8), R9           // w2
	MOVL  12(R8), R10         // w3

	// Word 0: Cb0=[9:0], Y0=[19:10], Cr0=[29:20]
	MOVL  AX, R11
	ANDL  $0x3FF, R11         // Cb0
	SHRL  $2, R11
	MOVB  R11, (SI)

	MOVL  AX, R11
	SHRL  $10, R11
	ANDL  $0x3FF, R11         // Y0
	SHRL  $2, R11
	MOVB  R11, (DI)

	SHRL  $20, AX
	ANDL  $0x3FF, AX          // Cr0
	SHRL  $2, AX
	MOVB  AX, (DX)

	// Word 1: Y1=[9:0], Cb2=[19:10], Y2=[29:20]
	MOVL  BX, R11
	ANDL  $0x3FF, R11         // Y1
	SHRL  $2, R11
	MOVB  R11, 1(DI)

	MOVL  BX, R11
	SHRL  $10, R11
	ANDL  $0x3FF, R11         // Cb2
	SHRL  $2, R11
	MOVB  R11, 1(SI)

	SHRL  $20, BX
	ANDL  $0x3FF, BX          // Y2
	SHRL  $2, BX
	MOVB  BX, 2(DI)

	// Word 2: Cr2=[9:0], Y3=[19:10], Cb4=[29:20]
	MOVL  R9, R11
	ANDL  $0x3FF, R11         // Cr2
	SHRL  $2, R11
	MOVB  R11, 1(DX)

	MOVL  R9, R11
	SHRL  $10, R11
	ANDL  $0x3FF, R11         // Y3
	SHRL  $2, R11
	MOVB  R11, 3(DI)

	SHRL  $20, R9
	ANDL  $0x3FF, R9          // Cb4
	SHRL  $2, R9
	MOVB  R9, 2(SI)

	// Word 3: Y4=[9:0], Cr4=[19:10], Y5=[29:20]
	MOVL  R10, R11
	ANDL  $0x3FF, R11         // Y4
	SHRL  $2, R11
	MOVB  R11, 4(DI)

	MOVL  R10, R11
	SHRL  $10, R11
	ANDL  $0x3FF, R11         // Cr4
	SHRL  $2, R11
	MOVB  R11, 2(DX)

	SHRL  $20, R10
	ANDL  $0x3FF, R10         // Y5
	SHRL  $2, R10
	MOVB  R10, 5(DI)

	// Advance pointers
	ADDQ  $16, R8             // v210: 16 bytes
	ADDQ  $6, DI              // Y: 6 bytes
	ADDQ  $3, SI              // Cb: 3 bytes
	ADDQ  $3, DX              // Cr: 3 bytes
	DECQ  CX
	JNZ   unpack_loop

unpack_done:
	RET


// ============================================================================
// func V210PackRow(v210Out, yIn, cbIn, crIn *byte, groups int)
// ============================================================================
// Packs 8-bit Y/Cb/Cr into V210 words (<<2 to 10-bit).
// Per group: 6 Y + 3 Cb + 3 Cr -> 4 x uint32 (16 bytes).
TEXT ·V210PackRow(SB), NOSPLIT, $0-40
	MOVQ v210Out+0(FP), DI    // V210 output
	MOVQ yIn+8(FP), SI        // Y input
	MOVQ cbIn+16(FP), DX      // Cb input
	MOVQ crIn+24(FP), R8      // Cr input
	MOVQ groups+32(FP), CX    // group count

	TESTQ CX, CX
	JLE   pack_done

pack_loop:
	// Build each word by loading 3 values at a time.
	// Uses AX as accumulator, BX as temp per field.

	// --- Word 0: Cb0 | (Y0 << 10) | (Cr0 << 20) ---
	MOVBLZX (DX), AX          // Cb0
	SHLL    $2, AX             // Cb0 << 2

	MOVBLZX (SI), BX          // Y0
	SHLL    $12, BX            // Y0 << 2 << 10 = Y0 << 12
	ORL     BX, AX

	MOVBLZX (R8), BX          // Cr0
	SHLL    $22, BX            // Cr0 << 2 << 20 = Cr0 << 22
	ORL     BX, AX
	MOVL    AX, (DI)           // store w0

	// --- Word 1: Y1 | (Cb2 << 10) | (Y2 << 20) ---
	MOVBLZX 1(SI), AX         // Y1
	SHLL    $2, AX

	MOVBLZX 1(DX), BX         // Cb2
	SHLL    $12, BX
	ORL     BX, AX

	MOVBLZX 2(SI), BX         // Y2
	SHLL    $22, BX
	ORL     BX, AX
	MOVL    AX, 4(DI)          // store w1

	// --- Word 2: Cr2 | (Y3 << 10) | (Cb4 << 20) ---
	MOVBLZX 1(R8), AX         // Cr2
	SHLL    $2, AX

	MOVBLZX 3(SI), BX         // Y3
	SHLL    $12, BX
	ORL     BX, AX

	MOVBLZX 2(DX), BX         // Cb4
	SHLL    $22, BX
	ORL     BX, AX
	MOVL    AX, 8(DI)          // store w2

	// --- Word 3: Y4 | (Cr4 << 10) | (Y5 << 20) ---
	MOVBLZX 4(SI), AX         // Y4
	SHLL    $2, AX

	MOVBLZX 2(R8), BX         // Cr4
	SHLL    $12, BX
	ORL     BX, AX

	MOVBLZX 5(SI), BX         // Y5
	SHLL    $22, BX
	ORL     BX, AX
	MOVL    AX, 12(DI)         // store w3

	// Advance pointers
	ADDQ  $16, DI
	ADDQ  $6, SI
	ADDQ  $3, DX
	ADDQ  $3, R8
	DECQ  CX
	JNZ   pack_loop

pack_done:
	RET
