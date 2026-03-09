#include "textflag.h"

// ARM64 NEON kernels for float32 vector operations.
// Processes 4 float32s per iteration (128-bit NEON registers).
//
// The Go ARM64 assembler lacks several NEON float instructions, so we encode
// them via WORD macros. Register encoding uses 5-bit fields (V0=0 .. V31=31).

// --- Float32 NEON macros ---
// FADD Vd.4S, Vn.4S, Vm.4S — vector float add
#define FADD_4S(Vd, Vn, Vm) WORD $(0x4E20D400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
// FMUL Vd.4S, Vn.4S, Vm.4S — vector float multiply
#define FMUL_4S(Vd, Vn, Vm) WORD $(0x6E20DC00 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
// DUP Vd.4S, Vn.S[0] — broadcast element 0 of Vn to all 4 lanes of Vd
#define VDUP_S0_4S(Vd, Vn) WORD $(0x4E040400 | ((Vn)<<5) | (Vd))

// ============================================================================
// func AddFloat32(dst, src *float32, n int)
// ============================================================================
// dst[i] += src[i] for n elements.
TEXT ·AddFloat32(SB), NOSPLIT, $0-24
	MOVD dst+0(FP), R0
	MOVD src+8(FP), R1
	MOVD n+16(FP), R2

	CMP  $0, R2
	BLE  add_done

	CMP  $4, R2
	BLT  add_scalar

add_neon_loop:
	VLD1 (R0), [V0.S4]       // dst[i..i+3]
	VLD1 (R1), [V1.S4]       // src[i..i+3]
	FADD_4S(0, 0, 1)         // V0 = V0 + V1
	VST1 [V0.S4], (R0)       // store result

	ADD  $16, R0
	ADD  $16, R1
	SUB  $4, R2, R2
	CMP  $4, R2
	BGE  add_neon_loop

add_scalar:
	CMP  $0, R2
	BLE  add_done

add_scalar_loop:
	FMOVS (R0), F0
	FMOVS (R1), F1
	FADDS F1, F0, F0
	FMOVS F0, (R0)

	ADD  $4, R0
	ADD  $4, R1
	SUB  $1, R2, R2
	CMP  $0, R2
	BGT  add_scalar_loop

add_done:
	RET

// ============================================================================
// func ScaleFloat32(dst *float32, scale float32, n int)
// ============================================================================
// dst[i] *= scale for n elements.
// scale is at offset +8 (after dst pointer), n at offset +16.
TEXT ·ScaleFloat32(SB), NOSPLIT, $0-24
	MOVD  dst+0(FP), R0
	FMOVS scale+8(FP), F5    // F5 = scale (scalar in V5.S[0])
	MOVD  n+16(FP), R2

	CMP  $0, R2
	BLE  scale_done

	// Broadcast V5.S[0] to all 4 lanes of V5.4S
	VDUP_S0_4S(5, 5)          // V5.4S = [scale, scale, scale, scale]

	CMP  $4, R2
	BLT  scale_scalar

scale_neon_loop:
	VLD1 (R0), [V0.S4]       // dst[i..i+3]
	FMUL_4S(0, 0, 5)         // V0 = V0 * V5
	VST1 [V0.S4], (R0)       // store result

	ADD  $16, R0
	SUB  $4, R2, R2
	CMP  $4, R2
	BGE  scale_neon_loop

scale_scalar:
	CMP  $0, R2
	BLE  scale_done

	// Reload scalar scale from V5.S[0] (VDUP modified the vector but F5 is V5.S[0])
scale_scalar_loop:
	FMOVS (R0), F0
	FMULS F5, F0, F0
	FMOVS F0, (R0)

	ADD  $4, R0
	SUB  $1, R2, R2
	CMP  $0, R2
	BGT  scale_scalar_loop

scale_done:
	RET

// ============================================================================
// func MulAddFloat32(dst, a, x, b, y *float32, n int)
// ============================================================================
// dst[i] = a[i]*x[i] + b[i]*y[i] for n elements.
// 6 args: dst+0(FP), a+8(FP), x+16(FP), b+24(FP), y+32(FP), n+40(FP)
TEXT ·MulAddFloat32(SB), NOSPLIT, $0-48
	MOVD dst+0(FP), R0         // R0 = dst
	MOVD a+8(FP), R1           // R1 = a
	MOVD x+16(FP), R2          // R2 = x
	MOVD b+24(FP), R3          // R3 = b
	MOVD y+32(FP), R4          // R4 = y
	MOVD n+40(FP), R5          // R5 = n

	CMP  $0, R5
	BLE  muladd_done

	CMP  $4, R5
	BLT  muladd_scalar

muladd_neon_loop:
	VLD1 (R1), [V0.S4]        // V0 = a[i..i+3]
	VLD1 (R2), [V1.S4]        // V1 = x[i..i+3]
	FMUL_4S(0, 0, 1)          // V0 = a * x
	VLD1 (R3), [V2.S4]        // V2 = b[i..i+3]
	VLD1 (R4), [V3.S4]        // V3 = y[i..i+3]
	FMUL_4S(2, 2, 3)          // V2 = b * y
	FADD_4S(0, 0, 2)          // V0 = a*x + b*y
	VST1 [V0.S4], (R0)        // store to dst

	ADD  $16, R0
	ADD  $16, R1
	ADD  $16, R2
	ADD  $16, R3
	ADD  $16, R4
	SUB  $4, R5, R5
	CMP  $4, R5
	BGE  muladd_neon_loop

muladd_scalar:
	CMP  $0, R5
	BLE  muladd_done

muladd_scalar_loop:
	FMOVS (R1), F0             // a[i]
	FMOVS (R2), F1             // x[i]
	FMULS F1, F0, F0           // a*x
	FMOVS (R3), F2             // b[i]
	FMOVS (R4), F3             // y[i]
	FMULS F3, F2, F2           // b*y
	FADDS F2, F0, F0           // a*x + b*y
	FMOVS F0, (R0)             // store to dst

	ADD  $4, R0
	ADD  $4, R1
	ADD  $4, R2
	ADD  $4, R3
	ADD  $4, R4
	SUB  $1, R5, R5
	CMP  $0, R5
	BGT  muladd_scalar_loop

muladd_done:
	RET

// --- NEON float macros for PeakAbsFloat32 ---
// FABS Vd.4S, Vn.4S — vector float absolute value
#define FABS_4S(Vd, Vn) WORD $(0x4EA0F800 | ((Vn)<<5) | (Vd))
// FMAX Vd.4S, Vn.4S, Vm.4S — vector float max (element-wise)
#define FMAX_4S(Vd, Vn, Vm) WORD $(0x4E20F400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
// FMAXV Sd, Vn.4S — max across all 4 lanes (horizontal reduction)
#define FMAXV_S(Vd, Vn) WORD $(0x6E30F800 | ((Vn)<<5) | (Vd))

// ============================================================================
// func PeakAbsFloat32(data *float32, n int) float32
// ============================================================================
// Returns max(|data[i]|) for i in [0, n).
// NEON path processes 4 float32s per iteration using FABS + FMAX.
// Uses V4 as the running max accumulator.
TEXT ·PeakAbsFloat32(SB), NOSPLIT, $0-24
	MOVD data+0(FP), R0       // R0 = data pointer
	MOVD n+8(FP), R1          // R1 = n

	// Initialize accumulator to 0
	VEOR V4.B16, V4.B16, V4.B16  // V4 = [0, 0, 0, 0]

	CMP  $0, R1
	BLE  peak_store

	CMP  $16, R1
	BLT  peak_scalar

	// Process 16 floats per iteration (unrolled 4x)
peak_neon16:
	VLD1 (R0), [V0.S4, V1.S4, V2.S4, V3.S4]  // load 16 floats
	FABS_4S(0, 0)              // V0 = |V0|
	FABS_4S(1, 1)              // V1 = |V1|
	FABS_4S(2, 2)              // V2 = |V2|
	FABS_4S(3, 3)              // V3 = |V3|
	FMAX_4S(0, 0, 1)          // V0 = max(V0, V1)
	FMAX_4S(2, 2, 3)          // V2 = max(V2, V3)
	FMAX_4S(4, 4, 0)          // V4 = max(V4, V0)
	FMAX_4S(4, 4, 2)          // V4 = max(V4, V2)

	ADD  $64, R0
	SUB  $16, R1, R1
	CMP  $16, R1
	BGE  peak_neon16

	CMP  $4, R1
	BLT  peak_scalar

	// Process 4 floats per iteration (remainder)
peak_neon4:
	VLD1 (R0), [V0.S4]        // load 4 floats
	FABS_4S(0, 0)              // V0 = |V0|
	FMAX_4S(4, 4, 0)          // V4 = max(V4, V0)

	ADD  $16, R0
	SUB  $4, R1, R1
	CMP  $4, R1
	BGE  peak_neon4

peak_scalar:
	// Horizontal reduction: max across V4 lanes
	FMAXV_S(4, 4)             // S4 = max(V4[0..3])

	CMP  $0, R1
	BLE  peak_store

	// Process remaining 1-3 elements scalar
peak_scalar_loop:
	FMOVS (R0), F0
	FABSS F0, F0               // |val|
	FMAXS F0, F4, F4          // S4 = max(S4, |val|)

	ADD  $4, R0
	SUB  $1, R1, R1
	CMP  $0, R1
	BGT  peak_scalar_loop

peak_store:
	FMOVS F4, ret+16(FP)
	RET

// --- NEON macros for stereo deinterleave ---
// UZP1 Vd.4S, Vn.4S, Vm.4S — deinterleave even elements
#define UZP1_4S(Vd, Vn, Vm) WORD $(0x4E801800 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
// UZP2 Vd.4S, Vn.4S, Vm.4S — deinterleave odd elements
#define UZP2_4S(Vd, Vn, Vm) WORD $(0x4E805800 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// ============================================================================
// func PeakAbsStereoFloat32(data *float32, n int) (peakL, peakR float32)
// ============================================================================
// Returns max(|data[2i]|) and max(|data[2i+1]|) for interleaved stereo data.
// n = total number of samples (must be even).
// NEON path loads 8 floats [L R L R L R L R], uses UZP1/UZP2 to deinterleave
// into left and right vectors, then FABS + FMAX.
//
// Register allocation:
//   R0 = data pointer, R1 = remaining samples
//   V4 = left max accumulator, V5 = right max accumulator
//   V0/V1 = loaded data, V2/V3 = deinterleaved left/right
TEXT ·PeakAbsStereoFloat32(SB), NOSPLIT, $0-24
	MOVD data+0(FP), R0       // R0 = data pointer
	MOVD n+8(FP), R1          // R1 = total samples

	// Initialize accumulators to 0
	VEOR V4.B16, V4.B16, V4.B16  // left max
	VEOR V5.B16, V5.B16, V5.B16  // right max

	CMP  $2, R1
	BLT  stereo_store

	CMP  $8, R1
	BLT  stereo_scalar

	// Process 8 samples per iteration (4 stereo pairs)
stereo_neon8:
	VLD1 (R0), [V0.S4, V1.S4]  // V0=[L0 R0 L1 R1], V1=[L2 R2 L3 R3]
	UZP1_4S(2, 0, 1)           // V2=[L0 L1 L2 L3] (even elements)
	UZP2_4S(3, 0, 1)           // V3=[R0 R1 R2 R3] (odd elements)
	FABS_4S(2, 2)              // V2 = |left|
	FABS_4S(3, 3)              // V3 = |right|
	FMAX_4S(4, 4, 2)           // V4 = max(V4, |left|)
	FMAX_4S(5, 5, 3)           // V5 = max(V5, |right|)

	ADD  $32, R0               // advance 8 floats
	SUB  $8, R1, R1
	CMP  $8, R1
	BGE  stereo_neon8

stereo_scalar:
	// Horizontal reduction
	FMAXV_S(4, 4)             // S4 = max across V4
	FMAXV_S(5, 5)             // S5 = max across V5

	CMP  $2, R1
	BLT  stereo_store

	// Process remaining stereo pairs scalar
stereo_scalar_loop:
	FMOVS (R0), F0             // left sample
	FABSS F0, F0
	FMAXS F0, F4, F4

	FMOVS 4(R0), F1            // right sample
	FABSS F1, F1
	FMAXS F1, F5, F5

	ADD  $8, R0                // advance 2 floats
	SUB  $2, R1, R1
	CMP  $2, R1
	BGE  stereo_scalar_loop

stereo_store:
	FMOVS F4, ret+16(FP)      // peakL
	FMOVS F5, ret+20(FP)      // peakR
	RET
