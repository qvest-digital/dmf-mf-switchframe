#include "textflag.h"

// AMD64 downsample alpha kernel for 2x2 box average.
// AVX2 (32 input bytes → 16 output bytes/iter) with SSE2 fallback (16 → 8).
//
// Uses VPAVGB/PAVGB for rounding halving add: (a+b+1)/2.
// Two rounds of VPAVGB may differ from (a+b+c+d+2)/4 by at most +/-1,
// which is acceptable for alpha downsampling.

// ============================================================================
// func downsampleAlpha2x2(dst, row0, row1 *byte, pairs int)
// ============================================================================
// dst[i] = avg2x2(row0[2*i], row0[2*i+1], row1[2*i], row1[2*i+1])
TEXT ·downsampleAlpha2x2(SB), NOSPLIT, $0-32
	MOVQ dst+0(FP), DI
	MOVQ row0+8(FP), SI
	MOVQ row1+16(FP), DX
	MOVQ pairs+24(FP), CX

	TESTQ CX, CX
	JLE   ds_done

	// Check AVX2 availability
	CMPB ·avx2Available(SB), $1
	JE   ds_avx2

	// --- SSE2 path (16 input bytes = 8 pairs per iteration) ---
	// Even byte mask: 0x00FF repeated for 8 words
	MOVQ       $0x00FF00FF00FF00FF, AX
	MOVQ       AX, X5
	PUNPCKLQDQ X5, X5              // X5 = [0x00FF] × 8 words

	CMPQ CX, $8
	JLT  ds_sse2_tail

ds_sse2_loop:
	// Load 16 bytes from each row (8 pairs)
	MOVOU (SI), X0                 // row0[2*i..2*i+15]
	MOVOU (DX), X1                 // row1[2*i..2*i+15]

	// Vertical average: (row0 + row1 + 1) / 2
	PAVGB X1, X0                   // X0 = vavg (16 bytes)

	// Horizontal pair average: deinterleave even/odd bytes
	MOVO  X0, X1                   // copy
	PAND  X5, X0                   // X0 = even bytes as words [v0, v2, v4, ...]
	PSRLW $8, X1                   // X1 = odd bytes as words [v1, v3, v5, ...]

	// Average even and odd: (even + odd + 1) / 2
	PAVGW X1, X0                   // X0 = result as words (each in [0,255])

	// Pack 8 words → 8 bytes (upper 8 bytes zeroed via X4)
	PXOR     X4, X4
	PACKUSWB X4, X0                // X0 lower 8 bytes = result
	MOVQ     X0, (DI)

	ADDQ $16, SI                   // row0 += 16 (8 pairs × 2 bytes)
	ADDQ $16, DX                   // row1 += 16
	ADDQ $8, DI                    // dst += 8
	SUBQ $8, CX
	CMPQ CX, $8
	JGE  ds_sse2_loop

ds_sse2_tail:
	TESTQ CX, CX
	JZ    ds_done

ds_scalar_loop:
	MOVBLZX (SI), AX               // row0[2*i]
	MOVBLZX 1(SI), BX              // row0[2*i+1]
	ADDQ    BX, AX                  // row0[2*i] + row0[2*i+1]
	MOVBLZX (DX), BX               // row1[2*i]
	ADDQ    BX, AX
	MOVBLZX 1(DX), BX              // row1[2*i+1]
	ADDQ    BX, AX
	ADDQ    $2, AX                  // + 2 (rounding)
	SHRQ    $2, AX                  // / 4
	MOVB    AX, (DI)
	ADDQ    $2, SI
	ADDQ    $2, DX
	INCQ    DI
	DECQ    CX
	JNZ     ds_scalar_loop
	JMP     ds_done

ds_avx2:
	// --- AVX2 path (32 input bytes = 16 pairs per iteration) ---
	// Even byte mask: 0x00FF repeated for 16 words
	MOVQ         $0x00FF00FF00FF00FF, AX
	MOVQ         AX, X5
	VPBROADCASTQ X5, Y5            // Y5 = [0x00FF] × 16 words

	// Shuffle mask to consolidate results from per-lane to contiguous.
	// After VPACKUSWB, useful bytes are in Q0 and Q2. VPERMQ $0x08
	// reorders [Q0,Q2,Q0,Q0] so lower 128 bits = 16 contiguous results.

	CMPQ CX, $16
	JLT  ds_avx2_tail

ds_avx2_loop:
	VMOVDQU (SI), Y0               // row0[2*i..2*i+31]
	VMOVDQU (DX), Y1               // row1[2*i..2*i+31]

	// Vertical average: (row0 + row1 + 1) / 2
	VPAVGB Y1, Y0, Y0              // Y0 = vavg (32 bytes)

	// Horizontal pair average: deinterleave even/odd bytes
	VPAND  Y5, Y0, Y1              // Y1 = even bytes as words
	VPSRLW $8, Y0, Y2              // Y2 = odd bytes as words

	// Average even and odd: (even + odd + 1) / 2
	VPAVGW Y2, Y1, Y0              // Y0 = result as words (each in [0,255])

	// Pack 16 words → 16 bytes
	VPXOR    Y3, Y3, Y3
	VPACKUSWB Y3, Y0, Y0           // Pack with zeros: results in Q0 and Q2
	VPERMQ   $0x08, Y0, Y0         // [Q0,Q2,Q0,Q0] → lower X0 = 16 result bytes

	VMOVDQU X0, (DI)

	ADDQ $32, SI
	ADDQ $32, DX
	ADDQ $16, DI
	SUBQ $16, CX
	CMPQ CX, $16
	JGE  ds_avx2_loop

ds_avx2_tail:
	VZEROUPPER
	// Fall through to SSE2/scalar for remaining pairs
	TESTQ CX, CX
	JZ    ds_done
	CMPQ  CX, $8
	JLT   ds_scalar_loop_avx2

	// Set up SSE2 constants for tail
	MOVQ       $0x00FF00FF00FF00FF, AX
	MOVQ       AX, X5
	PUNPCKLQDQ X5, X5

ds_avx2_sse2_tail:
	MOVOU (SI), X0
	MOVOU (DX), X1
	PAVGB X1, X0
	MOVO  X0, X1
	PAND  X5, X0
	PSRLW $8, X1
	PAVGW X1, X0
	PXOR     X4, X4
	PACKUSWB X4, X0
	MOVQ     X0, (DI)
	ADDQ  $16, SI
	ADDQ  $16, DX
	ADDQ  $8, DI
	SUBQ  $8, CX
	CMPQ  CX, $8
	JGE   ds_avx2_sse2_tail

ds_scalar_loop_avx2:
	TESTQ CX, CX
	JZ    ds_done
	MOVBLZX (SI), AX
	MOVBLZX 1(SI), BX
	ADDQ    BX, AX
	MOVBLZX (DX), BX
	ADDQ    BX, AX
	MOVBLZX 1(DX), BX
	ADDQ    BX, AX
	ADDQ    $2, AX
	SHRQ    $2, AX
	MOVB    AX, (DI)
	ADDQ    $2, SI
	ADDQ    $2, DX
	INCQ    DI
	DECQ    CX
	JNZ     ds_scalar_loop_avx2

ds_done:
	RET
