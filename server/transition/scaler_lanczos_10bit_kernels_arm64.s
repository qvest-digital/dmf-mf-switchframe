#include "textflag.h"

// ARM64 NEON kernels for 10-bit Lanczos-3 scaler.
//
// lanczosHoriz10Row: reads uint16 LE source, outputs float32 intermediate
// lanczosVert10Row: reads float32 intermediate, outputs uint16 LE clamped to [0, 1023]

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

// FADDP Sd, Vn.2S — scalar pairwise add
#define FADDP_SCALAR(Vd, Vn) WORD $(0x7E30D800 | ((Vn)<<5) | (Vd))

// UXTL Vd.8H, Vn.8B — zero-extend 8 bytes → 8 halfwords
#define UXTL_8H(Vd, Vn) WORD $(0x2F08A400 | ((Vn)<<5) | (Vd))

// UXTL Vd.4S, Vn.4H — zero-extend lower 4 halfwords → 4 words
#define UXTL_4S(Vd, Vn) WORD $(0x2F10A400 | ((Vn)<<5) | (Vd))

// UCVTF Vd.4S, Vn.4S — unsigned int32 → float32
#define UCVTF_4S(Vd, Vn) WORD $(0x6E21D800 | ((Vn)<<5) | (Vd))

// XTN Vd.4H, Vn.4S — narrow int32 → int16
#define XTN_4H(Vd, Vn) WORD $(0x0E612800 | ((Vn)<<5) | (Vd))

// XTN2 Vd.8H, Vn.4S — narrow int32 → int16 (upper half)
#define XTN2_8H(Vd, Vn) WORD $(0x4E612800 | ((Vn)<<5) | (Vd))

// LD1R {Vt.4S}, [Xn] — load and replicate one float32
#define LD1R_4S(Vt, Xn) WORD $(0x4D40C800 | ((Xn)<<5) | (Vt))


// ============================================================================
// func lanczosHoriz10Row(dst []float32, src []byte, srcW int,
//                        offsets []int32, weights []float32, maxTaps int)
// ============================================================================
// Reads uint16 LE from src, writes float32 to dst.
// Stack: dst(0,8,16) src(24,32,40) srcW(48) offsets(56,64,72) weights(80,88,96) maxTaps(104)
TEXT ·lanczosHoriz10Row(SB), NOSPLIT, $0-112
	MOVD	dst_base+0(FP), R0       // dst ptr (float32)
	MOVD	dst_len+8(FP), R1        // dstW
	MOVD	src_base+24(FP), R2      // src ptr (uint16 LE)
	MOVD	src_len+32(FP), R3       // src len in bytes
	MOVD	srcW+48(FP), R13         // srcW (uint16 samples)
	MOVD	offsets_base+56(FP), R4  // offsets ptr
	MOVD	weights_base+80(FP), R5  // weights ptr
	MOVD	maxTaps+104(FP), R6      // maxTaps

	CBZ	R1, lh10_done
	MOVD	$0, R7                   // d = 0

lh10_pixel:
	// R8 = offsets[d] (int32, source sample index)
	LSL	$2, R7, R9
	ADD	R4, R9, R9
	MOVW	(R9), R8

	// R10 = &weights[d * maxTaps]
	MUL	R7, R6, R9
	LSL	$2, R9, R9
	ADD	R5, R9, R10

	// Scalar accumulate for this pixel (horizontal taps read from different positions)
	FMOVS	ZR, F0                   // accumulator
	MOVD	$0, R11                  // t = 0

lh10_tap:
	CMP	R6, R11
	BGE	lh10_store

	// Load weight
	LSL	$2, R11, R9
	ADD	R10, R9, R9
	FMOVS	(R9), F1                 // weight

	// Compute source index: sx = offset + t
	ADD	R8, R11, R12
	// Clamp: if sx < 0, sx = 0; if sx >= srcW, sx = srcW - 1
	TBNZ	$63, R12, lh10_clamp_low
	CMP	R13, R12
	BGE	lh10_clamp_high
	B	lh10_load_sample

lh10_clamp_low:
	MOVD	$0, R12
	B	lh10_load_sample

lh10_clamp_high:
	SUB	$1, R13, R12

lh10_load_sample:
	// Load uint16 LE sample at src[sx*2]
	LSL	$1, R12, R14
	ADD	R2, R14, R14
	MOVHU	(R14), R15
	UCVTFWS	R15, F2                  // float32(sample)
	FMADDS	F1, F0, F2, F0           // F0 += F1 * F2

	ADD	$1, R11
	B	lh10_tap

lh10_store:
	// Store dst[d] = F0
	LSL	$2, R7, R9
	ADD	R0, R9, R9
	FMOVS	F0, (R9)

	ADD	$1, R7
	CMP	R1, R7
	BLT	lh10_pixel

lh10_done:
	RET


// ============================================================================
// func lanczosVert10Row(dst []byte, temp []float32, tempStride int,
//                       startRow int32, weights []float32, maxTaps int)
// ============================================================================
// NEON vertical pass: 4-wide float32 FMLA, outputs uint16 LE clamped to [0, 1023].
// Stack: dst(0,8,16) temp(24,32,40) tempStride(48) startRow(56) weights(64,72,80) maxTaps(88)
TEXT ·lanczosVert10Row(SB), NOSPLIT, $0-96
	MOVD	dst_base+0(FP), R0
	MOVD	dst_len+8(FP), R1       // dstW * 2 (bytes)
	MOVD	temp_base+24(FP), R2
	MOVD	tempStride+48(FP), R3
	MOVW	startRow+56(FP), R4
	MOVD	weights_base+64(FP), R5
	MOVD	maxTaps+88(FP), R6

	// dstW = dst_len / 2
	LSR	$1, R1, R1

	CBZ	R1, lv10_done

	// stride_bytes = tempStride * 4
	LSL	$2, R3, R7

	// base = temp + startRow * stride_bytes
	MOVW	R4, R8
	MUL	R7, R8, R8
	ADD	R2, R8, R2               // R2 now points to starting row

	// Constants for NEON clamp
	VEOR	V16.B16, V16.B16, V16.B16   // V16 = 0.0
	MOVW	$0x44800000, R9              // 1024.0f (we use clamp to 0..1023, but need a suitable approach)
	// Actually use 1023.5 as upper bound before round-to-nearest
	MOVW	$0x447FC000, R9              // 1023.5f
	FMOVS	R9, F17
	VDUP_S0_4S(17, 17)                  // V17.4S = {1023.5, ...}

	// Scalar 0.5f for rounding
	MOVW	$0x3F000000, R9
	FMOVS	R9, F18

	MOVD	$0, R10                  // col = 0
	SUB	$3, R1, R11              // dstW - 3 (for 4-wide loop)

	// --- 4-wide NEON loop ---
lv10_neon4:
	CMP	R11, R10
	BGE	lv10_scalar_setup

	VEOR	V0.B16, V0.B16, V0.B16  // accumulator
	MOVD	$0, R12

lv10_neon4_tap:
	CMP	R6, R12
	BGE	lv10_neon4_cvt

	// Load weight[t] and broadcast
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
	B	lv10_neon4_tap

lv10_neon4_cvt:
	// Clamp to [0, 1023.5], round to nearest, convert to int, clamp to [0, 1023]
	FMAX_4S(0, 0, 16)               // max(val, 0)
	FMIN_4S(0, 0, 17)               // min(val, 1023.5)
	FCVTNS_4S(0, 0)                 // round to nearest int32

	// Narrow int32 → int16
	XTN_4H(5, 0)                    // V5.4H = narrow(V0.4S)

	// Store 4 uint16 (8 bytes) via FMOVD
	ADD	R0, R10, R14
	LSL	$1, R14, R14             // byte offset = col * 2
	// Recalculate: R0 is byte pointer, R10 is column index
	// dst byte offset = R10 * 2
	LSL	$1, R10, R9
	ADD	R0, R9, R14
	FMOVD	F5, R13
	MOVD	R13, (R14)

	ADD	$4, R10
	B	lv10_neon4

lv10_scalar_setup:
	CMP	R1, R10
	BGE	lv10_done

lv10_scalar:
	FMOVS	ZR, F0
	MOVD	$0, R12

lv10_scalar_tap:
	CMP	R6, R12
	BGE	lv10_scalar_store

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
	B	lv10_scalar_tap

lv10_scalar_store:
	FADDS	F18, F0, F0             // + 0.5 for rounding
	FCVTZSSW	F0, R13
	CMP	$0, R13
	CSEL	LT, ZR, R13, R13
	CMP	$1023, R13
	MOVD	$1023, R14
	CSEL	GT, R14, R13, R13
	// Store uint16 LE
	LSL	$1, R10, R9
	ADD	R0, R9, R9
	MOVHU	R13, (R9)
	ADD	$1, R10
	CMP	R1, R10
	BLT	lv10_scalar

lv10_done:
	RET
