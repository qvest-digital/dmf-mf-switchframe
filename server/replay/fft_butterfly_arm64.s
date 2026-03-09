#include "textflag.h"

// ARM64 NEON butterfly for radix-2 FFT stage.
//
// func butterflyRadix2(data, twiddle []float32, halfN, stride, twiddleStride int)
//
// For twiddleStride == 1, processes 2 butterflies per iteration using NEON.
// Falls back to scalar for other strides.

// NEON macros for instructions missing from Go assembler
#define FMUL_4S(Vd, Vn, Vm) WORD $(0x6E20DC00 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
#define FSUB_4S(Vd, Vn, Vm) WORD $(0x4EA0D400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
#define FMLA_4S(Vd, Vn, Vm) WORD $(0x4E20CC00 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
#define FADD_4S(Vd, Vn, Vm) WORD $(0x4E20D400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// TRN1 Vd.4S, Vn.4S, Vm.4S — zip even lanes
#define TRN1_4S(Vd, Vn, Vm) WORD $(0x4E802800 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
// TRN2 Vd.4S, Vn.4S, Vm.4S — zip odd lanes
#define TRN2_4S(Vd, Vn, Vm) WORD $(0x4E806800 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
// REV64 Vd.4S, Vn.4S — reverse 32-bit elements within 64-bit halves
#define REV64_4S(Vd, Vn) WORD $(0x4EA00800 | ((Vn)<<5) | (Vd))
// FNEG Vd.4S, Vn.4S — negate all lanes
#define FNEG_4S(Vd, Vn) WORD $(0x6EA0F800 | ((Vn)<<5) | (Vd))

// Sign mask for complex multiply: [-1, 1, -1, 1]
// Used to negate real parts after REV64 swap for im*im subtraction
DATA sign_mask<>+0x00(SB)/4, $0xBF800000  // -1.0
DATA sign_mask<>+0x04(SB)/4, $0x3F800000  // +1.0
DATA sign_mask<>+0x08(SB)/4, $0xBF800000  // -1.0
DATA sign_mask<>+0x0C(SB)/4, $0x3F800000  // +1.0
GLOBL sign_mask<>(SB), RODATA|NOPTR, $16

// Registers:
//   R0 = data ptr, R1 = twiddle ptr
//   R2 = halfN, R3 = stride (unused), R4 = twiddleStride
//   R5 = k (loop counter)
//   R6 = even offset, R7 = odd offset
//   R8 = twiddle offset
//   R9 = halfN * 8 (byte offset for odd half)
TEXT ·butterflyRadix2(SB), NOSPLIT, $0-80
	MOVD data_base+0(FP), R0
	MOVD data_len+8(FP), R10       // data length (not used for bounds)
	MOVD twiddle_base+24(FP), R1
	MOVD halfN+48(FP), R2
	MOVD stride+56(FP), R3
	MOVD twiddleStride+64(FP), R4

	CMP  $0, R2
	BLE  bf_done

	// Check if we can use NEON path (twiddleStride == 1)
	CMP  $1, R4
	BNE  bf_scalar_init

	// --- NEON path (twiddleStride == 1) ---
	// Process 2 butterflies per iteration (4 complex floats = 16 bytes per half)
	// Load sign mask for complex multiply
	MOVD $sign_mask<>(SB), R8
	VLD1 (R8), [V7.S4]             // V7 = [-1, 1, -1, 1]

	// halfN * 8 = byte offset from even to odd (halfN complex pairs * 2 floats * 4 bytes)
	LSL  $3, R2, R9                // R9 = halfN * 8

	MOVD $0, R5                    // k = 0
	CMP  $2, R2
	BLT  bf_neon_tail

bf_neon_loop:
	// k and k+1 butterflies
	// Even: data[k*2..k*2+3], Odd: data[(k+halfN)*2..(k+halfN)*2+3]
	// Twiddle: twiddle[k*2..k*2+3] (consecutive since twiddleStride=1)
	LSL  $3, R5, R6                // R6 = k * 8 (byte offset for even)
	ADD  R0, R6, R6                // R6 = &data[k*2]

	// Load twiddle factors for k and k+1
	LSL  $3, R5, R8                // R8 = k * 8 (twiddle byte offset)
	ADD  R1, R8, R8                // R8 = &twiddle[k*2]
	VLD1 (R8), [V0.S4]            // V0 = [wRe0, wIm0, wRe1, wIm1]

	// Load even data
	VLD1 (R6), [V1.S4]            // V1 = [eRe0, eIm0, eRe1, eIm1]

	// Load odd data
	ADD  R9, R6, R7                // R7 = &data[(k+halfN)*2]
	VLD1 (R7), [V2.S4]            // V2 = [oRe0, oIm0, oRe1, oIm1]

	// Complex multiply: t = W * odd
	// tRe = wRe*oRe - wIm*oIm
	// tIm = wRe*oIm + wIm*oRe
	//
	// Strategy:
	// 1. TRN1 to get [wRe0, wRe0, wRe1, wRe1]
	// 2. TRN2 to get [wIm0, wIm0, wIm1, wIm1]
	// 3. FMUL wRe_dup * odd = [wRe*oRe, wRe*oIm, wRe*oRe, wRe*oIm]
	// 4. REV64 odd to swap re/im = [oIm0, oRe0, oIm1, oRe1]
	// 5. FMUL wIm_dup * swapped = [wIm*oIm, wIm*oRe, wIm*oIm, wIm*oRe]
	// 6. Sign mask multiply: [-wIm*oIm, wIm*oRe, -wIm*oIm, wIm*oRe]
	// 7. FADD step3 + step6 = [wRe*oRe - wIm*oIm, wRe*oIm + wIm*oRe, ...]

	TRN1_4S(3, 0, 0)              // V3 = [wRe0, wRe0, wRe1, wRe1]
	TRN2_4S(4, 0, 0)              // V4 = [wIm0, wIm0, wIm1, wIm1]
	FMUL_4S(5, 3, 2)              // V5 = wRe * odd
	REV64_4S(6, 2)                // V6 = [oIm0, oRe0, oIm1, oRe1]
	FMUL_4S(6, 4, 6)              // V6 = wIm * swapped
	FMUL_4S(6, 6, 7)              // V6 = sign_mask * (wIm * swapped)
	FADD_4S(5, 5, 6)              // V5 = t = W * odd

	// Butterfly: even' = even + t, odd' = even - t
	FADD_4S(3, 1, 5)              // V3 = even + t
	FSUB_4S(4, 1, 5)              // V4 = even - t

	// Store results
	VST1 [V3.S4], (R6)            // store even'
	VST1 [V4.S4], (R7)            // store odd'

	ADD  $2, R5                    // k += 2
	CMP  R2, R5
	// Need at least 2 more iterations
	ADD  $1, R5, R8                // R8 = k + 1
	CMP  R2, R8
	BLT  bf_neon_loop

bf_neon_tail:
	// Handle remaining single butterfly (if halfN is odd)
	CMP  R2, R5
	BGE  bf_done

	// Single scalar butterfly
	LSL  $3, R5, R6                // even byte offset
	ADD  R0, R6, R6
	ADD  R9, R6, R7                // odd byte offset

	LSL  $3, R5, R8
	ADD  R1, R8, R8

	// Load twiddle
	FMOVS (R8), F0                // wRe
	FMOVS 4(R8), F1               // wIm

	// Load odd
	FMOVS (R7), F2                // oRe
	FMOVS 4(R7), F3               // oIm

	// Complex multiply
	FMULS F0, F2, F4              // wRe * oRe
	FMULS F1, F3, F5              // wIm * oIm
	FSUBS F5, F4, F4              // tRe = wRe*oRe - wIm*oIm

	FMULS F0, F3, F5              // wRe * oIm
	FMULS F1, F2, F6              // wIm * oRe
	FADDS F6, F5, F5              // tIm = wRe*oIm + wIm*oRe

	// Load even
	FMOVS (R6), F2                // eRe
	FMOVS 4(R6), F3               // eIm

	// Butterfly
	FADDS F4, F2, F6              // even' Re = eRe + tRe
	FADDS F5, F3, F7              // even' Im = eIm + tIm
	FSUBS F4, F2, F2              // odd' Re = eRe - tRe
	FSUBS F5, F3, F3              // odd' Im = eIm - tIm

	FMOVS F6, (R6)               // store even' Re
	FMOVS F7, 4(R6)              // store even' Im
	FMOVS F2, (R7)               // store odd' Re
	FMOVS F3, 4(R7)              // store odd' Im

	B    bf_done

bf_scalar_init:
	// --- Scalar path (twiddleStride != 1) ---
	LSL  $3, R2, R9               // R9 = halfN * 8 (byte offset for odd)
	MOVD $0, R5                   // k = 0

bf_scalar_loop:
	CMP  R2, R5
	BGE  bf_done

	// Twiddle index: k * twiddleStride * 2 * 4 bytes = k * twiddleStride * 8
	MUL  R4, R5, R8               // k * twiddleStride
	LSL  $3, R8                   // * 8 bytes
	ADD  R1, R8, R8               // &twiddle[k*twiddleStride*2]

	// Even/odd data offsets
	LSL  $3, R5, R6               // k * 8
	ADD  R0, R6, R6               // &data[k*2]
	ADD  R9, R6, R7               // &data[(k+halfN)*2]

	// Load twiddle
	FMOVS (R8), F0                // wRe
	FMOVS 4(R8), F1               // wIm

	// Load odd
	FMOVS (R7), F2                // oRe
	FMOVS 4(R7), F3               // oIm

	// Complex multiply: t = W * odd
	FMULS F0, F2, F4              // wRe * oRe
	FMULS F1, F3, F5              // wIm * oIm
	FSUBS F5, F4, F4              // tRe = wRe*oRe - wIm*oIm

	FMULS F0, F3, F5              // wRe * oIm
	FMULS F1, F2, F6              // wIm * oRe
	FADDS F6, F5, F5              // tIm = wRe*oIm + wIm*oRe

	// Load even
	FMOVS (R6), F2                // eRe
	FMOVS 4(R6), F3               // eIm

	// Butterfly: even' = even + t, odd' = even - t
	FADDS F4, F2, F6              // even' Re
	FADDS F5, F3, F7              // even' Im
	FSUBS F4, F2, F2              // odd' Re
	FSUBS F5, F3, F3              // odd' Im

	// Store
	FMOVS F6, (R6)
	FMOVS F7, 4(R6)
	FMOVS F2, (R7)
	FMOVS F3, 4(R7)

	ADD  $1, R5
	B    bf_scalar_loop

bf_done:
	RET
