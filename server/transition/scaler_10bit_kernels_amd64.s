#include "textflag.h"

// AMD64 AVX2/scalar kernel for 10-bit bilinear vertical row blend.
//
// blendRows10 blends two rows of uint16 LE samples with 16.16 fixed-point weight:
//   dst[i] = clamp10((top[i] * (65536-fy) + bot[i] * fy + 32768) >> 16)
//
// Max intermediate: 1023 * 65536 + 32768 = 67,076,096, fits in uint32/int32.
//
// AVX2 path: 8 samples/iter
//   VPMOVZXWD: zero-extend 8 uint16 → 8 int32
//   VPMULLD:   32-bit multiply
//   VPADDD:    32-bit add
//   VPSRLD:    32-bit shift right
//   VPACKUSDW: saturating pack 32→16
//   VPMINUW:   unsigned min with 1023 for 10-bit clamp

// ============================================================================
// func blendRows10(dst, topRow, botRow *byte, fy, count int)
// ============================================================================
TEXT ·blendRows10(SB), NOSPLIT, $0-40
	MOVQ dst+0(FP), DI         // dst *byte
	MOVQ topRow+8(FP), SI      // topRow *byte
	MOVQ botRow+16(FP), DX     // botRow *byte
	MOVQ fy+24(FP), R8         // fy [0..65536]
	MOVQ count+32(FP), CX      // count (number of uint16 samples)

	TESTQ CX, CX
	JLE   br10_done

	// Compute invFy = 65536 - fy
	MOVQ  $65536, R9
	SUBQ  R8, R9               // R9 = invFy

	// Check AVX2 availability
	CMPB ·avx2Available(SB), $1
	JE   br10_avx2

	JMP  br10_scalar

br10_avx2:
	// Broadcast invFy and fy as 8 x int32
	MOVQ    R9, X6
	VPBROADCASTD X6, Y6        // Y6 = [invFy] x 8
	MOVQ    R8, X7
	VPBROADCASTD X7, Y7        // Y7 = [fy] x 8

	// Y8 = [32768] x 8 (rounding constant)
	MOVQ    $32768, AX
	MOVQ    AX, X8
	VPBROADCASTD X8, Y8

	// Y9 = [1023] x 16 (clamp constant, uint16)
	MOVQ    $1023, AX
	MOVQ    AX, X9
	VPBROADCASTW X9, Y9

	CMPQ    CX, $8
	JLT     br10_avx2_tail

br10_avx2_loop:
	// Load 8 uint16 samples from top and bottom rows
	VMOVDQU (SI), X0           // X0 = top[i..i+7] as 8 x uint16
	VMOVDQU (DX), X1           // X1 = bot[i..i+7] as 8 x uint16

	// Zero-extend uint16 → int32
	VPMOVZXWD X0, Y0           // Y0 = top as 8 x int32
	VPMOVZXWD X1, Y1           // Y1 = bot as 8 x int32

	// Multiply: top * invFy, bot * fy
	VPMULLD Y6, Y0, Y0         // Y0 = top * invFy
	VPMULLD Y7, Y1, Y1         // Y1 = bot * fy

	// Sum + rounding
	VPADDD  Y1, Y0, Y0         // Y0 = top*invFy + bot*fy
	VPADDD  Y8, Y0, Y0         // Y0 += 32768

	// Shift right by 16
	VPSRLD  $16, Y0, Y0

	// Pack 8 x int32 → 8 x uint16 with unsigned saturation
	VPXOR   Y2, Y2, Y2
	VPACKUSDW Y2, Y0, Y0
	VPERMQ  $0xD8, Y0, Y0

	// Clamp to 1023
	VPMINUW Y9, Y0, Y0

	// Store 8 uint16 results
	VMOVDQU X0, (DI)

	ADDQ    $16, SI
	ADDQ    $16, DX
	ADDQ    $16, DI
	SUBQ    $8, CX
	CMPQ    CX, $8
	JGE     br10_avx2_loop

br10_avx2_tail:
	VZEROUPPER
	TESTQ   CX, CX
	JZ      br10_done

br10_scalar:
	MOVQ    $32768, R10
	MOVQ    $1023, R11

br10_scalar_loop:
	MOVWLZX (SI), AX           // top[i]
	MOVWLZX (DX), BX           // bot[i]

	IMULQ   R9, AX             // AX = top * invFy
	IMULQ   R8, BX             // BX = bot * fy
	ADDQ    BX, AX
	ADDQ    R10, AX            // AX += 32768
	SHRQ    $16, AX            // AX >>= 16

	CMPQ    AX, R11
	JLE     br10_scalar_store
	MOVQ    R11, AX

br10_scalar_store:
	MOVW    AX, (DI)

	ADDQ    $2, SI
	ADDQ    $2, DX
	ADDQ    $2, DI
	DECQ    CX
	JNZ     br10_scalar_loop

br10_done:
	RET
