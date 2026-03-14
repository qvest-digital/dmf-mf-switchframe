#include "textflag.h"

// AMD64 scalar kernel for RGBA-to-YUV chroma (Cb/Cr) alpha blending.
//
// func alphaBlendRGBAChromaRow(cbRow *byte, crRow *byte, rgba *byte,
//                               chromaWidth int, alphaScale256 int)
//
// Scalar implementation: eliminates bounds checks for stride-8 RGBA access.
TEXT ·alphaBlendRGBAChromaRow(SB), NOSPLIT, $8-40
	MOVQ cbRow+0(FP), DI       // DI = cbRow
	MOVQ crRow+8(FP), SI       // SI = crRow
	MOVQ rgba+16(FP), DX       // DX = rgba
	MOVQ chromaWidth+24(FP), CX // CX = chromaWidth
	MOVQ alphaScale256+32(FP), R8 // R8 = alphaScale256

	TESTQ CX, CX
	JLE   chroma_done_amd64

chroma_loop_amd64:
	// Save CX (loop counter) — we need all other regs for math
	MOVQ CX, 0(SP)

	// Load alpha from rgba[3]
	MOVBQZX 3(DX), AX          // AX = A
	MOVQ AX, R9
	SHRQ $7, R9                // R9 = A >> 7
	ADDQ R9, AX               // AX = A' = A + (A >> 7)
	IMULQ R8, AX               // AX = A' * alphaScale256
	SHRQ $8, AX                // AX = a256
	TESTQ AX, AX
	JZ   chroma_skip_amd64

	// Load R, G, B
	MOVBQZX (DX), R9           // R9 = R
	MOVBQZX 1(DX), R10         // R10 = G
	MOVBQZX 2(DX), R11         // R11 = B

	// overlayCb = (112*B - 26*R - 86*G + 128) >> 8 + 128
	MOVQ $112, R15
	IMULQ R11, R15              // R15 = 112*B
	MOVQ R15, R12               // R12 = 112*B
	ADDQ $128, R12             // + 128
	MOVQ $26, R13
	IMULQ R9, R13               // R13 = 26*R
	SUBQ R13, R12              // - 26*R
	MOVQ $86, R13
	IMULQ R10, R13              // R13 = 86*G
	SUBQ R13, R12              // - 86*G
	SARQ $8, R12               // >> 8 (arithmetic)
	ADDQ $128, R12             // + 128 = overlayCb

	// overlayCr = (112*R - 102*G - 10*B + 128) >> 8 + 128
	MOVQ $112, R15
	IMULQ R9, R15               // R15 = 112*R
	MOVQ R15, R13               // R13 = 112*R
	ADDQ $128, R13             // + 128
	MOVQ $102, R14
	IMULQ R10, R14              // R14 = 102*G
	SUBQ R14, R13              // - 102*G
	MOVQ $10, R14
	IMULQ R11, R14              // R14 = 10*B
	SUBQ R14, R13              // - 10*B
	SARQ $8, R13               // >> 8
	ADDQ $128, R13             // + 128 = overlayCr

	// inv = 256 - a256
	MOVQ $256, R14
	SUBQ AX, R14               // R14 = inv

	// Blend Cb: (existing*inv + overlayCb*a256 + 128) >> 8
	MOVBQZX (DI), R15          // existing Cb
	IMULQ R14, R15              // existing * inv
	IMULQ AX, R12               // overlayCb * a256
	ADDQ R12, R15
	ADDQ $128, R15
	SARQ $8, R15                // arithmetic shift (preserves sign)
	TESTQ R15, R15
	JGE  chroma_cb_nonneg_amd64
	XORQ R15, R15               // clamp to 0
	JMP  chroma_cb_ok_amd64
chroma_cb_nonneg_amd64:
	CMPQ R15, $255
	JLE  chroma_cb_ok_amd64
	MOVQ $255, R15
chroma_cb_ok_amd64:
	MOVB R15, (DI)

	// Blend Cr: (existing*inv + overlayCr*a256 + 128) >> 8
	MOVBQZX (SI), R15          // existing Cr
	IMULQ R14, R15
	IMULQ AX, R13               // overlayCr * a256
	ADDQ R13, R15
	ADDQ $128, R15
	SARQ $8, R15                // arithmetic shift (preserves sign)
	TESTQ R15, R15
	JGE  chroma_cr_nonneg_amd64
	XORQ R15, R15               // clamp to 0
	JMP  chroma_cr_ok_amd64
chroma_cr_nonneg_amd64:
	CMPQ R15, $255
	JLE  chroma_cr_ok_amd64
	MOVQ $255, R15
chroma_cr_ok_amd64:
	MOVB R15, (SI)

chroma_skip_amd64:
	// Restore CX
	MOVQ 0(SP), CX

	ADDQ $8, DX                // next RGBA (stride 8)
	INCQ DI                    // next Cb
	INCQ SI                    // next Cr
	DECQ CX
	JNZ  chroma_loop_amd64

chroma_done_amd64:
	RET
