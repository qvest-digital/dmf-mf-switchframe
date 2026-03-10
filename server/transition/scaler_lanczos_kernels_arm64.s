#include "textflag.h"

// ARM64 NEON Lanczos-3 scaler kernels.
//
// Optimizations vs naive Go:
// - Horizontal: NEON 4-tap FMLA with direct memory→NEON loads (no GPR crossing)
// - Vertical: 16/8/4-wide column processing, LD1R weight broadcast, XTN narrowing store

// --- Float32 NEON macros ---
// FMLA Vd.4S, Vn.4S, Vm.4S — fused multiply-add: Vd += Vn * Vm
#define FMLA_4S(Vd, Vn, Vm) WORD $(0x4E20CC00 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// DUP Vd.4S, Vn.S[0] — broadcast element 0 to all 4 lanes
#define VDUP_S0_4S(Vd, Vn) WORD $(0x4E040400 | ((Vn)<<5) | (Vd))

// FCVTNS Vd.4S, Vn.4S — float32 to signed int32 (round to nearest)
#define FCVTNS_4S(Vd, Vn) WORD $(0x4E21A800 | ((Vn)<<5) | (Vd))

// FADD Vd.4S, Vn.4S, Vm.4S
#define FADD_4S(Vd, Vn, Vm) WORD $(0x4E20D400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// FMAX Vd.4S, Vn.4S, Vm.4S
#define FMAX_4S(Vd, Vn, Vm) WORD $(0x4E20F400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// FMIN Vd.4S, Vn.4S, Vm.4S
#define FMIN_4S(Vd, Vn, Vm) WORD $(0x4EA0F400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// FADDP Vd.4S, Vn.4S, Vm.4S — pairwise add
#define FADDP_4S(Vd, Vn, Vm) WORD $(0x6E20D400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// FADDP Sd, Vn.2S — scalar pairwise add (reduce 2 floats to 1)
#define FADDP_SCALAR(Vd, Vn) WORD $(0x7E30D800 | ((Vn)<<5) | (Vd))

// UXTL Vd.8H, Vn.8B — zero-extend 8 bytes → 8 halfwords (USHLL #0)
#define UXTL_8H(Vd, Vn) WORD $(0x2F08A400 | ((Vn)<<5) | (Vd))

// UXTL Vd.4S, Vn.4H — zero-extend lower 4 halfwords → 4 words (USHLL #0, size=16-bit)
#define UXTL_4S(Vd, Vn) WORD $(0x2F10A400 | ((Vn)<<5) | (Vd))

// UCVTF Vd.4S, Vn.4S — unsigned int32 → float32
#define UCVTF_4S(Vd, Vn) WORD $(0x6E21D800 | ((Vn)<<5) | (Vd))

// XTN Vd.4H, Vn.4S — narrow int32 → int16 (lower half of Vd)
#define XTN_4H(Vd, Vn) WORD $(0x0E612800 | ((Vn)<<5) | (Vd))

// XTN2 Vd.8H, Vn.4S — narrow int32 → int16 (upper half of Vd)
#define XTN2_8H(Vd, Vn) WORD $(0x4E612800 | ((Vn)<<5) | (Vd))

// XTN Vd.8B, Vn.8H — narrow int16 → int8 (lower half of Vd)
#define XTN_8B(Vd, Vn) WORD $(0x0E212800 | ((Vn)<<5) | (Vd))

// XTN2 Vd.16B, Vn.8H — narrow int16 → int8 (upper half of Vd)
#define XTN2_16B(Vd, Vn) WORD $(0x4E212800 | ((Vn)<<5) | (Vd))

// LD1R {Vt.4S}, [Xn] — load one float32 and replicate to all 4 lanes
#define LD1R_4S(Vt, Xn) WORD $(0x4D40C800 | ((Xn)<<5) | (Vt))


// ============================================================================
// func lanczosHorizRow(dst []float32, src []byte, offsets []int32,
//                      weights []float32, maxTaps int)
// ============================================================================
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

	// Load 4 source bytes directly into NEON (avoids GPR→NEON crossing)
	ADD	R11, R12, R15            // &src[off + t]
	FMOVS	(R15), F2                // V2.S[0] = 4 packed bytes (upper bits zeroed)

	// Widen bytes → halfwords → words → float32
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

	// Reduce V0 to scalar, then add this tap
	B	lh_scalar_tail_start

lh_neon_reduce:
	// Horizontal sum of V0.4S → scalar S0
	FADDP_4S(0, 0, 0)
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
// NEON vertical pass: 16/8/4-wide float32 FMLA, LD1R weight broadcast,
// XTN narrowing store.
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
	SUB	$15, R1, R21             // dstW - 15 (for 16-wide loop)
	SUB	$7, R1, R20              // dstW - 7 (for 8-wide loop)
	SUB	$3, R1, R11              // dstW - 3 (for 4-wide loop)

	// --- 16-wide NEON loop: process 16 columns per iteration ---
lv_neon16:
	CMP	R21, R10
	BGE	lv_neon8                 // fewer than 16 columns left

	VEOR	V0.B16, V0.B16, V0.B16  // accum cols 0-3
	VEOR	V4.B16, V4.B16, V4.B16  // accum cols 4-7
	VEOR	V20.B16, V20.B16, V20.B16 // accum cols 8-11
	VEOR	V21.B16, V21.B16, V21.B16 // accum cols 12-15
	MOVD	$0, R12

lv_neon16_tap:
	CMP	R6, R12
	BGE	lv_neon16_cvt

	// Load weight[t] and broadcast to all 4 lanes via LD1R
	LSL	$2, R12, R9
	ADD	R5, R9, R9
	LD1R_4S(1, 9)                    // V1.4S = {w, w, w, w}

	// Compute row base: temp + t * stride_bytes + col * 4
	MUL	R12, R7, R13
	ADD	R2, R13, R14
	LSL	$2, R10, R9
	ADD	R14, R9, R14

	// Load 16 float32 from temp row (4 × 4-wide loads)
	VLD1	(R14), [V2.S4]           // cols 0-3
	ADD	$16, R14, R15
	VLD1	(R15), [V3.S4]           // cols 4-7
	ADD	$32, R14, R15
	VLD1	(R15), [V22.S4]          // cols 8-11
	ADD	$48, R14, R15
	VLD1	(R15), [V23.S4]          // cols 12-15

	FMLA_4S(0, 2, 1)                // V0 += V2 * V1 (cols 0-3)
	FMLA_4S(4, 3, 1)                // V4 += V3 * V1 (cols 4-7)
	FMLA_4S(20, 22, 1)              // V20 += V22 * V1 (cols 8-11)
	FMLA_4S(21, 23, 1)              // V21 += V23 * V1 (cols 12-15)

	ADD	$1, R12
	B	lv_neon16_tap

lv_neon16_cvt:
	// Clamp and convert all 4 accumulators
	FMAX_4S(0, 0, 16)
	FMIN_4S(0, 0, 17)
	FCVTNS_4S(0, 0)

	FMAX_4S(4, 4, 16)
	FMIN_4S(4, 4, 17)
	FCVTNS_4S(4, 4)

	FMAX_4S(20, 20, 16)
	FMIN_4S(20, 20, 17)
	FCVTNS_4S(20, 20)

	FMAX_4S(21, 21, 16)
	FMIN_4S(21, 21, 17)
	FCVTNS_4S(21, 21)

	// Narrow int32 → int16 → uint8 and store 16 bytes
	XTN_4H(5, 0)                    // V5.4H = narrow V0.4S (cols 0-3)
	XTN2_8H(5, 4)                   // V5.8H upper = narrow V4.4S (cols 4-7)
	XTN_4H(6, 20)                   // V6.4H = narrow V20.4S (cols 8-11)
	XTN2_8H(6, 21)                  // V6.8H upper = narrow V21.4S (cols 12-15)
	XTN_8B(7, 5)                    // V7 lower 8B = narrow V5.8H (cols 0-7)
	XTN2_16B(7, 6)                  // V7 upper 8B = narrow V6.8H (cols 8-15)

	// Store 16 bytes via two 8-byte writes
	ADD	R0, R10, R14
	FMOVD	F7, R13
	MOVD	R13, (R14)
	VMOV	V7.D[1], R13
	MOVD	R13, 8(R14)

	ADD	$16, R10
	B	lv_neon16

	// --- 8-wide NEON loop: process 8 columns per iteration ---
lv_neon8:
	CMP	R20, R10
	BGE	lv_neon                  // fewer than 8 columns left, fall to 4-wide

	VEOR	V0.B16, V0.B16, V0.B16  // accum cols 0-3
	VEOR	V4.B16, V4.B16, V4.B16  // accum cols 4-7
	MOVD	$0, R12

lv_neon8_tap:
	CMP	R6, R12
	BGE	lv_neon8_cvt

	// Load weight[t] and broadcast via LD1R
	LSL	$2, R12, R9
	ADD	R5, R9, R9
	LD1R_4S(1, 9)                    // V1.4S = {w, w, w, w}

	// Compute row base: temp + t * stride_bytes + col * 4
	MUL	R12, R7, R13
	ADD	R2, R13, R14
	LSL	$2, R10, R9
	ADD	R14, R9, R14

	// Load 8 float32 from temp row (2 × 4-wide loads)
	VLD1	(R14), [V2.S4]
	ADD	$16, R14, R15
	VLD1	(R15), [V3.S4]

	FMLA_4S(0, 2, 1)            // V0 += V2 * V1 (cols 0-3)
	FMLA_4S(4, 3, 1)            // V4 += V3 * V1 (cols 4-7)

	ADD	$1, R12
	B	lv_neon8_tap

lv_neon8_cvt:
	// Clamp and convert both accumulators
	FMAX_4S(0, 0, 16)
	FMIN_4S(0, 0, 17)
	FCVTNS_4S(0, 0)

	FMAX_4S(4, 4, 16)
	FMIN_4S(4, 4, 17)
	FCVTNS_4S(4, 4)

	// Narrow int32 → int16 → uint8 and store 8 bytes
	XTN_4H(5, 0)                    // V5.4H = narrow V0.4S
	XTN2_8H(5, 4)                   // V5.8H = both halves
	XTN_8B(6, 5)                    // V6 lower 8B = 8 bytes
	ADD	R0, R10, R14
	FMOVD	F6, R13
	MOVD	R13, (R14)

	ADD	$8, R10
	B	lv_neon8

	// --- 4-wide NEON loop: process remaining 4-7 columns ---
lv_neon:
	CMP	R11, R10
	BGE	lv_scalar_setup

	VEOR	V0.B16, V0.B16, V0.B16
	MOVD	$0, R12

lv_neon_tap:
	CMP	R6, R12
	BGE	lv_neon_cvt

	// Load weight[t] and broadcast via LD1R
	LSL	$2, R12, R9
	ADD	R5, R9, R9
	LD1R_4S(1, 9)                    // V1.4S = {w, w, w, w}

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

	// Narrow int32 → int16 → uint8 and store 4 bytes
	XTN_4H(5, 0)                    // V5.4H = narrow V0.4S
	XTN_8B(6, 5)                    // V6 lower 4B valid
	ADD	R0, R10, R14
	FMOVS	F6, R13
	MOVW	R13, (R14)

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
