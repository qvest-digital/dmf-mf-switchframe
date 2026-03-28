#include "textflag.h"

// ARM64 NEON kernel for horizontal 2:1 alpha downsample (4:2:2).
//
// downsampleAlpha10Row averages pairs of adjacent uint8 values:
//   dst[i] = (src[2*i] + src[2*i+1] + 1) >> 1
//
// NEON approach: LD2 deinterleaves into even/odd, URHADD computes rounding average.
// 16 output samples per iteration (reads 32 source bytes).

// URHADD Vd.16B, Vn.16B, Vm.16B — unsigned rounding halving add
// Encoding: 0x6E201400 | Rm<<16 | Rn<<5 | Rd
#define URHADD_16B(Vd, Vn, Vm) WORD $(0x6E201400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// ============================================================================
// func downsampleAlpha10Row(dst, src *byte, count int)
// ============================================================================
// dst[i] = (src[2*i] + src[2*i+1] + 1) >> 1
// count = number of output samples
TEXT ·downsampleAlpha10Row(SB), NOSPLIT, $0-24
	MOVD dst+0(FP), R0         // dst *byte
	MOVD src+8(FP), R1         // src *byte
	MOVD count+16(FP), R2      // count (number of output samples)

	CMP  $0, R2
	BLE  done

	// Process 16 output samples per iteration
	CMP  $16, R2
	BLT  tail

loop16:
	// LD2 deinterleaves: V0 = even bytes (src[0],src[2],...), V1 = odd bytes (src[1],src[3],...)
	// Read 32 bytes from src with post-increment
	// LD2 {V0.16B, V1.16B}, [X1], #32
	// Encoding: Q=1, L=1, Rm=11111 (imm), opcode=1000, size=00
	WORD $0x4CDF8020           // LD2 {V0.16B, V1.16B}, [X1], #32

	// URHADD: dst = (even + odd + 1) >> 1
	URHADD_16B(2, 0, 1)       // V2 = avg(V0, V1)

	// Store 16 output bytes
	VST1.P [V2.B16], 16(R0)

	SUB  $16, R2, R2
	CMP  $16, R2
	BGE  loop16

tail:
	CBZ  R2, done

tail_loop:
	// Load 2 source bytes
	MOVBU (R1), R3             // src[2*i]
	MOVBU 1(R1), R4            // src[2*i+1]
	ADD  $2, R1

	// (a + b + 1) >> 1
	ADD  R3, R4, R3
	ADD  $1, R3, R3
	LSR  $1, R3, R3

	MOVB R3, (R0)
	ADD  $1, R0

	SUB  $1, R2, R2
	CBNZ R2, tail_loop

done:
	RET
