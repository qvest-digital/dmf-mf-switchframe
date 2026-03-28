#include "textflag.h"

// AMD64 AVX2/SSE2 kernel for horizontal 2:1 alpha downsample (4:2:2).
//
// downsampleAlpha10Row averages pairs of adjacent uint8 values:
//   dst[i] = (src[2*i] + src[2*i+1] + 1) >> 1
//
// AVX2 path: 16 output samples per iteration.
//   Load 32 bytes, deinterleave even/odd using AND/SRL mask, VPAVGB.
// SSE2 path: 8 output samples per iteration.
//   Same approach with 128-bit registers.

// ============================================================================
// func downsampleAlpha10Row(dst, src *byte, count int)
// ============================================================================
TEXT ·downsampleAlpha10Row(SB), NOSPLIT, $0-24
	MOVQ dst+0(FP), DI         // dst *byte
	MOVQ src+8(FP), SI         // src *byte
	MOVQ count+16(FP), CX      // count (number of output samples)

	TESTQ CX, CX
	JLE   ds10_done

	// Check AVX2 availability
	CMPB ·avx2Available(SB), $1
	JE   ds10_avx2

	// --- SSE2 path ---
	JMP  ds10_sse2

ds10_avx2:
	// Prepare mask: 0x00FF repeated in each 16-bit word
	VPCMPEQD Y15, Y15, Y15     // Y15 = all 1s
	VPSRLW   $8, Y15, Y15      // Y15 = 0x00FF repeated

	CMPQ CX, $16
	JLT  ds10_avx2_tail

ds10_avx2_loop:
	// Load 32 bytes from src (16 pixel pairs)
	VMOVDQU (SI), Y0

	// Separate even bytes (src[0], src[2], ...) and odd bytes (src[1], src[3], ...)
	VPAND   Y15, Y0, Y1        // Y1 = even bytes as 16-bit words (0x00XX)
	VPSRLW  $8, Y0, Y2         // Y2 = odd bytes as 16-bit words

	// Average in 16-bit domain then pack to 8-bit
	VPAVGW  Y1, Y2, Y3         // Y3 = (even + odd + 1) >> 1 as 16-bit

	// Pack from 16-bit to 8-bit with unsigned saturation
	// VPACKUSWB packs within 128-bit lanes
	VPXOR   Y4, Y4, Y4
	VPACKUSWB Y4, Y3, Y3       // Pack: each lane packs src,zero → low 8 bytes per lane

	// Fix lane ordering
	VPERMQ  $0xD8, Y3, Y3      // [q0, q2, q1, q3] → results in low 128 bits

	// Store 16 output bytes
	VMOVDQU X3, (DI)

	ADDQ    $32, SI
	ADDQ    $16, DI
	SUBQ    $16, CX
	CMPQ    CX, $16
	JGE     ds10_avx2_loop

ds10_avx2_tail:
	VZEROUPPER
	TESTQ   CX, CX
	JZ      ds10_done
	// Fall through to SSE2 for remaining

ds10_sse2:
	// Prepare mask for SSE2
	PCMPEQL X15, X15            // X15 = all 1s
	PSRLW   $8, X15             // X15 = 0x00FF repeated

	CMPQ    CX, $8
	JLT     ds10_scalar

ds10_sse2_loop:
	// Load 16 bytes from src (8 pixel pairs)
	MOVOU   (SI), X0

	// Separate even/odd
	MOVOU   X0, X1
	PAND    X15, X1             // X1 = even bytes
	PSRLW   $8, X0              // X0 = odd bytes

	// Average
	PAVGW   X1, X0              // X0 = (even + odd + 1) >> 1

	// Pack 16→8
	PACKUSWB X0, X0             // Low 8 bytes = result

	// Store 8 output bytes
	MOVQ    X0, (DI)

	ADDQ    $16, SI
	ADDQ    $8, DI
	SUBQ    $8, CX
	CMPQ    CX, $8
	JGE     ds10_sse2_loop

ds10_scalar:
	TESTQ   CX, CX
	JZ      ds10_done

ds10_scalar_loop:
	MOVBLZX (SI), AX            // src[2*i]
	MOVBLZX 1(SI), BX           // src[2*i+1]
	ADDQ    $2, SI

	// (a + b + 1) >> 1
	ADDQ    BX, AX
	INCQ    AX
	SHRQ    $1, AX

	MOVB    AX, (DI)
	INCQ    DI

	DECQ    CX
	JNZ     ds10_scalar_loop

ds10_done:
	RET
