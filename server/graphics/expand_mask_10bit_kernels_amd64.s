#include "textflag.h"

// AMD64 SSE2 kernel for 4:2:2 chroma→luma mask row expansion.
// Each input byte is duplicated to produce two output bytes.
//
// func expandChromaMask10Row(dst *byte, src *byte, count int)

// SSE2: 8 input bytes → 16 output bytes per iteration using
// PUNPCKLBW to interleave each byte with its duplicate.

TEXT ·expandChromaMask10Row(SB), NOSPLIT, $0-24
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ count+16(FP), CX

	TESTQ CX, CX
	JLE   expand10_done

	CMPQ  CX, $8
	JLT   expand10_scalar

expand10_sse2_loop:
	// Load 8 bytes from src into low 64 bits of X0
	MOVQ  (SI), X0

	// Duplicate: PUNPCKLBW with self interleaves each byte pair
	MOVOU X0, X1
	PUNPCKLBW X1, X0          // X0 = [a0,a0,a1,a1,...,a7,a7] (16 bytes)

	// Store 16 output bytes
	MOVOU X0, (DI)

	ADDQ  $8, SI
	ADDQ  $16, DI
	SUBQ  $8, CX
	CMPQ  CX, $8
	JGE   expand10_sse2_loop

expand10_scalar:
	TESTQ CX, CX
	JZ    expand10_done

expand10_scalar_loop:
	MOVBLZX (SI), AX
	MOVB  AX, (DI)
	MOVB  AX, 1(DI)
	INCQ  SI
	ADDQ  $2, DI
	DECQ  CX
	JNZ   expand10_scalar_loop

expand10_done:
	RET
