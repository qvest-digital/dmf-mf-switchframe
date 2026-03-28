#include "textflag.h"

// AMD64 scalar kernels for 10-bit alpha blend (RGBA overlay onto YUV422P10LE).
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
	MOVQ yPlane+0(FP), DI       // Y plane ptr (uint16 LE)
	MOVQ rgba+8(FP), SI         // RGBA ptr (4 bytes per pixel)
	MOVQ count+16(FP), CX       // pixel count

	TESTQ CX, CX
	JLE   aby10_done

aby10_loop:
	// Load alpha
	MOVBLZX 3(SI), R8           // R8 = A
	TESTQ   R8, R8
	JZ      aby10_skip           // skip if fully transparent

	// Load R, G, B
	MOVBLZX (SI), AX            // AX = R
	MOVBLZX 1(SI), BX           // BX = G
	MOVBLZX 2(SI), DX           // DX = B

	// y8 = (66*R + 129*G + 25*B + 128) >> 8 + 16
	// 66*R
	MOVQ    $66, R9
	IMULQ   R9, AX              // AX = 66*R
	// 129*G
	MOVQ    $129, R9
	MOVQ    BX, R10
	IMULQ   R9, R10             // R10 = 129*G
	ADDQ    R10, AX             // AX = 66*R + 129*G
	// 25*B
	MOVQ    $25, R9
	IMULQ   R9, DX              // DX = 25*B
	ADDQ    DX, AX              // AX += 25*B
	ADDQ    $128, AX            // AX += 128
	SHRQ    $8, AX              // AX >>= 8
	ADDQ    $16, AX             // AX += 16 = y8
	SHLQ    $2, AX              // AX *= 4 = overlayY (10-bit)

	// Load existing Y (uint16 LE)
	MOVWLZX (DI), DX            // DX = existingY

	// result = (overlayY * A + existingY * (255-A) + 128) >> 8
	MOVQ    $255, R9
	SUBQ    R8, R9              // R9 = 255 - A = inv
	IMULQ   R8, AX              // AX = overlayY * A
	IMULQ   R9, DX              // DX = existingY * inv
	ADDQ    DX, AX              // AX = sum
	ADDQ    $128, AX            // AX += 128
	SHRQ    $8, AX              // AX >>= 8

	// Clamp to [0, 1023]
	CMPQ    AX, $1023
	JLE     aby10_store
	MOVQ    $1023, AX

aby10_store:
	MOVW    AX, (DI)

aby10_skip:
	ADDQ    $2, DI              // advance Y ptr (1 uint16)
	ADDQ    $4, SI              // advance RGBA ptr (4 bytes)
	DECQ    CX
	JNZ     aby10_loop

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
	MOVQ cbPlane+0(FP), DI      // Cb ptr
	MOVQ crPlane+8(FP), R14     // Cr ptr
	MOVQ rgba+16(FP), SI        // RGBA ptr
	MOVQ chromaCount+24(FP), CX

	TESTQ CX, CX
	JLE   abuv10_done

abuv10_loop:
	// Load left pixel RGBA (even column)
	MOVBLZX 3(SI), R8           // R8 = A
	TESTQ   R8, R8
	JZ      abuv10_skip

	// Load R, G, B (keep in R9, R10, R11 for reuse)
	MOVBLZX (SI), R9            // R9 = R
	MOVBLZX 1(SI), R10          // R10 = G
	MOVBLZX 2(SI), R11          // R11 = B

	// Cb8 = (-38*R - 74*G + 112*B + 128) >> 8 + 128
	// sum = 112*B + 128 - 38*R - 74*G
	MOVQ    R11, AX
	MOVQ    $112, BX
	IMULQ   BX, AX              // AX = 112*B
	ADDQ    $128, AX            // AX += 128
	MOVQ    R9, BX
	MOVQ    $38, DX
	IMULQ   DX, BX              // BX = 38*R
	SUBQ    BX, AX              // AX -= 38*R
	MOVQ    R10, BX
	MOVQ    $74, DX
	IMULQ   DX, BX              // BX = 74*G
	SUBQ    BX, AX              // AX -= 74*G
	SARQ    $8, AX              // AX >>= 8 (signed)
	ADDQ    $128, AX            // AX += 128
	SHLQ    $2, AX              // AX *= 4 = overlayCb
	MOVQ    AX, R12             // R12 = overlayCb

	// Cr8 = (112*R - 94*G - 18*B + 128) >> 8 + 128
	MOVQ    R9, AX
	MOVQ    $112, BX
	IMULQ   BX, AX              // AX = 112*R
	ADDQ    $128, AX            // AX += 128
	MOVQ    R10, BX
	MOVQ    $94, DX
	IMULQ   DX, BX              // BX = 94*G
	SUBQ    BX, AX              // AX -= 94*G
	MOVQ    R11, BX
	MOVQ    $18, DX
	IMULQ   DX, BX              // BX = 18*B
	SUBQ    BX, AX              // AX -= 18*B
	SARQ    $8, AX              // AX >>= 8 (signed)
	ADDQ    $128, AX            // AX += 128
	SHLQ    $2, AX              // AX *= 4 = overlayCr
	MOVQ    AX, R13             // R13 = overlayCr

	// inv = 255 - A
	MOVQ    $255, R15
	SUBQ    R8, R15             // R15 = inv

	// Alpha blend Cb
	MOVWLZX (DI), AX            // AX = existing Cb
	IMULQ   R8, R12             // R12 = overlayCb * A
	IMULQ   R15, AX             // AX = existingCb * inv
	ADDQ    AX, R12             // R12 = sum
	ADDQ    $128, R12           // R12 += 128
	SARQ    $8, R12             // R12 >>= 8 (signed, can be negative)

	// Clamp Cb to [0, 1023]
	TESTQ   R12, R12
	JL      abuv10_cb_zero
	CMPQ    R12, $1023
	JG      abuv10_cb_max
	JMP     abuv10_cb_store
abuv10_cb_zero:
	XORQ    R12, R12
	JMP     abuv10_cb_store
abuv10_cb_max:
	MOVQ    $1023, R12
abuv10_cb_store:
	MOVW    R12, (DI)

	// Alpha blend Cr
	MOVWLZX (R14), AX           // AX = existing Cr
	IMULQ   R8, R13             // R13 = overlayCr * A
	IMULQ   R15, AX             // AX = existingCr * inv
	ADDQ    AX, R13             // R13 = sum
	ADDQ    $128, R13           // R13 += 128
	SARQ    $8, R13             // R13 >>= 8 (signed)

	// Clamp Cr to [0, 1023]
	TESTQ   R13, R13
	JL      abuv10_cr_zero
	CMPQ    R13, $1023
	JG      abuv10_cr_max
	JMP     abuv10_cr_store
abuv10_cr_zero:
	XORQ    R13, R13
	JMP     abuv10_cr_store
abuv10_cr_max:
	MOVQ    $1023, R13
abuv10_cr_store:
	MOVW    R13, (R14)

abuv10_skip:
	ADDQ    $2, DI              // advance Cb ptr (1 uint16)
	ADDQ    $2, R14             // advance Cr ptr
	ADDQ    $8, SI              // advance RGBA ptr (skip 2 pixels = 8 bytes)
	DECQ    CX
	JNZ     abuv10_loop

abuv10_done:
	RET
