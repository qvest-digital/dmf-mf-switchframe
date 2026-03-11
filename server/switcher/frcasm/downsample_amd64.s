#include "textflag.h"

// AMD64 SSE2/AVX2 2× Y-plane downsample using box filter.
//
// Algorithm: For each 2×2 source block, compute:
//   avg(avg(row0[2c], row0[2c+1]), avg(row1[2c], row1[2c+1]))
// SSE2: PAND/PSRLW to deinterleave, PAVGW for horizontal average, PACKUSWB + PAVGB.
// AVX2: Same approach with 256-bit registers, 16 output pixels per iteration.
//
// SSE2 approach (8 output pixels per iteration):
//   Load 16 bytes from row0 (8 pixel pairs).
//   Separate even/odd: even = AND mask_00ff, odd = PSRLW 8.
//   PAVGW even, odd → horizontal average (16-bit words).
//   Same for row1.
//   PAVGW row0_avg, row1_avg → vertical average (16-bit words).
//   PACKUSWB → 8 output bytes.


// ============================================================================
// func DownsampleY2x(dst, src *byte, srcW, srcH int)
// ============================================================================
// Stack layout (Go ABI0):
//   dst    +0(FP)    *byte
//   src    +8(FP)    *byte
//   srcW   +16(FP)   int
//   srcH   +24(FP)   int
//
// Register assignment:
//   SI  = src row0 pointer (advances per row pair)
//   DI  = dst pointer (advances)
//   R8  = srcW
//   R9  = src row1 pointer (= row0 + srcW)
//   R10 = dstW (= srcW / 2)
//   R11 = remaining output rows (dstH = srcH / 2)
//   CX  = remaining output cols per row
//   X15 = mask_00ff constant (SSE2 path)
//
TEXT ·DownsampleY2x(SB), NOSPLIT, $0-32
	MOVQ dst+0(FP), DI          // dst pointer
	MOVQ src+8(FP), SI          // src pointer (= row0)
	MOVQ srcW+16(FP), R8        // source width
	MOVQ srcH+24(FP), R11       // source height

	// dstW = srcW / 2
	MOVQ R8, R10
	SHRQ $1, R10                // R10 = dstW

	// dstH = srcH / 2
	SHRQ $1, R11                // R11 = dstH (remaining rows)

	// row1 = row0 + srcW
	MOVQ SI, R9
	ADDQ R8, R9                 // R9 = src + srcW = row1

	TESTQ R11, R11
	JZ    ds_done

	// Check AVX2 availability
	CMPB ·avx2Available(SB), $1
	JE   ds_avx2_rows

	// --- SSE2 path ---
	// Set up mask_00ff in X15
	PCMPEQL X15, X15            // X15 = all 1s
	PSRLW   $8, X15             // X15 = 0x00FF repeated (mask for even bytes)

ds_sse2_row_loop:
	MOVQ R10, CX                // CX = remaining output cols

	CMPQ CX, $8
	JLT  ds_sse2_tail

ds_sse2_loop:
	// Load 16 bytes from row0 (8 pixel pairs)
	MOVOU (SI), X0
	MOVOU X0, X1
	PAND  X15, X0               // X0 = even bytes as 16-bit words
	PSRLW $8, X1                // X1 = odd bytes as 16-bit words
	PAVGW X1, X0                // X0 = row0 horizontal averages (16-bit)

	// Load 16 bytes from row1 (8 pixel pairs)
	MOVOU (R9), X2
	MOVOU X2, X3
	PAND  X15, X2               // X2 = even bytes as 16-bit words
	PSRLW $8, X3                // X3 = odd bytes as 16-bit words
	PAVGW X3, X2                // X2 = row1 horizontal averages (16-bit)

	// Vertical average in 16-bit domain
	PAVGW X2, X0                // X0 = final average (16-bit words)

	// Pack from 16-bit to 8-bit
	PACKUSWB X0, X0             // low 8 bytes = result (high 8 = duplicate)

	// Store 8 output pixels
	MOVQ X0, (DI)

	ADDQ $16, SI                // advance row0 by 16 source bytes
	ADDQ $16, R9                // advance row1 by 16 source bytes
	ADDQ $8, DI                 // advance dst by 8 output bytes
	SUBQ $8, CX
	CMPQ CX, $8
	JGE  ds_sse2_loop

ds_sse2_tail:
	TESTQ CX, CX
	JZ    ds_sse2_row_advance

ds_sse2_scalar:
	// Scalar: load 2 bytes from each row, cascaded average
	MOVBLZX (SI), AX            // row0[2c]
	MOVBLZX 1(SI), BX           // row0[2c+1]
	ADDQ    $2, SI

	MOVBLZX (R9), DX            // row1[2c]
	MOVBLZX 1(R9), R12          // row1[2c+1]
	ADDQ    $2, R9

	// havg0 = (a + b + 1) >> 1
	ADDQ BX, AX
	INCQ AX
	SHRQ $1, AX

	// havg1 = (c + d + 1) >> 1
	ADDQ R12, DX
	INCQ DX
	SHRQ $1, DX

	// final = (havg0 + havg1 + 1) >> 1
	ADDQ DX, AX
	INCQ AX
	SHRQ $1, AX

	MOVB AX, (DI)
	INCQ DI

	DECQ CX
	JNZ  ds_sse2_scalar

ds_sse2_row_advance:
	// row0 and row1 have been advanced by srcW bytes (dstW*2).
	// Next row0 = current row1 position, next row1 = next row0 + srcW.
	MOVQ R9, SI                 // row0 = old row1 end position
	MOVQ SI, R9
	ADDQ R8, R9                 // row1 = new row0 + srcW

	DECQ R11
	JNZ  ds_sse2_row_loop
	JMP  ds_done

	// --- AVX2 path ---
ds_avx2_rows:

ds_avx2_row_loop:
	// Set up mask_00ff in Y15 (256-bit).
	// Must be inside the row loop because VZEROUPPER at the row tail
	// zeros the upper 128 bits of all YMM registers including Y15.
	VPCMPEQD Y15, Y15, Y15     // Y15 = all 1s
	VPSRLW   $8, Y15, Y15      // Y15 = 0x00FF repeated

	MOVQ R10, CX                // CX = remaining output cols

	CMPQ CX, $16
	JLT  ds_avx2_sse2_tail

ds_avx2_loop:
	// Load 32 bytes from row0 (16 pixel pairs)
	VMOVDQU (SI), Y0
	VPAND   Y15, Y0, Y1        // Y1 = even bytes as 16-bit
	VPSRLW  $8, Y0, Y0         // Y0 = odd bytes as 16-bit
	VPAVGW  Y0, Y1, Y1         // Y1 = row0 horizontal averages

	// Load 32 bytes from row1 (16 pixel pairs)
	VMOVDQU (R9), Y2
	VPAND   Y15, Y2, Y3        // Y3 = even bytes as 16-bit
	VPSRLW  $8, Y2, Y2         // Y2 = odd bytes as 16-bit
	VPAVGW  Y2, Y3, Y3         // Y3 = row1 horizontal averages

	// Vertical average in 16-bit domain
	VPAVGW  Y3, Y1, Y1         // Y1 = final average (16-bit)

	// Pack from 16-bit to 8-bit
	// VPACKUSWB packs within 128-bit lanes, so we need to fix lane ordering.
	// Y1 = [lane0_lo8, lane1_lo8, lane0_hi8, lane1_hi8] after VPACKUSWB
	VPACKUSWB Y1, Y1, Y1       // Pack: each lane independently

	// VPACKUSWB with same input gives:
	// lane0: pack(Y1_lo128, Y1_lo128) = [8 bytes result, 8 bytes duplicate]
	// lane1: pack(Y1_hi128, Y1_hi128) = [8 bytes result, 8 bytes duplicate]
	// We need to extract and combine the low 8 bytes from each lane.

	// Extract low 64-bits from lane0 and lane1:
	// Use VPERMQ to rearrange: want qword 0 and qword 2 adjacent.
	VPERMQ $0xD8, Y1, Y1       // 0xD8 = 11_01_10_00 → [q0, q2, q1, q3]

	// Now low 128 bits has our 16 output bytes (q0 from lane0, q2 from lane1)
	VMOVDQU X1, (DI)           // store 16 output pixels

	ADDQ $32, SI
	ADDQ $32, R9
	ADDQ $16, DI
	SUBQ $16, CX
	CMPQ CX, $16
	JGE  ds_avx2_loop

ds_avx2_sse2_tail:
	VZEROUPPER                  // transition to SSE2 for tail

	// Fall through to SSE2 for remaining 8+ pixel chunks
	CMPQ CX, $8
	JLT  ds_avx2_scalar_tail

	// Reuse SSE2 mask (set up X15 from scratch since VZEROUPPER cleared it)
	PCMPEQL X15, X15
	PSRLW   $8, X15

ds_avx2_sse2_loop:
	MOVOU (SI), X0
	MOVOU X0, X1
	PAND  X15, X0
	PSRLW $8, X1
	PAVGW X1, X0

	MOVOU (R9), X2
	MOVOU X2, X3
	PAND  X15, X2
	PSRLW $8, X3
	PAVGW X3, X2

	PAVGW X2, X0
	PACKUSWB X0, X0
	MOVQ X0, (DI)

	ADDQ $16, SI
	ADDQ $16, R9
	ADDQ $8, DI
	SUBQ $8, CX
	CMPQ CX, $8
	JGE  ds_avx2_sse2_loop

ds_avx2_scalar_tail:
	TESTQ CX, CX
	JZ    ds_avx2_row_advance

ds_avx2_scalar:
	MOVBLZX (SI), AX
	MOVBLZX 1(SI), BX
	ADDQ    $2, SI

	MOVBLZX (R9), DX
	MOVBLZX 1(R9), R12
	ADDQ    $2, R9

	ADDQ BX, AX
	INCQ AX
	SHRQ $1, AX

	ADDQ R12, DX
	INCQ DX
	SHRQ $1, DX

	ADDQ DX, AX
	INCQ AX
	SHRQ $1, AX

	MOVB AX, (DI)
	INCQ DI

	DECQ CX
	JNZ  ds_avx2_scalar

ds_avx2_row_advance:
	MOVQ R9, SI
	MOVQ SI, R9
	ADDQ R8, R9

	DECQ R11
	JNZ  ds_avx2_row_loop

ds_done:
	RET
