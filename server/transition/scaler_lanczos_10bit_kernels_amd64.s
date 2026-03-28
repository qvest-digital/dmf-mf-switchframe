#include "textflag.h"

// AMD64 SSE2 kernels for 10-bit Lanczos-3 scaler.
//
// lanczosHoriz10Row: reads uint16 LE source, outputs float32 intermediate (scalar)
// lanczosVert10Row: reads float32 intermediate, outputs uint16 LE clamped to [0, 1023]

// ============================================================================
// func lanczosHoriz10Row(dst []float32, src []byte, srcW int,
//                        offsets []int32, weights []float32, maxTaps int)
// ============================================================================
// Scalar loop — each output pixel reads from a different source offset,
// so vectorization across pixels provides no benefit.
//
// Stack: dst(0,8,16) src(24,32,40) srcW(48) offsets(56,64,72) weights(80,88,96) maxTaps(104)
// Total: 112 bytes
TEXT ·lanczosHoriz10Row(SB), NOSPLIT, $0-112
	MOVQ dst_base+0(FP), DI     // dst ptr (float32)
	MOVQ dst_len+8(FP), R8      // dstW
	MOVQ src_base+24(FP), SI    // src ptr (uint16 LE)
	MOVQ srcW+48(FP), R13       // srcW (number of uint16 samples)
	MOVQ offsets_base+56(FP), R9 // offsets ptr (int32)
	MOVQ weights_base+80(FP), R10 // weights ptr (float32)
	MOVQ maxTaps+104(FP), R11   // maxTaps

	TESTQ R8, R8
	JLE   lh10_done
	XORQ  CX, CX                // d = 0

lh10_pixel:
	// offset = offsets[d] (int32)
	MOVLQSX (R9)(CX*4), R12     // R12 = signed offset

	// wPtr = &weights[d * maxTaps]
	MOVQ    CX, AX
	IMULQ   R11, AX             // AX = d * maxTaps
	LEAQ    (R10)(AX*4), R14    // R14 = wPtr

	// Scalar accumulate: sum = 0.0
	XORPS   X0, X0              // X0 = accumulator
	XORQ    DX, DX              // t = 0

lh10_tap:
	CMPQ    DX, R11
	JGE     lh10_store

	// Load weight[t]
	MOVSS   (R14)(DX*4), X1     // X1 = weight

	// Compute sx = offset + t
	MOVQ    R12, AX
	ADDQ    DX, AX              // AX = sx

	// Clamp: if sx < 0, sx = 0; if sx >= srcW, sx = srcW - 1
	TESTQ   AX, AX
	JL      lh10_clamp_low
	CMPQ    AX, R13
	JGE     lh10_clamp_high
	JMP     lh10_load_sample

lh10_clamp_low:
	XORQ    AX, AX
	JMP     lh10_load_sample

lh10_clamp_high:
	MOVQ    R13, AX
	DECQ    AX

lh10_load_sample:
	// Load uint16 LE sample at src[sx*2]
	MOVWLZX (SI)(AX*2), BX     // BX = sample (zero-extended)
	CVTSL2SS BX, X2             // X2 = float32(sample)
	MULSS   X1, X2              // X2 = weight * sample
	ADDSS   X2, X0              // X0 += w * sample

	INCQ    DX
	JMP     lh10_tap

lh10_store:
	// Store dst[d] = X0
	MOVSS   X0, (DI)(CX*4)

	INCQ    CX
	CMPQ    CX, R8
	JLT     lh10_pixel

lh10_done:
	RET


// ============================================================================
// func lanczosVert10Row(dst []byte, temp []float32, tempStride int,
//                       startRow int32, weights []float32, maxTaps int)
// ============================================================================
// SSE2 vertical pass: 4-wide float32, outputs uint16 LE clamped to [0, 1023].
//
// Stack: dst(0,8,16) temp(24,32,40) tempStride(48) startRow(56) weights(64,72,80) maxTaps(88)
// Total: 96 bytes
TEXT ·lanczosVert10Row(SB), NOSPLIT, $0-96
	MOVQ  dst_base+0(FP), DI
	MOVQ  dst_len+8(FP), R8     // dstW * 2 (bytes)
	MOVQ  temp_base+24(FP), SI
	MOVQ  tempStride+48(FP), R9
	MOVLQSX startRow+56(FP), R10
	MOVQ  weights_base+64(FP), R11
	MOVQ  maxTaps+88(FP), R12

	// dstW = dst_len / 2
	SHRQ  $1, R8

	TESTQ R8, R8
	JLE   lv10_done

	// stride_bytes = tempStride * 4
	MOVQ  R9, R13
	SHLQ  $2, R13

	// base = temp + startRow * stride_bytes
	MOVQ  R10, AX
	IMULQ R13, AX
	ADDQ  AX, SI                 // SI now points to starting row in temp

	// Prepare SSE constants for clamping
	// X14 = 0.0 (zero)
	XORPS X14, X14
	// X15 = 1023.0f broadcast (clamp before +0.5 to ensure max output is 1023)
	MOVL  $0x447FC000, AX       // 1023.0f
	MOVD  AX, X15
	SHUFPS $0, X15, X15         // broadcast to all 4 lanes

	// Scalar rounding constant: 0.5f
	MOVL  $0x3F000000, AX       // 0.5f
	MOVD  AX, X13

	XORQ  CX, CX                // col = 0

	// Check if we can do 4-wide loop
	MOVQ  R8, AX
	SUBQ  $3, AX                // dstW - 3
	CMPQ  CX, AX
	JGE   lv10_scalar_setup

	// --- 4-wide SSE loop ---
lv10_sse4:
	XORPS X0, X0                // accumulator = 0.0
	XORQ  DX, DX                // t = 0

lv10_sse4_tap:
	CMPQ  DX, R12
	JGE   lv10_sse4_cvt

	// Load weight[t] and broadcast to all 4 lanes
	MOVSS (R11)(DX*4), X1
	SHUFPS $0, X1, X1           // X1 = {w, w, w, w}

	// Load 4 float32 from temp row: SI + t * stride_bytes + col * 4
	MOVQ  DX, AX
	IMULQ R13, AX               // AX = t * stride_bytes
	LEAQ  (SI)(AX*1), BX        // BX = row ptr
	MOVUPS (BX)(CX*4), X2       // X2 = temp[startRow+t][col..col+3]

	// X0 += X1 * X2
	MULPS X1, X2
	ADDPS X2, X0

	INCQ  DX
	JMP   lv10_sse4_tap

lv10_sse4_cvt:
	// Clamp to [0, 1023.0], add 0.5, truncate to int32 (matches Go: int32(acc+0.5) then clamp)
	MAXPS X14, X0                // max(val, 0)
	MINPS X15, X0                // min(val, 1023.0)

	// Add 0.5 for rounding (SSE2 CVTTPS2DQ truncates, so we round manually)
	// Actually, we can use CVTPS2DQ which rounds to nearest in default rounding mode
	// But to match Go reference exactly (which does int32(acc + 0.5)):
	// We need truncation after adding 0.5
	MOVAPS X0, X1
	MOVSS  X13, X2
	SHUFPS $0, X2, X2           // X2 = {0.5, 0.5, 0.5, 0.5}
	ADDPS  X2, X1               // X1 = val + 0.5
	CVTTPS2PL X1, X1            // X1 = int32(trunc(val + 0.5))

	// Pack int32 to int16: PACKSSLW (signed saturation, but values are [0, 1023])
	PACKSSLW X1, X1             // X1.low = 4 x int16

	// Store 4 uint16 (8 bytes)
	MOVQ  X1, (DI)(CX*2)

	ADDQ  $4, CX
	MOVQ  R8, AX
	SUBQ  $3, AX
	CMPQ  CX, AX
	JLT   lv10_sse4

lv10_scalar_setup:
	CMPQ  CX, R8
	JGE   lv10_done

lv10_scalar:
	XORPS X0, X0                // accumulator
	XORQ  DX, DX                // t = 0

lv10_scalar_tap:
	CMPQ  DX, R12
	JGE   lv10_scalar_store

	MOVSS (R11)(DX*4), X1       // weight[t]

	// Load temp[startRow+t][col]
	MOVQ  DX, AX
	IMULQ R13, AX
	LEAQ  (SI)(AX*1), BX
	MOVSS (BX)(CX*4), X2        // temp value

	MULSS X1, X2
	ADDSS X2, X0

	INCQ  DX
	JMP   lv10_scalar_tap

lv10_scalar_store:
	// result = int32(acc + 0.5)
	ADDSS X13, X0
	CVTTSS2SL X0, AX            // AX = int32(trunc(acc + 0.5))

	// Clamp to [0, 1023]
	TESTL AX, AX
	JL    lv10_clamp_zero
	CMPL  AX, $1023
	JG    lv10_clamp_max
	JMP   lv10_scalar_write

lv10_clamp_zero:
	XORL  AX, AX
	JMP   lv10_scalar_write

lv10_clamp_max:
	MOVL  $1023, AX

lv10_scalar_write:
	// Store uint16 LE
	MOVW  AX, (DI)(CX*2)

	INCQ  CX
	CMPQ  CX, R8
	JLT   lv10_scalar

lv10_done:
	RET
