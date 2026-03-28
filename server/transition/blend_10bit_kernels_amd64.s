#include "textflag.h"

// AMD64 AVX2/scalar kernel for 10-bit (uint16 LE) blend.
//
// blendMix10Row blends count uint16 LE samples:
//   dst[i] = clamp10((a[i] * (256-pos) + b[i] * pos + 128) >> 8)
//
// Max intermediate: 1023 * 256 + 128 = 262016, fits in uint32/int32.
//
// AVX2 path: 8 samples/iter
//   VPMOVZXWD: zero-extend 8 uint16 → 8 int32 (128-bit src → 256-bit dst)
//   VPMULLD:   32-bit multiply
//   VPADDD:    32-bit add
//   VPSRLD:    32-bit shift right
//   VPACKUSDW: saturating pack 32→16 (clamps negative to 0, >65535 to 65535)
//   VPMINUW:   unsigned min with 1023 for 10-bit clamp
//
// Scalar fallback for non-AVX2 and tail.

// ============================================================================
// func blendMix10Row(dst, a, b *byte, count, pos256 int)
// ============================================================================
TEXT ·blendMix10Row(SB), NOSPLIT, $0-40
	MOVQ dst+0(FP), DI         // dst *byte
	MOVQ a+8(FP), SI           // a *byte
	MOVQ b+16(FP), DX          // b *byte
	MOVQ count+24(FP), CX      // count (number of uint16 samples)
	MOVQ pos256+32(FP), R8     // pos256 [0..256]

	TESTQ CX, CX
	JLE   mix10_done

	// Compute inv = 256 - pos
	MOVQ  $256, R9
	SUBQ  R8, R9               // R9 = inv

	// Check AVX2 availability
	CMPB ·avx2Available(SB), $1
	JE   mix10_avx2

	// --- Scalar path ---
	JMP  mix10_scalar

mix10_avx2:
	// Broadcast inv and pos as 8 x int32
	MOVQ    R9, X6
	VPBROADCASTD X6, Y6        // Y6 = [inv] x 8 (int32)
	MOVQ    R8, X7
	VPBROADCASTD X7, Y7        // Y7 = [pos] x 8 (int32)

	// Y8 = [128] x 8 (rounding constant)
	MOVQ    $128, AX
	MOVQ    AX, X8
	VPBROADCASTD X8, Y8

	// Y9 = [1023] x 16 (clamp constant, uint16)
	MOVQ    $1023, AX
	MOVQ    AX, X9
	VPBROADCASTW X9, Y9

	CMPQ    CX, $8
	JLT     mix10_avx2_tail

mix10_avx2_loop:
	// Load 8 uint16 samples (16 bytes) from a and b
	VMOVDQU (SI), X0           // X0 = a[i..i+7] as 8 x uint16 (128-bit)
	VMOVDQU (DX), X1           // X1 = b[i..i+7] as 8 x uint16

	// Zero-extend uint16 → int32 (8 samples → 8 dwords in YMM)
	VPMOVZXWD X0, Y0           // Y0 = a[0..7] as 8 x int32
	VPMOVZXWD X1, Y1           // Y1 = b[0..7] as 8 x int32

	// Multiply: a * inv, b * pos (32-bit)
	VPMULLD Y6, Y0, Y0         // Y0 = a * inv
	VPMULLD Y7, Y1, Y1         // Y1 = b * pos

	// Sum + rounding
	VPADDD  Y1, Y0, Y0         // Y0 = a*inv + b*pos
	VPADDD  Y8, Y0, Y0         // Y0 += 128

	// Shift right by 8
	VPSRLD  $8, Y0, Y0         // Y0 >>= 8

	// Pack 8 x int32 → 8 x uint16 with unsigned saturation
	// VPACKUSDW operates on 128-bit lanes, so we need to handle lane crossing.
	// Y0 = [d0 d1 d2 d3 | d4 d5 d6 d7]
	// We pack Y0 with itself (upper half is garbage, we only want lower)
	// Actually, we have exactly 8 dwords in Y0 that need to become 8 words.
	//
	// VPACKUSDW packs within 128-bit lanes:
	//   Lane0: Y0[0..3] + Yzero[0..3] → 8 words in lane 0
	//   Lane1: Y0[4..7] + Yzero[4..7] → 8 words in lane 1
	// Then VPERMQ to fix lane ordering.
	VPXOR   Y2, Y2, Y2         // Y2 = zero
	VPACKUSDW Y2, Y0, Y0       // Pack: lane0=[d0,d1,d2,d3,0,0,0,0], lane1=[d4,d5,d6,d7,0,0,0,0]
	VPERMQ  $0xD8, Y0, Y0      // Fix lane order: [d0..d3,d4..d7,0..0,0..0]
	// Now X0 (lower 128 bits) has our 8 results as uint16

	// Clamp to 1023
	VPMINUW Y9, Y0, Y0         // Y0 = min(Y0, 1023)

	// Store 8 uint16 results (16 bytes)
	VMOVDQU X0, (DI)

	ADDQ    $16, SI
	ADDQ    $16, DX
	ADDQ    $16, DI
	SUBQ    $8, CX
	CMPQ    CX, $8
	JGE     mix10_avx2_loop

mix10_avx2_tail:
	VZEROUPPER
	TESTQ   CX, CX
	JZ      mix10_done
	// Fall through to scalar for remaining samples

mix10_scalar:
	// R8 = pos, R9 = inv, R10 = 128 (rounding), R11 = 1023 (clamp)
	MOVQ    $128, R10
	MOVQ    $1023, R11

mix10_scalar_loop:
	// Load uint16 LE from a and b
	MOVWLZX (SI), AX           // AX = a[i] (uint16, zero-extended to 32-bit)
	MOVWLZX (DX), BX           // BX = b[i]

	// result = (a * inv + b * pos + 128) >> 8
	IMULQ   R9, AX             // AX = a * inv
	IMULQ   R8, BX             // BX = b * pos
	ADDQ    BX, AX             // AX = a*inv + b*pos
	ADDQ    R10, AX            // AX += 128
	SHRQ    $8, AX             // AX >>= 8

	// Clamp to 1023
	CMPQ    AX, R11
	JLE     mix10_scalar_store
	MOVQ    R11, AX

mix10_scalar_store:
	MOVW    AX, (DI)           // Store uint16 LE

	ADDQ    $2, SI
	ADDQ    $2, DX
	ADDQ    $2, DI
	DECQ    CX
	JNZ     mix10_scalar_loop

mix10_done:
	RET
