#include "textflag.h"

// ARM64 NEON kernel for triple dot product (cross-correlation).
//
// func crossCorrFloat32(a *float32, b *float32, n int) (corr, normA, normB float32)
//
// Computes:
//   corr  = sum(a[i] * b[i])
//   normA = sum(a[i] * a[i])
//   normB = sum(b[i] * b[i])

// --- NEON macros ---
// FMLA Vd.4S, Vn.4S, Vm.4S — Vd += Vn * Vm
#define FMLA_4S(Vd, Vn, Vm) WORD $(0x4E20CC00 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
// FADD Vd.4S, Vn.4S, Vm.4S — element-wise float add
#define FADD_4S(Vd, Vn, Vm) WORD $(0x4E20D400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
// FADDP Vd.4S, Vn.4S, Vm.4S — pairwise add (for horizontal reduction)
#define FADDP_4S(Vd, Vn, Vm) WORD $(0x6E20D400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// Registers:
//   R0 = a, R1 = b, R2 = n
//   V0-V1 = corr accumulators (8-wide)
//   V2-V3 = normA accumulators (8-wide)
//   V4-V5 = normB accumulators (8-wide)
TEXT ·crossCorrFloat32(SB), NOSPLIT, $0-36
	MOVD a+0(FP), R0
	MOVD b+8(FP), R1
	MOVD n+16(FP), R2

	// Zero accumulators
	VEOR V0.B16, V0.B16, V0.B16   // corr acc 0
	VEOR V1.B16, V1.B16, V1.B16   // corr acc 1
	VEOR V2.B16, V2.B16, V2.B16   // normA acc 0
	VEOR V3.B16, V3.B16, V3.B16   // normA acc 1
	VEOR V4.B16, V4.B16, V4.B16   // normB acc 0
	VEOR V5.B16, V5.B16, V5.B16   // normB acc 1

	CMP  $0, R2
	BLE  xcorr_reduce

	// Process 8 elements per iteration (2 NEON registers)
	CMP  $8, R2
	BLT  xcorr_4

xcorr_8:
	// Load 8 floats from a and b
	VLD1 (R0), [V16.S4, V17.S4]
	VLD1 (R1), [V18.S4, V19.S4]

	// corr += a[i] * b[i]
	FMLA_4S(0, 16, 18)
	FMLA_4S(1, 17, 19)

	// normA += a[i] * a[i]
	FMLA_4S(2, 16, 16)
	FMLA_4S(3, 17, 17)

	// normB += b[i] * b[i]
	FMLA_4S(4, 18, 18)
	FMLA_4S(5, 19, 19)

	ADD  $32, R0
	ADD  $32, R1
	SUB  $8, R2, R2
	CMP  $8, R2
	BGE  xcorr_8

xcorr_4:
	CMP  $4, R2
	BLT  xcorr_scalar

	// Load 4 floats
	VLD1 (R0), [V16.S4]
	VLD1 (R1), [V18.S4]

	FMLA_4S(0, 16, 18)    // corr
	FMLA_4S(2, 16, 16)    // normA
	FMLA_4S(4, 18, 18)    // normB

	ADD  $16, R0
	ADD  $16, R1
	SUB  $4, R2, R2

xcorr_scalar:
	CMP  $0, R2
	BLE  xcorr_reduce

xcorr_scalar_loop:
	FMOVS (R0), F16
	FMOVS (R1), F18

	// corr += a * b
	FMULS F16, F18, F20
	FADDS F20, F0, F0

	// normA += a * a
	FMULS F16, F16, F20
	FADDS F20, F2, F2

	// normB += b * b
	FMULS F18, F18, F20
	FADDS F20, F4, F4

	ADD  $4, R0
	ADD  $4, R1
	SUB  $1, R2, R2
	CBNZ R2, xcorr_scalar_loop

xcorr_reduce:
	// Merge the two 4-wide accumulators
	FADD_4S(0, 0, 1)              // corr: V0 += V1
	FADD_4S(2, 2, 3)              // normA: V2 += V3
	FADD_4S(4, 4, 5)              // normB: V4 += V5

	// Horizontal sum: pairwise add then extract
	FADDP_4S(0, 0, 0)             // V0 = [s0+s1, s2+s3, s0+s1, s2+s3]
	FADDP_4S(2, 2, 2)             // V2 = [s0+s1, s2+s3, s0+s1, s2+s3]
	FADDP_4S(4, 4, 4)             // V4 = [s0+s1, s2+s3, s0+s1, s2+s3]

	// One more pairwise to get final scalar in lane 0
	FADDP_4S(0, 0, 0)             // V0.S[0] = total corr
	FADDP_4S(2, 2, 2)             // V2.S[0] = total normA
	FADDP_4S(4, 4, 4)             // V4.S[0] = total normB

	// Store results
	FMOVS F0, corr+24(FP)
	FMOVS F2, normA+28(FP)
	FMOVS F4, normB+32(FP)

	RET
