#include "textflag.h"

// ARM64 NEON Lanczos-3 scaler kernels.
//
// Both horizontal and vertical passes use NEON:
// - Horizontal: load contiguous source taps, widen to float32, FMLA, horizontal reduce
// - Vertical: 4-wide column processing with FMLA

// --- Float32 NEON macros ---
// FMLA Vd.4S, Vn.4S, Vm.4S — fused multiply-add: Vd += Vn * Vm
#define FMLA_4S(Vd, Vn, Vm) WORD $(0x4E20CC00 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// DUP Vd.4S, Vn.S[0] — broadcast element 0 to all 4 lanes
#define VDUP_S0_4S(Vd, Vn) WORD $(0x4E040400 | ((Vn)<<5) | (Vd))

// FCVTZS Vd.4S, Vn.4S — float32 to signed int32 (truncate)
#define FCVTZS_4S(Vd, Vn) WORD $(0x4EA1B800 | ((Vn)<<5) | (Vd))

// FCVTNS Vd.4S, Vn.4S — float32 to signed int32 (round to nearest)
#define FCVTNS_4S(Vd, Vn) WORD $(0x4E21A800 | ((Vn)<<5) | (Vd))

// FADD Vd.4S, Vn.4S, Vm.4S
#define FADD_4S(Vd, Vn, Vm) WORD $(0x4E20D400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// FMAX Vd.4S, Vn.4S, Vm.4S
#define FMAX_4S(Vd, Vn, Vm) WORD $(0x4E20F400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// FMIN Vd.4S, Vn.4S, Vm.4S
#define FMIN_4S(Vd, Vn, Vm) WORD $(0x4EA0F400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// FMUL Vd.4S, Vn.4S, Vm.4S
#define FMUL_4S(Vd, Vn, Vm) WORD $(0x6E20DC00 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// FADDP Vd.4S, Vn.4S, Vm.4S — pairwise add
#define FADDP_4S(Vd, Vn, Vm) WORD $(0x6E20D400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// FADDP Sd, Vn.2S — scalar pairwise add (reduce 2 floats to 1)
#define FADDP_SCALAR(Vd, Vn) WORD $(0x7E30D800 | ((Vn)<<5) | (Vd))

// UXTL Vd.8H, Vn.8B — zero-extend 8 bytes → 8 halfwords (USHLL #0)
#define UXTL_8H(Vd, Vn) WORD $(0x2F08A400 | ((Vn)<<5) | (Vd))

// UXTL Vd.4S, Vn.4H — zero-extend lower 4 halfwords → 4 words (USHLL #0, size=16-bit)
#define UXTL_4S(Vd, Vn) WORD $(0x2F10A400 | ((Vn)<<5) | (Vd))

// UXTL2 Vd.4S, Vn.8H — zero-extend upper 4 halfwords → 4 words
#define UXTL2_4S(Vd, Vn) WORD $(0x6F10A400 | ((Vn)<<5) | (Vd))

// UCVTF Vd.4S, Vn.4S — unsigned int32 → float32
#define UCVTF_4S(Vd, Vn) WORD $(0x6E21D800 | ((Vn)<<5) | (Vd))


// ============================================================================
// func lanczosHorizRow(dst []float32, src []byte, offsets []int32,
//                      weights []float32, maxTaps int)
// ============================================================================
// NEON horizontal pass: for each destination pixel, load contiguous source
// taps as bytes, widen to float32, multiply by weights, horizontal sum.
// Processes taps in groups of 4 using FMLA, then reduces to scalar.
//
// For maxTaps <= 8 (typical upscale): one batch of 8 bytes → 2×4 float32
// For maxTaps > 8 (downscale): multiple batches of 4
TEXT ·lanczosHorizRow(SB), NOSPLIT, $0-104
	MOVD	dst_base+0(FP), R0       // dst ptr
	MOVD	dst_len+8(FP), R1        // dstW
	MOVD	src_base+24(FP), R2      // src ptr
	MOVD	src_len+32(FP), R3       // srcLen
	MOVD	offsets_base+48(FP), R4  // offsets ptr
	MOVD	weights_base+72(FP), R5  // weights ptr
	MOVD	maxTaps+96(FP), R6       // maxTaps

	CBZ	R1, lh_done
	MOVD	$0, R7                   // d = 0

lh_pixel:
	// R8 = offsets[d]
	LSL	$2, R7, R9
	ADD	R4, R9, R9
	MOVW	(R9), R8

	// R10 = &weights[d * maxTaps]
	MUL	R7, R6, R9
	LSL	$2, R9, R9
	ADD	R5, R9, R10

	// R11 = src + offset (source base for this pixel's taps)
	ADD	R2, R8, R11

	// R12 = offset (as 64-bit, for end-of-buffer check)
	// Safely load: ensure offset + maxTaps <= srcLen
	ADD	R8, R6, R13              // offset + maxTaps
	CMP	R3, R13
	BGT	lh_scalar_pixel          // if taps exceed src bounds, use scalar

	// Check offset >= 0
	TBNZ	$63, R8, lh_scalar_pixel

	// --- NEON path: process 4 taps at a time ---
	VEOR	V0.B16, V0.B16, V0.B16  // accumulator V0 = {0,0,0,0}

	MOVD	$0, R12                  // t = 0
	SUB	$3, R6, R14              // maxTaps - 3

lh_neon_4tap:
	CMP	R14, R12
	BGE	lh_neon_tail

	// Load 4 source bytes at src[off + t .. off + t + 3]
	ADD	R11, R12, R15            // &src[off + t]
	MOVW	(R15), R16               // load 4 bytes as uint32

	// Move to NEON, widen bytes → halfwords → words → float32
	FMOVS	R16, F2                  // V2.S[0] = 4 packed bytes
	// Unpack 4 bytes to 4 uint32 via byte→halfword→word
	// V2 has 4 bytes in S[0]: b0,b1,b2,b3,0,0,0,0,...
	UXTL_8H(3, 2)                   // V3.8H = zero-extend 8 bytes of V2
	UXTL_4S(2, 3)                   // V2.4S = zero-extend lower 4 halfwords
	UCVTF_4S(2, 2)                  // V2.4S = float32(pixels)

	// Load 4 weights
	LSL	$2, R12, R15
	ADD	R10, R15, R15
	VLD1	(R15), [V3.S4]           // V3.4S = 4 weights

	// Accumulate: V0 += V2 * V3
	FMLA_4S(0, 2, 3)

	ADD	$4, R12
	B	lh_neon_4tap

lh_neon_tail:
	// Process remaining 0-3 taps with scalar
	CMP	R6, R12
	BGE	lh_neon_reduce

lh_neon_tail_loop:
	// Load one weight
	LSL	$2, R12, R15
	ADD	R10, R15, R15
	FMOVS	(R15), F1                // weight

	// Load one source byte
	ADD	R11, R12, R15
	MOVBU	(R15), R16
	UCVTFWS	R16, F2                  // float32(src[off+t])

	// Scalar accumulate into F0 (which is V0.S[0])
	// But F0 is part of V0 (the NEON accumulator). We need to
	// accumulate into a separate scalar register.
	FMULS	F1, F2, F2               // F2 = weight * pixel
	// Add to V0.S[0]: extract, add, insert
	// Simpler: accumulate in a scalar F4 then add to V0 at the end
	FMOVS	ZR, F3                   // Can't easily add to V0.S[0], so...
	// Actually just use FMADDS with a separate scalar accumulator
	// We'll handle this below after the NEON reduce
	B	lh_scalar_tail_start

lh_neon_reduce:
	// Horizontal sum of V0.4S → scalar S0
	// FADDP {a,b,c,d},{a,b,c,d} → {a+b, c+d, a+b, c+d}
	FADDP_4S(0, 0, 0)
	// FADDP scalar: S0 = V0.S[0] + V0.S[1] = (a+b) + (c+d)
	FADDP_SCALAR(0, 0)

	// Store dst[d] = S0
	LSL	$2, R7, R9
	ADD	R0, R9, R9
	FMOVS	F0, (R9)

	ADD	$1, R7
	CMP	R1, R7
	BLT	lh_pixel
	B	lh_done

lh_scalar_tail_start:
	// We have partial NEON result in V0.4S and remaining scalar taps.
	// Reduce V0 first, then add scalar taps.
	FADDP_4S(0, 0, 0)
	FADDP_SCALAR(0, 0)               // F0 = NEON partial sum

	// Continue scalar taps from R12
	FMADDS	F1, F0, F2, F0           // F0 = F0 + F1*F2 (the tap we already loaded)
	ADD	$1, R12

lh_scalar_tail_cont:
	CMP	R6, R12
	BGE	lh_scalar_tail_done

	LSL	$2, R12, R15
	ADD	R10, R15, R15
	FMOVS	(R15), F1

	ADD	R11, R12, R15
	MOVBU	(R15), R16
	UCVTFWS	R16, F2
	FMADDS	F1, F0, F2, F0

	ADD	$1, R12
	B	lh_scalar_tail_cont

lh_scalar_tail_done:
	// Store
	LSL	$2, R7, R9
	ADD	R0, R9, R9
	FMOVS	F0, (R9)

	ADD	$1, R7
	CMP	R1, R7
	BLT	lh_pixel
	B	lh_done

lh_scalar_pixel:
	// Full scalar fallback for edge pixels where taps exceed src bounds
	FMOVS	ZR, F0
	MOVD	$0, R11                  // t = 0
	ADD	R2, R8, R13              // src + offset

lh_scalar_tap:
	CMP	R6, R11
	BGE	lh_scalar_store

	LSL	$2, R11, R9
	ADD	R10, R9, R9
	FMOVS	(R9), F1

	ADD	R8, R11, R12
	TBNZ	$63, R12, lh_scalar_next
	CMP	R3, R12
	BGE	lh_scalar_next

	MOVBU	(R2)(R12), R13
	UCVTFWS	R13, F2
	FMADDS	F1, F0, F2, F0

lh_scalar_next:
	ADD	$1, R11
	B	lh_scalar_tap

lh_scalar_store:
	LSL	$2, R7, R9
	ADD	R0, R9, R9
	FMOVS	F0, (R9)

	ADD	$1, R7
	CMP	R1, R7
	BLT	lh_pixel

lh_done:
	RET


// ============================================================================
// func lanczosVertRow(dst []byte, temp []float32, tempStride int,
//                     startRow int32, weights []float32, maxTaps int)
// ============================================================================
// NEON vertical pass: 4-wide float32 FMLA accumulation, clamp, convert.
TEXT ·lanczosVertRow(SB), NOSPLIT, $0-96
	MOVD	dst_base+0(FP), R0
	MOVD	dst_len+8(FP), R1
	MOVD	temp_base+24(FP), R2
	MOVD	tempStride+48(FP), R3
	MOVW	startRow+56(FP), R4
	MOVD	weights_base+64(FP), R5
	MOVD	maxTaps+88(FP), R6

	CBZ	R1, lv_done

	// stride_bytes = tempStride * 4
	LSL	$2, R3, R7

	// base = temp + startRow * stride_bytes
	MOVW	R4, R8
	MUL	R7, R8, R8
	ADD	R2, R8, R2

	// Constants for NEON clamp
	VEOR	V16.B16, V16.B16, V16.B16   // V16 = 0.0
	MOVW	$0x437F0000, R9              // 255.0f
	FMOVS	R9, F17
	VDUP_S0_4S(17, 17)                  // V17.4S = {255.0, ...}

	// Scalar 0.5f
	MOVW	$0x3F000000, R9
	FMOVS	R9, F18

	MOVD	$0, R10                  // col = 0
	SUB	$3, R1, R11              // dstW - 3

lv_neon:
	CMP	R11, R10
	BGE	lv_scalar_setup

	VEOR	V0.B16, V0.B16, V0.B16
	MOVD	$0, R12

lv_neon_tap:
	CMP	R6, R12
	BGE	lv_neon_cvt

	// Load weight[t], broadcast
	LSL	$2, R12, R9
	ADD	R5, R9, R9
	FMOVS	(R9), F1
	VDUP_S0_4S(1, 1)

	// Load 4 float32 from temp row
	MUL	R12, R7, R13
	ADD	R2, R13, R14
	LSL	$2, R10, R9
	ADD	R14, R9, R14
	VLD1	(R14), [V2.S4]

	FMLA_4S(0, 2, 1)

	ADD	$1, R12
	B	lv_neon_tap

lv_neon_cvt:
	FMAX_4S(0, 0, 16)
	FMIN_4S(0, 0, 17)
	FCVTNS_4S(0, 0)

	// Store 4 bytes
	ADD	R0, R10, R14
	VMOV	V0.S[0], R13
	MOVB	R13, (R14)
	VMOV	V0.S[1], R13
	MOVB	R13, 1(R14)
	VMOV	V0.S[2], R13
	MOVB	R13, 2(R14)
	VMOV	V0.S[3], R13
	MOVB	R13, 3(R14)

	ADD	$4, R10
	B	lv_neon

lv_scalar_setup:
	CMP	R1, R10
	BGE	lv_done

lv_scalar:
	FMOVS	ZR, F0
	MOVD	$0, R12

lv_scalar_tap:
	CMP	R6, R12
	BGE	lv_scalar_store

	LSL	$2, R12, R9
	ADD	R5, R9, R9
	FMOVS	(R9), F1

	MUL	R12, R7, R13
	ADD	R2, R13, R14
	LSL	$2, R10, R9
	ADD	R14, R9, R14
	FMOVS	(R14), F2

	FMADDS	F1, F0, F2, F0

	ADD	$1, R12
	B	lv_scalar_tap

lv_scalar_store:
	FADDS	F18, F0, F0
	FCVTZSSW	F0, R13
	CMP	$0, R13
	CSEL	LT, ZR, R13, R13
	CMP	$255, R13
	MOVD	$255, R14
	CSEL	GT, R14, R13, R13
	ADD	R0, R10, R9
	MOVB	R13, (R9)
	ADD	$1, R10
	CMP	R1, R10
	BLT	lv_scalar

lv_done:
	RET
