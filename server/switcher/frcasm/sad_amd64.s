#include "textflag.h"

// AMD64 SAD kernels for block-matching motion estimation and scene detection.
// AVX2 path (32 bytes/iter) with SSE2 fallback (16 bytes/iter).
//
// PSADBW computes the sum of absolute differences of packed unsigned byte
// integers in two XMM registers and produces two 64-bit partial sums.

// ============================================================================
// func SadBlock16x16(a, b *byte, aStride, bStride int) uint32
// ============================================================================
// Processes 16 rows, each 16 bytes wide. Uses PSADBW for SAD computation.
// Each PSADBW produces two 64-bit partial sums (low 8 bytes and high 8 bytes).
TEXT ·SadBlock16x16(SB), NOSPLIT, $0-36
	MOVQ a+0(FP), SI           // block A pointer
	MOVQ b+8(FP), DI           // block B pointer
	MOVQ aStride+16(FP), R8    // A stride
	MOVQ bStride+24(FP), R9    // B stride

	PXOR X2, X2                // accumulator = 0
	MOVQ $16, CX               // 16 rows

sad16x16_loop:
	MOVOU (SI), X0              // load 16 bytes from A row
	MOVOU (DI), X1              // load 16 bytes from B row
	PSADBW X1, X0               // X0 = [sad_hi64, sad_lo64]
	PADDQ X0, X2                // accumulate both partial sums
	ADDQ R8, SI                 // advance A by stride
	ADDQ R9, DI                 // advance B by stride
	DECQ CX
	JNZ  sad16x16_loop

	// Horizontal add: X2 has two 64-bit partial sums
	MOVHLPS X2, X0              // X0 = high qword of X2
	PADDQ   X0, X2              // X2[0] = total SAD
	MOVQ    X2, AX
	MOVL    AX, ret+32(FP)     // return as uint32
	RET


// ============================================================================
// func SadBlock16x16x4(cur *byte, refs [4]*byte, curStride, refStride int) [4]uint32
// ============================================================================
// Computes 4 SADs in one pass: loads each current-block row once, computes
// PSADBW against 4 reference rows, accumulates into 4 independent accumulators.
// This amortizes source-block memory loads across 4 computations.
//
// Stack layout (Go ABI0):
//   cur        +0(FP)     *byte
//   refs[0]    +8(FP)     *byte
//   refs[1]    +16(FP)    *byte
//   refs[2]    +24(FP)    *byte
//   refs[3]    +32(FP)    *byte
//   curStride  +40(FP)    int
//   refStride  +48(FP)    int
//   ret[0]     +56(FP)    uint32
//   ret[1]     +60(FP)    uint32
//   ret[2]     +64(FP)    uint32
//   ret[3]     +68(FP)    uint32
TEXT ·SadBlock16x16x4(SB), NOSPLIT, $0-72
	MOVQ cur+0(FP), SI         // current block pointer
	MOVQ refs_0+8(FP), R10     // refs[0]
	MOVQ refs_1+16(FP), R11    // refs[1]
	MOVQ refs_2+24(FP), R12    // refs[2]
	MOVQ refs_3+32(FP), R13    // refs[3]
	MOVQ curStride+40(FP), R8  // cur stride
	MOVQ refStride+48(FP), R9  // ref stride

	PXOR X6, X6                // accumulator 0
	PXOR X7, X7                // accumulator 1
	PXOR X8, X8                // accumulator 2
	PXOR X9, X9                // accumulator 3
	MOVQ $16, CX               // 16 rows

sadx4_loop:
	MOVOU (SI), X0              // load current row (shared across 4 refs)

	MOVOU (R10), X1             // load ref0 row
	MOVOU X0, X5                // copy cur (PSADBW is destructive)
	PSADBW X1, X5
	PADDQ X5, X6                // acc0 += sad(cur, ref0)

	MOVOU (R11), X2             // load ref1 row
	MOVOU X0, X5
	PSADBW X2, X5
	PADDQ X5, X7                // acc1 += sad(cur, ref1)

	MOVOU (R12), X3             // load ref2 row
	MOVOU X0, X5
	PSADBW X3, X5
	PADDQ X5, X8                // acc2 += sad(cur, ref2)

	MOVOU (R13), X4             // load ref3 row
	PSADBW X4, X0               // can use X0 directly (last ref)
	PADDQ X0, X9                // acc3 += sad(cur, ref3)

	ADDQ R8, SI                 // advance cur
	ADDQ R9, R10                // advance ref0
	ADDQ R9, R11                // advance ref1
	ADDQ R9, R12                // advance ref2
	ADDQ R9, R13                // advance ref3
	DECQ CX
	JNZ  sadx4_loop

	// Horizontal reduce each accumulator: 2 qwords → 1 dword
	MOVHLPS X6, X0
	PADDQ   X0, X6
	MOVQ    X6, AX
	MOVL    AX, ret_0+56(FP)    // ret[0]

	MOVHLPS X7, X0
	PADDQ   X0, X7
	MOVQ    X7, AX
	MOVL    AX, ret_1+60(FP)    // ret[1]

	MOVHLPS X8, X0
	PADDQ   X0, X8
	MOVQ    X8, AX
	MOVL    AX, ret_2+64(FP)    // ret[2]

	MOVHLPS X9, X0
	PADDQ   X0, X9
	MOVQ    X9, AX
	MOVL    AX, ret_3+68(FP)    // ret[3]
	RET


// ============================================================================
// func SadRow(a, b *byte, n int) uint64
// ============================================================================
// Computes sum(|a[i] - b[i]|) across n bytes.
// AVX2 path: VPSADBW at 32 bytes/iter. SSE2: PSADBW at 16 bytes/iter.
// Scalar tail for remaining bytes.
TEXT ·SadRow(SB), NOSPLIT, $0-32
	MOVQ a+0(FP), SI           // pointer to a
	MOVQ b+8(FP), DI           // pointer to b
	MOVQ n+16(FP), CX          // byte count

	XORQ AX, AX                // scalar accumulator = 0

	TESTQ CX, CX
	JLE   sadrow_done

	// Check AVX2 availability
	CMPB ·avx2Available(SB), $1
	JE   sadrow_avx2

	// --- SSE2 path (16 bytes/iteration) ---
	PXOR X2, X2                // SIMD accumulator = 0

	CMPQ CX, $16
	JLT  sadrow_sse2_tail

sadrow_sse2_loop:
	MOVOU  (SI), X0             // load 16 bytes from a
	MOVOU  (DI), X1             // load 16 bytes from b
	PSADBW X1, X0               // X0 = two 64-bit partial SAD sums
	PADDQ  X0, X2               // accumulate
	ADDQ   $16, SI
	ADDQ   $16, DI
	SUBQ   $16, CX
	CMPQ   CX, $16
	JGE    sadrow_sse2_loop

sadrow_sse2_tail:
	// Reduce SIMD accumulator to scalar
	MOVHLPS X2, X0
	PADDQ   X0, X2
	MOVQ    X2, AX              // AX = SIMD total

	// Scalar tail for remaining < 16 bytes
	TESTQ CX, CX
	JZ    sadrow_done

sadrow_scalar_loop:
	MOVBLZX (SI), R8
	MOVBLZX (DI), R9
	SUBQ    R9, R8
	// Branchless absolute value: mask = R8 >> 63, abs = (R8 ^ mask) - mask
	MOVQ    R8, R10
	SARQ    $63, R10            // R10 = sign mask (0 or -1)
	XORQ    R10, R8
	SUBQ    R10, R8             // R8 = |a[i] - b[i]|
	ADDQ    R8, AX
	INCQ    SI
	INCQ    DI
	DECQ    CX
	JNZ     sadrow_scalar_loop
	JMP     sadrow_done

sadrow_avx2:
	// --- AVX2 path (32 bytes/iteration) ---
	VPXOR Y2, Y2, Y2           // SIMD accumulator = 0

	CMPQ CX, $32
	JLT  sadrow_avx2_tail

sadrow_avx2_loop:
	VMOVDQU (SI), Y0            // load 32 bytes from a
	VMOVDQU (DI), Y1            // load 32 bytes from b
	VPSADBW Y1, Y0, Y0          // Y0 = four 64-bit partial SAD sums
	VPADDQ  Y0, Y2, Y2          // accumulate
	ADDQ    $32, SI
	ADDQ    $32, DI
	SUBQ    $32, CX
	CMPQ    CX, $32
	JGE     sadrow_avx2_loop

sadrow_avx2_tail:
	// Reduce 256-bit accumulator: extract high 128 bits
	VEXTRACTI128 $1, Y2, X0
	VPADDQ       X0, X2, X2    // X2 = 128-bit partial sum
	VZEROUPPER

	// Reduce 128-bit to 64-bit
	MOVHLPS X2, X0
	PADDQ   X0, X2
	MOVQ    X2, AX              // AX = SIMD total

	// Fall through to SSE2/scalar for remaining bytes
	TESTQ CX, CX
	JZ    sadrow_done

	CMPQ CX, $16
	JLT  sadrow_scalar_loop

	// SSE2 path for 16-byte chunks after AVX2
	PXOR X2, X2

sadrow_avx2_sse2_tail:
	MOVOU  (SI), X0
	MOVOU  (DI), X1
	PSADBW X1, X0
	PADDQ  X0, X2
	ADDQ   $16, SI
	ADDQ   $16, DI
	SUBQ   $16, CX
	CMPQ   CX, $16
	JGE    sadrow_avx2_sse2_tail

	// Reduce SSE2 accumulator and add to scalar total
	MOVHLPS X2, X0
	PADDQ   X0, X2
	MOVQ    X2, R8
	ADDQ    R8, AX

	TESTQ CX, CX
	JZ    sadrow_done
	JMP   sadrow_scalar_loop

sadrow_done:
	MOVQ AX, ret+24(FP)
	RET


// ============================================================================
// func SadBlock16x16HpelH(cur, ref *byte, curStride, refStride int) uint32
// ============================================================================
// Horizontal half-pel SAD: interp = PAVGB(ref[x], ref[x+1]), then PSADBW vs cur.
// ref must have 17 valid bytes per row (16 + 1 for the horizontal neighbor).
TEXT ·SadBlock16x16HpelH(SB), NOSPLIT, $0-36
	MOVQ cur+0(FP), SI            // current block pointer
	MOVQ ref+8(FP), DI            // reference block pointer
	MOVQ curStride+16(FP), R8     // current stride
	MOVQ refStride+24(FP), R9     // reference stride

	PXOR X3, X3                   // accumulator = 0
	MOVQ $16, CX                  // 16 rows

hpelh_loop:
	MOVOU (SI), X0                // load 16 bytes of current row
	MOVOU (DI), X1                // load ref[x..x+15]
	MOVOU 1(DI), X2               // load ref[x+1..x+16]
	PAVGB X2, X1                  // X1 = (ref[x] + ref[x+1] + 1) >> 1
	PSADBW X0, X1                 // X1 = SAD(cur, interp)
	PADDQ X1, X3                  // accumulate
	ADDQ R8, SI                   // advance cur
	ADDQ R9, DI                   // advance ref
	DECQ CX
	JNZ  hpelh_loop

	// Reduce two 64-bit partial sums
	MOVHLPS X3, X0
	PADDQ   X0, X3
	MOVQ    X3, AX
	MOVL    AX, ret+32(FP)
	RET


// ============================================================================
// func SadBlock16x16HpelV(cur, ref *byte, curStride, refStride int) uint32
// ============================================================================
// Vertical half-pel SAD: interp = PAVGB(ref[y], ref[y+stride]), then PSADBW vs cur.
// ref must have 17 valid rows (16 + 1 for the vertical neighbor).
TEXT ·SadBlock16x16HpelV(SB), NOSPLIT, $0-36
	MOVQ cur+0(FP), SI            // current block pointer
	MOVQ ref+8(FP), DI            // reference block pointer
	MOVQ curStride+16(FP), R8     // current stride
	MOVQ refStride+24(FP), R9     // reference stride

	PXOR X3, X3                   // accumulator = 0
	MOVQ $16, CX                  // 16 rows

	// R10 = ref + refStride (next row pointer)
	MOVQ DI, R10
	ADDQ R9, R10

hpelv_loop:
	MOVOU (SI), X0                // load 16 bytes of current row
	MOVOU (DI), X1                // load ref row Y
	MOVOU (R10), X2               // load ref row Y+1
	PAVGB X2, X1                  // X1 = (ref[y] + ref[y+stride] + 1) >> 1
	PSADBW X0, X1                 // X1 = SAD(cur, interp)
	PADDQ X1, X3                  // accumulate
	ADDQ R8, SI                   // advance cur
	ADDQ R9, DI                   // advance ref row Y
	ADDQ R9, R10                  // advance ref row Y+1
	DECQ CX
	JNZ  hpelv_loop

	// Reduce two 64-bit partial sums
	MOVHLPS X3, X0
	PADDQ   X0, X3
	MOVQ    X3, AX
	MOVL    AX, ret+32(FP)
	RET


// ============================================================================
// func SadBlock16x16HpelD(cur, ref *byte, curStride, refStride int) uint32
// ============================================================================
// Diagonal half-pel SAD: interp = PAVGB(PAVGB(ref[0],ref[1]), PAVGB(ref[stride],ref[stride+1])).
// Uses cascaded PAVGB which may differ by ±1 LSB from (a+b+c+d+2)>>2.
// ref must have 17 valid bytes per row and 17 valid rows.
TEXT ·SadBlock16x16HpelD(SB), NOSPLIT, $0-36
	MOVQ cur+0(FP), SI            // current block pointer
	MOVQ ref+8(FP), DI            // reference block pointer
	MOVQ curStride+16(FP), R8     // current stride
	MOVQ refStride+24(FP), R9     // reference stride

	PXOR X5, X5                   // accumulator = 0
	MOVQ $16, CX                  // 16 rows

	// R10 = ref + refStride (next row pointer)
	MOVQ DI, R10
	ADDQ R9, R10

hpeld_loop:
	MOVOU (SI), X0                // load 16 bytes of current row
	// Top row: avg(ref[x], ref[x+1])
	MOVOU (DI), X1                // ref[y][x..x+15]
	MOVOU 1(DI), X2               // ref[y][x+1..x+16]
	PAVGB X2, X1                  // X1 = avg(top_left, top_right)
	// Bottom row: avg(ref[x+stride], ref[x+stride+1])
	MOVOU (R10), X3               // ref[y+1][x..x+15]
	MOVOU 1(R10), X4              // ref[y+1][x+1..x+16]
	PAVGB X4, X3                  // X3 = avg(bot_left, bot_right)
	// Diagonal average: avg(top_avg, bot_avg)
	PAVGB X3, X1                  // X1 = cascaded diagonal interp
	PSADBW X0, X1                 // X1 = SAD(cur, interp)
	PADDQ X1, X5                  // accumulate
	ADDQ R8, SI                   // advance cur
	ADDQ R9, DI                   // advance ref row Y
	ADDQ R9, R10                  // advance ref row Y+1
	DECQ CX
	JNZ  hpeld_loop

	// Reduce two 64-bit partial sums
	MOVHLPS X5, X0
	PADDQ   X0, X5
	MOVQ    X5, AX
	MOVL    AX, ret+32(FP)
	RET
