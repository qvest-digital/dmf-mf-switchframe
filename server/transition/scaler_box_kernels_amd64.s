#include "textflag.h"

// AMD64 2x2 box shrink kernel.
// AVX2 (32 input bytes per row -> 16 output bytes/iter), SSE2 (16 -> 8),
// scalar tail. Even and odd bytes are split into 16-bit lanes and summed
// there, so (a+b+c+d+2)>>2 is exact.

// ============================================================================
// func boxShrink2xRow(dst, row0, row1 *byte, n int)
// ============================================================================
TEXT ·boxShrink2xRow(SB), NOSPLIT, $0-32
	MOVQ dst+0(FP), DI
	MOVQ row0+8(FP), SI
	MOVQ row1+16(FP), DX
	MOVQ n+24(FP), CX

	TESTQ CX, CX
	JLE   box_done

	// X5 = 0x00FF per word (even-byte mask), X6 = 2 per word (rounding).
	MOVQ       $0x00FF00FF00FF00FF, AX
	MOVQ       AX, X5
	PUNPCKLQDQ X5, X5
	MOVQ       $0x0002000200020002, AX
	MOVQ       AX, X6
	PUNPCKLQDQ X6, X6

	CMPB ·avx2Available(SB), $1
	JNE  box_sse2
	CMPQ CX, $16
	JLT  box_sse2

	VPBROADCASTQ X5, Y5
	VPBROADCASTQ X6, Y6

box_avx2_loop:
	VMOVDQU (SI), Y0               // row0[2i..2i+31]
	VMOVDQU (DX), Y1               // row1[2i..2i+31]

	VPAND  Y5, Y0, Y2              // row0 even bytes as words
	VPSRLW $8, Y0, Y0              // row0 odd bytes as words
	VPADDW Y2, Y0, Y0              // row0 pair sums
	VPAND  Y5, Y1, Y3
	VPSRLW $8, Y1, Y1
	VPADDW Y3, Y1, Y1              // row1 pair sums
	VPADDW Y1, Y0, Y0              // block sums, at most 1020
	VPADDW Y6, Y0, Y0
	VPSRLW $2, Y0, Y0

	// Pack 16 words -> 16 bytes. VPACKUSWB works per 128-bit lane, so the
	// results land in Q0 and Q2; VPERMQ $0x08 moves them to the low half.
	VPACKUSWB Y0, Y0, Y0
	VPERMQ    $0x08, Y0, Y0
	VMOVDQU   X0, (DI)

	ADDQ $32, SI
	ADDQ $32, DX
	ADDQ $16, DI
	SUBQ $16, CX
	CMPQ CX, $16
	JGE  box_avx2_loop

	// The low halves of X5 and X6 survive VZEROUPPER for the SSE2 tail.
	VZEROUPPER

box_sse2:
	CMPQ CX, $8
	JLT  box_scalar

box_sse2_loop:
	MOVOU (SI), X0                 // row0[2i..2i+15]
	MOVOU (DX), X1                 // row1[2i..2i+15]

	MOVO  X0, X2
	PAND  X5, X2
	PSRLW $8, X0
	PADDW X2, X0                   // row0 pair sums
	MOVO  X1, X3
	PAND  X5, X3
	PSRLW $8, X1
	PADDW X3, X1                   // row1 pair sums
	PADDW X1, X0
	PADDW X6, X0
	PSRLW $2, X0

	PACKUSWB X0, X0                // low 8 bytes = result
	MOVQ     X0, (DI)

	ADDQ $16, SI
	ADDQ $16, DX
	ADDQ $8, DI
	SUBQ $8, CX
	CMPQ CX, $8
	JGE  box_sse2_loop

box_scalar:
	TESTQ CX, CX
	JLE   box_done

box_scalar_loop:
	MOVBLZX (SI), AX               // row0[2i]
	MOVBLZX 1(SI), BX              // row0[2i+1]
	ADDQ    BX, AX
	MOVBLZX (DX), BX               // row1[2i]
	ADDQ    BX, AX
	MOVBLZX 1(DX), BX              // row1[2i+1]
	ADDQ    BX, AX
	ADDQ    $2, AX
	SHRQ    $2, AX
	MOVB    AX, (DI)
	ADDQ    $2, SI
	ADDQ    $2, DX
	INCQ    DI
	DECQ    CX
	JNZ     box_scalar_loop

box_done:
	RET
