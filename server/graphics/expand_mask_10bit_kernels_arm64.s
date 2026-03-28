#include "textflag.h"

// ARM64 NEON kernel for 4:2:2 chroma→luma mask row expansion.
// Each input byte is duplicated to produce two output bytes.
// Identical to the 8-bit expand_mask kernel — re-used for 10-bit
// since the mask itself remains 8-bit.
//
// func expandChromaMask10Row(dst *byte, src *byte, count int)

// ZIP1 Vd.16B, Vn.16B, Vm.16B — interleave lower halves
#define ZIP1_16B(Vd, Vn, Vm) WORD $(0x4E003800 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
// ZIP2 Vd.16B, Vn.16B, Vm.16B — interleave upper halves
#define ZIP2_16B(Vd, Vn, Vm) WORD $(0x4E007800 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

TEXT ·expandChromaMask10Row(SB), NOSPLIT, $0-24
	MOVD dst+0(FP), R0
	MOVD src+8(FP), R1
	MOVD count+16(FP), R2

	CMP  $0, R2
	BLE  expand10_done

	CMP  $16, R2
	BLT  expand10_scalar

expand10_neon16:
	// Load 16 source bytes
	VLD1 (R1), [V0.B16]

	// ZIP1: interleave lower 8 bytes with self
	ZIP1_16B(1, 0, 0)
	// ZIP2: interleave upper 8 bytes with self
	ZIP2_16B(2, 0, 0)

	// Store 32 output bytes
	VST1 [V1.B16, V2.B16], (R0)

	ADD  $16, R1
	ADD  $32, R0
	SUB  $16, R2, R2
	CMP  $16, R2
	BGE  expand10_neon16

expand10_scalar:
	CMP  $0, R2
	BLE  expand10_done

expand10_scalar_loop:
	MOVBU (R1), R3
	MOVB  R3, (R0)
	MOVB  R3, 1(R0)
	ADD   $1, R1
	ADD   $2, R0
	SUB   $1, R2, R2
	CBNZ  R2, expand10_scalar_loop

expand10_done:
	RET
