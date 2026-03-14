#include "textflag.h"

// ARM64 NEON kernel for RGBA-to-YUV chroma (Cb/Cr) alpha blending.
//
// func alphaBlendRGBAChromaRow(cbRow *byte, crRow *byte, rgba *byte,
//                               chromaWidth int, alphaScale256 int)

// --- NEON macros ---
// UZP1 Vd.4S, Vn.4S, Vm.4S — extract even-indexed 32-bit elements
#define UZP1_4S(Vd, Vn, Vm) WORD $(0x4E801800 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
// USHR Vd.4S, Vn.4S, #imm — unsigned right shift
#define USHR_4S(Vd, Vn, shift) WORD $(0x6F200400 | ((64-(shift))<<16) | ((Vn)<<5) | (Vd))
// SSHR Vd.4S, Vn.4S, #imm — signed right shift
#define SSHR_4S(Vd, Vn, shift) WORD $(0x4F200400 | ((64-(shift))<<16) | ((Vn)<<5) | (Vd))
// MUL Vd.4S, Vn.4S, Vm.4S — integer multiply
#define VMUL_4S(Vd, Vn, Vm) WORD $(0x4EA09C00 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
// MLS Vd.4S, Vn.4S, Vm.4S — Vd -= Vn * Vm
#define VMLS_4S(Vd, Vn, Vm) WORD $(0x6EA09400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
// MLA Vd.4S, Vn.4S, Vm.4S — Vd += Vn * Vm
#define VMLA_4S(Vd, Vn, Vm) WORD $(0x4EA09400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
// UQXTN Vd.4H, Vn.4S — unsigned saturating narrow uint32→uint16
#define UQXTN_4H(Vd, Vn) WORD $(0x2E614800 | ((Vn)<<5) | (Vd))
// UQXTN Vd.8B, Vn.8H — unsigned saturating narrow uint16→uint8
#define UQXTN_8B(Vd, Vn) WORD $(0x2E214800 | ((Vn)<<5) | (Vd))
// DUP Vd.4S, Rn — broadcast GP register to all lanes
#define VDUP_4S(Vd, Rn) WORD $(0x4E040C00 | ((Rn)<<5) | (Vd))
// USHLL Vd.8H, Vn.8B, #0 — zero-extend bytes to uint16
#define USHLL_8H(Vd, Vn) WORD $(0x2F08A400 | ((Vn)<<5) | (Vd))
// USHLL Vd.4S, Vn.4H, #0 — zero-extend uint16 to uint32
#define USHLL_4S(Vd, Vn) WORD $(0x2F10A400 | ((Vn)<<5) | (Vd))
// SMAX Vd.4S, Vn.4S, Vm.4S — signed maximum (clamp negatives to 0 using zero vector)
#define VSMAX_4S(Vd, Vn, Vm) WORD $(0x4EA06400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// Registers:
//   R0 = cbRow, R1 = crRow, R2 = rgba, R3 = chromaWidth, R4 = alphaScale256
//   V23 = alphaScale256 broadcast
//   V24 = byte mask 0xFF per .4S
//   V25 = constant 128 per .4S
//   V26 = constant 256 per .4S
//   V27-V31 = coefficients [26, 86, 112, 102, 10]
TEXT ·alphaBlendRGBAChromaRow(SB), NOSPLIT, $0-40
	MOVD cbRow+0(FP), R0
	MOVD crRow+8(FP), R1
	MOVD rgba+16(FP), R2
	MOVD chromaWidth+24(FP), R3
	MOVD alphaScale256+32(FP), R4

	CMP  $0, R3
	BLE  chroma_done

	// Set up constant vectors
	MOVD  $0xFF, R5
	VDUP_4S(24, 5)
	MOVD  $128, R5
	VDUP_4S(25, 5)
	MOVD  $256, R5
	VDUP_4S(26, 5)
	MOVD  $26, R5
	VDUP_4S(27, 5)
	MOVD  $86, R5
	VDUP_4S(28, 5)
	MOVD  $112, R5
	VDUP_4S(29, 5)
	MOVD  $102, R5
	VDUP_4S(30, 5)
	MOVD  $10, R5
	VDUP_4S(31, 5)
	VDUP_4S(23, 4)
	// V22 = zero vector for negative clamping
	VEOR V22.B16, V22.B16, V22.B16

	CMP  $4, R3
	BLT  chroma_scalar

chroma_neon4:
	// Load 8 RGBA pixels (32 bytes)
	VLD1 (R2), [V0.S4, V1.S4]

	// Extract even pixels: V2 = [RGBA0, RGBA2, RGBA4, RGBA6]
	UZP1_4S(2, 0, 1)

	// Extract alpha: A = V2 >> 24
	USHR_4S(6, 2, 24)

	// Quick check: skip if all alphas are 0
	VMOV V6.S[0], R5
	VMOV V6.S[1], R6
	ORR  R6, R5, R5
	VMOV V6.S[2], R6
	ORR  R6, R5, R5
	VMOV V6.S[3], R6
	ORR  R6, R5, R5
	CBZ  R5, chroma_skip4

	// Alpha: a256 = (A + (A >> 7)) * alphaScale256 >> 8
	USHR_4S(7, 6, 7)
	VADD V6.S4, V7.S4, V6.S4
	VMUL_4S(6, 6, 23)
	USHR_4S(6, 6, 8)

	// Extract R, G, B from packed RGBA
	VAND V2.B16, V24.B16, V3.B16  // R = V2 & 0xFF
	USHR_4S(4, 2, 8)
	VAND V4.B16, V24.B16, V4.B16  // G
	USHR_4S(5, 2, 16)
	VAND V5.B16, V24.B16, V5.B16  // B

	// overlayCb = (112*B - 26*R - 86*G + 128) >> 8 + 128
	VMUL_4S(8, 5, 29)             // 112*B
	VADD V8.S4, V25.S4, V8.S4    // + 128
	VMLS_4S(8, 3, 27)             // -= 26*R
	VMLS_4S(8, 4, 28)             // -= 86*G
	SSHR_4S(8, 8, 8)
	VADD V8.S4, V25.S4, V8.S4    // + 128 = overlayCb

	// overlayCr = (112*R - 102*G - 10*B + 128) >> 8 + 128
	VMUL_4S(9, 3, 29)             // 112*R
	VADD V9.S4, V25.S4, V9.S4    // + 128
	VMLS_4S(9, 4, 30)             // -= 102*G
	VMLS_4S(9, 5, 31)             // -= 10*B
	SSHR_4S(9, 9, 8)
	VADD V9.S4, V25.S4, V9.S4    // + 128 = overlayCr

	// inv = 256 - a256
	VSUB V6.S4, V26.S4, V10.S4

	// Load existing Cb (4 bytes), widen to uint32 via USHLL chain
	MOVWU (R0), R5
	VMOV R5, V11.S[0]            // 4 bytes in lower 32 bits
	USHLL_8H(11, 11)             // bytes → uint16
	USHLL_4S(11, 11)             // uint16 → uint32

	// Load existing Cr (4 bytes), widen
	MOVWU (R1), R5
	VMOV R5, V16.S[0]
	USHLL_8H(16, 16)
	USHLL_4S(16, 16)

	// Blend Cb: (existing*inv + overlayCb*a256 + 128) >> 8
	VMUL_4S(11, 11, 10)
	VMLA_4S(11, 8, 6)
	VADD V11.S4, V25.S4, V11.S4
	SSHR_4S(11, 11, 8)             // signed shift to preserve negative
	VSMAX_4S(11, 11, 22)           // clamp negatives to 0

	// Blend Cr
	VMUL_4S(16, 16, 10)
	VMLA_4S(16, 9, 6)
	VADD V16.S4, V25.S4, V16.S4
	SSHR_4S(16, 16, 8)             // signed shift to preserve negative
	VSMAX_4S(16, 16, 22)           // clamp negatives to 0

	// Saturating narrow: uint32 → uint16 → uint8
	UQXTN_4H(11, 11)
	UQXTN_8B(11, 11)
	UQXTN_4H(16, 16)
	UQXTN_8B(16, 16)

	// Store 4 Cb bytes
	VMOV V11.S[0], R5
	MOVW R5, (R0)

	// Store 4 Cr bytes
	VMOV V16.S[0], R5
	MOVW R5, (R1)

chroma_skip4:
	ADD  $32, R2
	ADD  $4, R0
	ADD  $4, R1
	SUB  $4, R3, R3
	CMP  $4, R3
	BGE  chroma_neon4

chroma_scalar:
	CMP  $0, R3
	BLE  chroma_done

chroma_scalar_loop:
	MOVBU 3(R2), R5
	LSR   $7, R5, R6
	ADD   R6, R5, R5
	MUL   R4, R5, R5
	LSR   $8, R5, R5
	CBZ   R5, chroma_scalar_skip

	MOVBU (R2), R6
	MOVBU 1(R2), R7
	MOVBU 2(R2), R8

	// overlayCb
	MOVD  $112, R9
	MUL   R9, R8, R9
	ADD   $128, R9, R9
	MOVD  $26, R10
	MUL   R10, R6, R10
	SUB   R10, R9, R9
	MOVD  $86, R10
	MUL   R10, R7, R10
	SUB   R10, R9, R9
	ASR   $8, R9, R9
	ADD   $128, R9, R9

	// overlayCr
	MOVD  $112, R10
	MUL   R10, R6, R10
	ADD   $128, R10, R10
	MOVD  $102, R11
	MUL   R11, R7, R11
	SUB   R11, R10, R10
	MOVD  $10, R11
	MUL   R11, R8, R11
	SUB   R11, R10, R10
	ASR   $8, R10, R10
	ADD   $128, R10, R10

	// inv, blend
	MOVD  $256, R11
	SUB   R5, R11, R11

	MOVBU (R0), R12
	MUL   R11, R12, R12
	MUL   R5, R9, R9
	ADD   R9, R12, R12
	ADD   $128, R12, R12
	ASR   $8, R12, R12
	CMP   $0, R12
	CSEL  LT, ZR, R12, R12
	MOVD  $255, R13
	CMP   R13, R12
	CSEL  HI, R13, R12, R12
	MOVB  R12, (R0)

	MOVBU (R1), R12
	MUL   R11, R12, R12
	MUL   R5, R10, R10
	ADD   R10, R12, R12
	ADD   $128, R12, R12
	ASR   $8, R12, R12
	CMP   $0, R12
	CSEL  LT, ZR, R12, R12
	CMP   R13, R12
	CSEL  HI, R13, R12, R12
	MOVB  R12, (R1)

chroma_scalar_skip:
	ADD   $8, R2
	ADD   $1, R0
	ADD   $1, R1
	SUB   $1, R3, R3
	CBNZ  R3, chroma_scalar_loop

chroma_done:
	RET
