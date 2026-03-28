#include "textflag.h"

// ARM64 NEON kernels for 10-bit alpha blend (RGBA overlay onto YUV422P10LE).
//
// alphaBlendRGBA10YRow: per-pixel Y plane blend
// alphaBlendRGBA10UVRow: per-chroma Cb/Cr plane blend

// ============================================================================
// func alphaBlendRGBA10YRow(yPlane *byte, rgba *byte, count int)
// ============================================================================
// For each pixel:
//   overlayY = ((66*R + 129*G + 25*B + 128) >> 8 + 16) * 4
//   result = (overlayY * A + existingY * (255-A) + 128) >> 8
//   clamp to [0, 1023]
TEXT ·alphaBlendRGBA10YRow(SB), NOSPLIT, $0-24
	MOVD yPlane+0(FP), R0      // Y plane ptr (uint16 LE)
	MOVD rgba+8(FP), R1        // RGBA ptr (4 bytes per pixel)
	MOVD count+16(FP), R2      // pixel count

	CMP  $0, R2
	BLE  aby10_done

	// Constants
	MOVD $66, R3
	MOVD $129, R4
	MOVD $25, R5
	MOVD $128, R6
	MOVD $16, R7
	MOVD $255, R8
	MOVD $1023, R9

aby10_loop:
	// Load RGBA pixel
	MOVBU 3(R1), R10           // A
	CBZ   R10, aby10_skip      // skip if fully transparent

	MOVBU (R1), R11            // R
	MOVBU 1(R1), R12           // G
	MOVBU 2(R1), R13           // B

	// y8 = (66*R + 129*G + 25*B + 128) >> 8 + 16
	MUL   R3, R11, R14        // 66*R
	MUL   R4, R12, R15        // 129*G
	ADD   R15, R14, R14       // 66*R + 129*G
	MUL   R5, R13, R15        // 25*B
	ADD   R15, R14, R14       // + 25*B
	ADD   R6, R14, R14        // + 128
	LSR   $8, R14, R14        // >> 8
	ADD   R7, R14, R14        // + 16
	LSL   $2, R14, R14        // * 4 = overlayY (10-bit)

	// Load existing Y (uint16 LE)
	MOVHU (R0), R15

	// result = (overlayY * A + existingY * (255-A) + 128) >> 8
	SUB   R10, R8, R16        // 255 - A = inv
	MUL   R10, R14, R14       // overlayY * A
	MUL   R16, R15, R15       // existingY * inv
	ADD   R15, R14, R14       // sum
	ADD   R6, R14, R14        // + 128
	LSR   $8, R14, R14        // >> 8

	// Clamp to [0, 1023]
	CMP   R9, R14
	CSEL  LT, R14, R9, R14

	// Store uint16 LE
	MOVHU R14, (R0)

aby10_skip:
	ADD  $2, R0               // advance Y ptr
	ADD  $4, R1               // advance RGBA ptr
	SUB  $1, R2, R2
	CBNZ R2, aby10_loop

aby10_done:
	RET


// ============================================================================
// func alphaBlendRGBA10UVRow(cbPlane, crPlane *byte, rgba *byte, chromaCount int)
// ============================================================================
// For each chroma sample (2 horizontal pixels), uses the left RGBA pixel.
//   cb8 = (-38*R - 74*G + 112*B + 128) >> 8 + 128; cb10 = cb8 * 4
//   cr8 = (112*R - 94*G - 18*B + 128) >> 8 + 128; cr10 = cr8 * 4
//   resultCb = (overlayCb * A + existingCb * (255-A) + 128) >> 8
//   resultCr = (overlayCr * A + existingCr * (255-A) + 128) >> 8
TEXT ·alphaBlendRGBA10UVRow(SB), NOSPLIT, $0-32
	MOVD cbPlane+0(FP), R0     // Cb ptr
	MOVD crPlane+8(FP), R1     // Cr ptr
	MOVD rgba+16(FP), R2       // RGBA ptr (stride = frameW * 4, but we skip by 8 = 2 pixels)
	MOVD chromaCount+24(FP), R3

	CMP  $0, R3
	BLE  abuv10_done

	MOVD $128, R4              // rounding constant
	MOVD $255, R5
	MOVD $1023, R6

abuv10_loop:
	// Load left pixel RGBA (even column)
	MOVBU 3(R2), R7            // A
	CBZ   R7, abuv10_skip

	MOVBU (R2), R8             // R
	MOVBU 1(R2), R9            // G
	MOVBU 2(R2), R10           // B

	// Cb8 = (-38*R - 74*G + 112*B + 128) >> 8 + 128
	// Use signed arithmetic carefully:
	// sum = -38*R - 74*G + 112*B + 128
	MOVD  $112, R11
	MUL   R11, R10, R11       // 112*B
	ADD   R4, R11, R11        // + 128
	MOVD  $38, R12
	MUL   R12, R8, R12        // 38*R
	SUB   R12, R11, R11       // - 38*R
	MOVD  $74, R12
	MUL   R12, R9, R12        // 74*G
	SUB   R12, R11, R11       // - 74*G
	ASR   $8, R11, R11        // >> 8 (signed)
	ADD   R4, R11, R11        // + 128
	LSL   $2, R11, R11        // * 4 = overlayCb

	// Cr8 = (112*R - 94*G - 18*B + 128) >> 8 + 128
	MOVD  $112, R12
	MUL   R12, R8, R12        // 112*R
	ADD   R4, R12, R12        // + 128
	MOVD  $94, R13
	MUL   R13, R9, R13        // 94*G
	SUB   R13, R12, R12       // - 94*G
	MOVD  $18, R13
	MUL   R13, R10, R13       // 18*B
	SUB   R13, R12, R12       // - 18*B
	ASR   $8, R12, R12        // >> 8 (signed)
	ADD   R4, R12, R12        // + 128
	LSL   $2, R12, R12        // * 4 = overlayCr

	// Alpha blend Cb
	SUB   R7, R5, R13         // 255 - A = inv
	MOVHU (R0), R14            // existing Cb
	MUL   R7, R11, R15        // overlayCb * A
	MUL   R13, R14, R14       // existingCb * inv
	ADD   R14, R15, R15       // sum
	ADD   R4, R15, R15        // + 128
	ASR   $8, R15, R15        // >> 8

	// Clamp Cb
	CMP   $0, R15
	CSEL  GT, R15, ZR, R15
	CMP   R6, R15
	CSEL  LT, R15, R6, R15
	MOVHU R15, (R0)

	// Alpha blend Cr
	MOVHU (R1), R14            // existing Cr
	MUL   R7, R12, R15        // overlayCr * A
	MUL   R13, R14, R14       // existingCr * inv
	ADD   R14, R15, R15       // sum
	ADD   R4, R15, R15        // + 128
	ASR   $8, R15, R15        // >> 8

	// Clamp Cr
	CMP   $0, R15
	CSEL  GT, R15, ZR, R15
	CMP   R6, R15
	CSEL  LT, R15, R6, R15
	MOVHU R15, (R1)

abuv10_skip:
	ADD  $2, R0               // advance Cb ptr (1 uint16)
	ADD  $2, R1               // advance Cr ptr
	ADD  $8, R2               // advance RGBA ptr (skip 2 pixels = 8 bytes)
	SUB  $1, R3, R3
	CBNZ R3, abuv10_loop

abuv10_done:
	RET
