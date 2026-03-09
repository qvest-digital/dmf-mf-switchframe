#include "textflag.h"

// ARM64 NEON kernel for chroma→luma mask row expansion.
// Each input byte is duplicated to produce two output bytes.
//
// func expandChromaMaskRow(dst *byte, src *byte, chromaWidth int)

// --- NEON macros ---
// ZIP1 Vd.16B, Vn.16B, Vm.16B — interleave lower halves
#define ZIP1_16B(Vd, Vn, Vm) WORD $(0x4E003800 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
// ZIP2 Vd.16B, Vn.16B, Vm.16B — interleave upper halves
#define ZIP2_16B(Vd, Vn, Vm) WORD $(0x4E007800 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// Registers:
//   R0 = dst, R1 = src, R2 = chromaWidth
TEXT ·expandChromaMaskRow(SB), NOSPLIT, $0-24
	MOVD dst+0(FP), R0
	MOVD src+8(FP), R1
	MOVD chromaWidth+16(FP), R2

	CMP  $0, R2
	BLE  expand_done

	CMP  $16, R2
	BLT  expand_scalar

expand_neon16:
	// Load 16 source bytes
	VLD1 (R1), [V0.B16]

	// ZIP1: interleave lower 8 bytes with self → 16 output bytes
	// [a0,a1,...,a7] × [a0,a1,...,a7] → [a0,a0,a1,a1,...,a7,a7]
	ZIP1_16B(1, 0, 0)

	// ZIP2: interleave upper 8 bytes with self → 16 output bytes
	// [a8,a9,...,a15] × [a8,a9,...,a15] → [a8,a8,a9,a9,...,a15,a15]
	ZIP2_16B(2, 0, 0)

	// Store 32 output bytes
	VST1 [V1.B16, V2.B16], (R0)

	ADD  $16, R1
	ADD  $32, R0
	SUB  $16, R2, R2
	CMP  $16, R2
	BGE  expand_neon16

expand_scalar:
	CMP  $0, R2
	BLE  expand_done

expand_scalar_loop:
	MOVBU (R1), R3
	MOVB  R3, (R0)
	MOVB  R3, 1(R0)
	ADD   $1, R1
	ADD   $2, R0
	SUB   $1, R2, R2
	CBNZ  R2, expand_scalar_loop

expand_done:
	RET
