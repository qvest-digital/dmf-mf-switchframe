#include "textflag.h"

// AMD64 unrolled kernel for RGBA-to-YUV chroma (Cb/Cr) alpha blending.
//
// func alphaBlendRGBAChromaRow(cbRow *byte, crRow *byte, rgba *byte,
//                               chromaWidth int, alphaScale256 int)
//
// Processes chroma at half resolution: each chroma pixel corresponds to
// a 2x2 block of full-resolution pixels. RGBA is sampled at stride 8
// (every other full-res pixel in the row).
//
// Unrolled 2x to reduce loop overhead.
TEXT ·alphaBlendRGBAChromaRow(SB), NOSPLIT, $8-40
	MOVQ cbRow+0(FP), DI       // DI = cbRow
	MOVQ crRow+8(FP), SI       // SI = crRow
	MOVQ rgba+16(FP), DX       // DX = rgba
	MOVQ chromaWidth+24(FP), CX // CX = chromaWidth
	MOVQ alphaScale256+32(FP), R8 // R8 = alphaScale256

	TESTQ CX, CX
	JLE   chroma_done_amd64

	// Check if we have at least 2 for unrolled path
	CMPQ  CX, $2
	JLT   chroma_scalar_tail

chroma_unrolled_amd64:
	// ---- Pixel 0 ----
	MOVQ CX, 0(SP)             // save loop counter

	MOVBQZX 3(DX), AX
	MOVQ AX, R9
	SHRQ $7, R9
	ADDQ R9, AX
	IMULQ R8, AX
	SHRQ $8, AX
	TESTQ AX, AX
	JZ   chroma_skip0_amd64

	MOVBQZX (DX), R9           // R
	MOVBQZX 1(DX), R10         // G
	MOVBQZX 2(DX), R11         // B

	// overlayCb = (112*B - 26*R - 86*G + 128) >> 8 + 128
	MOVQ $112, R15
	IMULQ R11, R15
	MOVQ R15, R12
	ADDQ $128, R12
	MOVQ $26, R13
	IMULQ R9, R13
	SUBQ R13, R12
	MOVQ $86, R13
	IMULQ R10, R13
	SUBQ R13, R12
	SARQ $8, R12
	ADDQ $128, R12              // overlayCb

	// overlayCr = (112*R - 102*G - 10*B + 128) >> 8 + 128
	MOVQ $112, R15
	IMULQ R9, R15
	MOVQ R15, R13
	ADDQ $128, R13
	MOVQ $102, R14
	IMULQ R10, R14
	SUBQ R14, R13
	MOVQ $10, R14
	IMULQ R11, R14
	SUBQ R14, R13
	SARQ $8, R13
	ADDQ $128, R13              // overlayCr

	// inv = 256 - a256
	MOVQ $256, R14
	SUBQ AX, R14

	// Blend Cb
	MOVBQZX (DI), R15
	IMULQ R14, R15
	IMULQ AX, R12
	ADDQ R12, R15
	ADDQ $128, R15
	SARQ $8, R15
	TESTQ R15, R15
	JGE  chroma_cb_nonneg0
	XORQ R15, R15
	JMP  chroma_cb_ok0
chroma_cb_nonneg0:
	CMPQ R15, $255
	JLE  chroma_cb_ok0
	MOVQ $255, R15
chroma_cb_ok0:
	MOVB R15, (DI)

	// Blend Cr
	MOVBQZX (SI), R15
	IMULQ R14, R15
	IMULQ AX, R13
	ADDQ R13, R15
	ADDQ $128, R15
	SARQ $8, R15
	TESTQ R15, R15
	JGE  chroma_cr_nonneg0
	XORQ R15, R15
	JMP  chroma_cr_ok0
chroma_cr_nonneg0:
	CMPQ R15, $255
	JLE  chroma_cr_ok0
	MOVQ $255, R15
chroma_cr_ok0:
	MOVB R15, (SI)

chroma_skip0_amd64:
	// ---- Pixel 1 ----
	MOVBQZX 11(DX), AX         // alpha at offset 8+3=11
	MOVQ AX, R9
	SHRQ $7, R9
	ADDQ R9, AX
	IMULQ R8, AX
	SHRQ $8, AX
	TESTQ AX, AX
	JZ   chroma_skip1_amd64

	MOVBQZX 8(DX), R9          // R
	MOVBQZX 9(DX), R10         // G
	MOVBQZX 10(DX), R11        // B

	MOVQ $112, R15
	IMULQ R11, R15
	MOVQ R15, R12
	ADDQ $128, R12
	MOVQ $26, R13
	IMULQ R9, R13
	SUBQ R13, R12
	MOVQ $86, R13
	IMULQ R10, R13
	SUBQ R13, R12
	SARQ $8, R12
	ADDQ $128, R12

	MOVQ $112, R15
	IMULQ R9, R15
	MOVQ R15, R13
	ADDQ $128, R13
	MOVQ $102, R14
	IMULQ R10, R14
	SUBQ R14, R13
	MOVQ $10, R14
	IMULQ R11, R14
	SUBQ R14, R13
	SARQ $8, R13
	ADDQ $128, R13

	MOVQ $256, R14
	SUBQ AX, R14

	MOVBQZX 1(DI), R15
	IMULQ R14, R15
	IMULQ AX, R12
	ADDQ R12, R15
	ADDQ $128, R15
	SARQ $8, R15
	TESTQ R15, R15
	JGE  chroma_cb_nonneg1
	XORQ R15, R15
	JMP  chroma_cb_ok1
chroma_cb_nonneg1:
	CMPQ R15, $255
	JLE  chroma_cb_ok1
	MOVQ $255, R15
chroma_cb_ok1:
	MOVB R15, 1(DI)

	MOVBQZX 1(SI), R15
	IMULQ R14, R15
	IMULQ AX, R13
	ADDQ R13, R15
	ADDQ $128, R15
	SARQ $8, R15
	TESTQ R15, R15
	JGE  chroma_cr_nonneg1
	XORQ R15, R15
	JMP  chroma_cr_ok1
chroma_cr_nonneg1:
	CMPQ R15, $255
	JLE  chroma_cr_ok1
	MOVQ $255, R15
chroma_cr_ok1:
	MOVB R15, 1(SI)

chroma_skip1_amd64:
	MOVQ 0(SP), CX             // restore counter

	ADDQ $16, DX               // advance 2 chroma pixels (stride 8 * 2)
	ADDQ $2, DI
	ADDQ $2, SI
	SUBQ $2, CX
	CMPQ CX, $2
	JGE  chroma_unrolled_amd64

chroma_scalar_tail:
	TESTQ CX, CX
	JLE   chroma_done_amd64

chroma_loop_amd64:
	MOVQ CX, 0(SP)

	MOVBQZX 3(DX), AX
	MOVQ AX, R9
	SHRQ $7, R9
	ADDQ R9, AX
	IMULQ R8, AX
	SHRQ $8, AX
	TESTQ AX, AX
	JZ   chroma_skip_scalar_amd64

	MOVBQZX (DX), R9
	MOVBQZX 1(DX), R10
	MOVBQZX 2(DX), R11

	MOVQ $112, R15
	IMULQ R11, R15
	MOVQ R15, R12
	ADDQ $128, R12
	MOVQ $26, R13
	IMULQ R9, R13
	SUBQ R13, R12
	MOVQ $86, R13
	IMULQ R10, R13
	SUBQ R13, R12
	SARQ $8, R12
	ADDQ $128, R12

	MOVQ $112, R15
	IMULQ R9, R15
	MOVQ R15, R13
	ADDQ $128, R13
	MOVQ $102, R14
	IMULQ R10, R14
	SUBQ R14, R13
	MOVQ $10, R14
	IMULQ R11, R14
	SUBQ R14, R13
	SARQ $8, R13
	ADDQ $128, R13

	MOVQ $256, R14
	SUBQ AX, R14

	MOVBQZX (DI), R15
	IMULQ R14, R15
	IMULQ AX, R12
	ADDQ R12, R15
	ADDQ $128, R15
	SARQ $8, R15
	TESTQ R15, R15
	JGE  chroma_cb_nonneg_amd64
	XORQ R15, R15
	JMP  chroma_cb_ok_amd64
chroma_cb_nonneg_amd64:
	CMPQ R15, $255
	JLE  chroma_cb_ok_amd64
	MOVQ $255, R15
chroma_cb_ok_amd64:
	MOVB R15, (DI)

	MOVBQZX (SI), R15
	IMULQ R14, R15
	IMULQ AX, R13
	ADDQ R13, R15
	ADDQ $128, R15
	SARQ $8, R15
	TESTQ R15, R15
	JGE  chroma_cr_nonneg_amd64
	XORQ R15, R15
	JMP  chroma_cr_ok_amd64
chroma_cr_nonneg_amd64:
	CMPQ R15, $255
	JLE  chroma_cr_ok_amd64
	MOVQ $255, R15
chroma_cr_ok_amd64:
	MOVB R15, (SI)

chroma_skip_scalar_amd64:
	MOVQ 0(SP), CX

	ADDQ $8, DX
	INCQ DI
	INCQ SI
	DECQ CX
	JNZ  chroma_loop_amd64

chroma_done_amd64:
	RET
