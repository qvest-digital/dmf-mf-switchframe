#include "textflag.h"

// ARM64 NEON downsample alpha kernel for 2x2 box average.
//
// Uses URHADD (Unsigned Rounding Halving Add) for (a+b+1)/2 rounding average,
// and UZP1/UZP2 for deinterleaving even/odd bytes.
//
// Two rounds of URHADD may differ from (a+b+c+d+2)/4 by at most +/-1,
// which is acceptable for alpha downsampling.

// --- NEON instruction macros not available in Go assembler ---

// URHADD Vd.16B, Vn.16B, Vm.16B — unsigned rounding halving add
#define URHADD_16B(Vd, Vn, Vm) WORD $(0x6E201400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// UZP1 Vd.16B, Vn.16B, Vm.16B — unzip even-indexed elements
// Encoding: 0 Q 001110 size 0 Rm 0001 10 Rn Rd (opcode=0001 for UZP1)
#define UZP1_16B(Vd, Vn, Vm) WORD $(0x4E001800 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// UZP2 Vd.16B, Vn.16B, Vm.16B — unzip odd-indexed elements
#define UZP2_16B(Vd, Vn, Vm) WORD $(0x4E005800 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// ============================================================================
// func downsampleAlpha2x2(dst, row0, row1 *byte, pairs int)
// ============================================================================
// dst[i] = avg2x2(row0[2*i], row0[2*i+1], row1[2*i], row1[2*i+1])
//
// Processing 16 output pixels per iteration:
//   Load 32 input bytes from each row (two VLD1.P 16-byte loads).
//   Vertical average via URHADD, deinterleave via UZP1/UZP2,
//   horizontal average via URHADD, store 16 result bytes.
TEXT ·downsampleAlpha2x2(SB), NOSPLIT, $0-32
	MOVD dst+0(FP), R0
	MOVD row0+8(FP), R1
	MOVD row1+16(FP), R2
	MOVD pairs+24(FP), R3

	CMP  $0, R3
	BLE  ds_done

	CMP  $16, R3
	BLT  ds_tail8

ds_loop16:
	// Load 32 bytes from row0 (16 pairs worth of source)
	VLD1.P 16(R1), [V0.B16]       // row0[0..15]
	VLD1.P 16(R1), [V1.B16]       // row0[16..31]

	// Load 32 bytes from row1
	VLD1.P 16(R2), [V2.B16]       // row1[0..15]
	VLD1.P 16(R2), [V3.B16]       // row1[16..31]

	// Vertical average: (row0 + row1 + 1) / 2
	URHADD_16B(0, 0, 2)           // V0 = urhadd(row0[0..15], row1[0..15])
	URHADD_16B(1, 1, 3)           // V1 = urhadd(row0[16..31], row1[16..31])

	// Deinterleave even/odd bytes across V0:V1 concatenation
	// UZP1: even bytes [V0[0],V0[2],...,V0[14],V1[0],V1[2],...,V1[14]]
	// UZP2: odd bytes  [V0[1],V0[3],...,V0[15],V1[1],V1[3],...,V1[15]]
	UZP1_16B(4, 0, 1)             // V4 = even bytes (16 values)
	UZP2_16B(5, 0, 1)             // V5 = odd bytes (16 values)

	// Horizontal average: (even + odd + 1) / 2
	URHADD_16B(4, 4, 5)           // V4 = 16 result bytes

	VST1.P [V4.B16], 16(R0)

	SUB  $16, R3, R3
	CMP  $16, R3
	BGE  ds_loop16

ds_tail8:
	// Process 8 pairs at a time using a single 16-byte load from each row
	CMP  $8, R3
	BLT  ds_tail

	VLD1.P 16(R1), [V0.B16]       // row0: 16 bytes (8 pairs)
	VLD1.P 16(R2), [V1.B16]       // row1: 16 bytes

	// Vertical average
	URHADD_16B(0, 0, 1)           // V0 = vavg (16 bytes)

	// Deinterleave: use UZP with same register to get even/odd
	// UZP1 V2.16B, V0.16B, V0.16B → even bytes in lower 8 (repeated in upper)
	// UZP2 V3.16B, V0.16B, V0.16B → odd bytes in lower 8 (repeated in upper)
	UZP1_16B(2, 0, 0)             // V2 = even bytes
	UZP2_16B(3, 0, 0)             // V3 = odd bytes

	// Horizontal average
	URHADD_16B(2, 2, 3)           // V2 = results (lower 8 bytes valid)

	// Store lower 8 bytes using WORD-encoded ST1 {Vt.8B}, [Xn], #8
	// ST1 single struct, 8B, post-index by 8:
	// 0 0 001100 1 0 0 11111 0111 00 Rn Rt  (post-index, immediate = element_size * #elements = 8)
	// For Rn=R0(=0), Rt=V2(=2):
	// 0x0C9F7000 | (Rn << 5) | Rt
	WORD $(0x0C9F7000 | (0 << 5) | 2)   // ST1 {V2.8B}, [R0], #8

	SUB  $8, R3, R3

ds_tail:
	CBZ  R3, ds_done

ds_tail_loop:
	MOVBU (R1), R4                 // row0[2*i]
	MOVBU 1(R1), R5                // row0[2*i+1]
	ADD   R5, R4, R4               // sum row0
	MOVBU (R2), R5                 // row1[2*i]
	ADD   R5, R4, R4
	MOVBU 1(R2), R5                // row1[2*i+1]
	ADD   R5, R4, R4               // sum of 4 pixels
	ADD   $2, R4, R4               // + 2 (rounding)
	LSR   $2, R4, R4               // / 4
	MOVB  R4, (R0)
	ADD   $2, R1
	ADD   $2, R2
	ADD   $1, R0
	SUB   $1, R3, R3
	CBNZ  R3, ds_tail_loop

ds_done:
	RET
