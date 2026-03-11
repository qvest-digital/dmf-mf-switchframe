#include "textflag.h"

// ARM64 NEON SAD kernels for block-matching motion estimation and scene detection.
//
// The Go ARM64 assembler lacks several NEON instructions, so we encode them
// via WORD macros. Register encoding uses 5-bit fields (V0=0 .. V31=31).

// --- SAD instruction macros ---

// UABD Vd.16B, Vn.16B, Vm.16B — unsigned absolute difference of 16 bytes
// Encoding: 0x6E20_7400 | Rm<<16 | Rn<<5 | Rd
#define UABD_16B(Vd, Vn, Vm) WORD $(0x6E207400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))

// UADALP Vd.8H, Vn.16B — unsigned add and accumulate long pairwise
// Adds pairs of bytes in Vn, accumulates into 8 halfwords in Vd.
// Encoding: 0x6E206800 | Rn<<5 | Rd
#define UADALP_8H(Vd, Vn) WORD $(0x6E206800 | ((Vn)<<5) | (Vd))

// UADDLP Vd.8H, Vn.16B — unsigned add long pairwise (non-accumulating)
// Encoding: 0x6E202800 | Rn<<5 | Rd
#define UADDLP_8H(Vd, Vn) WORD $(0x6E202800 | ((Vn)<<5) | (Vd))

// UADDLV Hd, Vn.8H — unsigned add across long vector (sum 8 halfwords → 1 word in Sd)
// Encoding: 0x6E703800 | Rn<<5 | Rd
#define UADDLV_H8(Vd, Vn) WORD $(0x6E703800 | ((Vn)<<5) | (Vd))

// UADDLV Sd, Vn.4S — unsigned add across long vector (sum 4 words → 1 doubleword in Dd)
// Encoding: 0x6EB03800 | Rn<<5 | Rd
#define UADDLV_S4(Vd, Vn) WORD $(0x6EB03800 | ((Vn)<<5) | (Vd))

// URHADD Vd.16B, Vn.16B, Vm.16B — unsigned rounding halving add
// Computes (a + b + 1) >> 1 per byte (same semantics as x86 PAVGB).
// Encoding: 0x6E201400 | Rm<<16 | Rn<<5 | Rd
#define URHADD_16B(Vd, Vn, Vm) WORD $(0x6E201400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))


// ============================================================================
// func SadBlock16x16(a, b *byte, aStride, bStride int) uint32
// ============================================================================
// Processes 16 rows, each 16 bytes wide.
// Uses UABD for absolute difference, UADALP to accumulate pairwise into
// a halfword accumulator (V3.8H). After 16 rows, reduces with UADDLV.
//
// Max per-row: 16 * 255 = 4080 per pair → UADALP accumulates 8 pairs per row.
// After 16 rows of UADALP: max per halfword = 16 * 2 * 255 = 8160, fits uint16.
TEXT ·SadBlock16x16(SB), NOSPLIT, $0-36
	MOVD a+0(FP), R0            // block A pointer
	MOVD b+8(FP), R1            // block B pointer
	MOVD aStride+16(FP), R2     // A stride
	MOVD bStride+24(FP), R3     // B stride

	// Zero the halfword accumulator V3
	VEOR V3.B16, V3.B16, V3.B16

	MOVD $16, R4                // row counter

sad16x16_loop:
	VLD1 (R0), [V0.B16]        // load 16 bytes from A row
	VLD1 (R1), [V1.B16]        // load 16 bytes from B row
	UABD_16B(2, 0, 1)          // V2.16B = |A - B| per byte
	UADALP_8H(3, 2)            // V3.8H += pairwise_add(V2.16B)
	ADD  R2, R0                 // advance A by stride
	ADD  R3, R1                 // advance B by stride
	SUB  $1, R4, R4
	CBNZ R4, sad16x16_loop

	// Reduce V3.8H → scalar: sum all 8 halfwords into a 32-bit result
	UADDLV_H8(3, 3)            // V3.S[0] = sum of 8 halfwords
	VMOV V3.S[0], R0
	MOVW R0, ret+32(FP)
	RET


// ============================================================================
// func SadBlock16x16x4(cur *byte, refs [4]*byte, curStride, refStride int) [4]uint32
// ============================================================================
// Computes 4 SADs in one pass: loads each current-block row once, computes
// UABD against 4 reference rows, accumulates into 4 independent halfword
// accumulators (V4-V7.8H). Reduces each with UADDLV at the end.
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
//
// Registers:
//   R0 = cur, R5-R8 = ref0-ref3, R2 = curStride, R3 = refStride, R4 = counter
//   V0 = cur row, V1 = ref row (temp), V2 = absdiff (temp)
//   V4-V7.8H = accumulators for refs 0-3
TEXT ·SadBlock16x16x4(SB), NOSPLIT, $0-72
	MOVD cur+0(FP), R0          // current block pointer
	MOVD refs_0+8(FP), R5       // refs[0]
	MOVD refs_1+16(FP), R6      // refs[1]
	MOVD refs_2+24(FP), R7      // refs[2]
	MOVD refs_3+32(FP), R8      // refs[3]
	MOVD curStride+40(FP), R2   // cur stride
	MOVD refStride+48(FP), R3   // ref stride

	// Zero accumulators
	VEOR V4.B16, V4.B16, V4.B16 // acc0
	VEOR V5.B16, V5.B16, V5.B16 // acc1
	VEOR V6.B16, V6.B16, V6.B16 // acc2
	VEOR V7.B16, V7.B16, V7.B16 // acc3

	MOVD $16, R4                // row counter

sadx4_neon_loop:
	VLD1 (R0), [V0.B16]        // load current row (shared)

	VLD1 (R5), [V1.B16]        // load ref0 row
	UABD_16B(2, 0, 1)          // V2 = |cur - ref0|
	UADALP_8H(4, 2)            // V4.8H += pairwise_add(V2)

	VLD1 (R6), [V1.B16]        // load ref1 row
	UABD_16B(2, 0, 1)          // V2 = |cur - ref1|
	UADALP_8H(5, 2)            // V5.8H += pairwise_add(V2)

	VLD1 (R7), [V1.B16]        // load ref2 row
	UABD_16B(2, 0, 1)          // V2 = |cur - ref2|
	UADALP_8H(6, 2)            // V6.8H += pairwise_add(V2)

	VLD1 (R8), [V1.B16]        // load ref3 row
	UABD_16B(2, 0, 1)          // V2 = |cur - ref3|
	UADALP_8H(7, 2)            // V7.8H += pairwise_add(V2)

	ADD  R2, R0                 // advance cur
	ADD  R3, R5                 // advance ref0
	ADD  R3, R6                 // advance ref1
	ADD  R3, R7                 // advance ref2
	ADD  R3, R8                 // advance ref3
	SUB  $1, R4, R4
	CBNZ R4, sadx4_neon_loop

	// Reduce each accumulator: sum 8 halfwords → 32-bit result
	UADDLV_H8(4, 4)
	VMOV V4.S[0], R0
	MOVW R0, ret_0+56(FP)       // ret[0]

	UADDLV_H8(5, 5)
	VMOV V5.S[0], R0
	MOVW R0, ret_1+60(FP)       // ret[1]

	UADDLV_H8(6, 6)
	VMOV V6.S[0], R0
	MOVW R0, ret_2+64(FP)       // ret[2]

	UADDLV_H8(7, 7)
	VMOV V7.S[0], R0
	MOVW R0, ret_3+68(FP)       // ret[3]
	RET


// ============================================================================
// func SadRow(a, b *byte, n int) uint64
// ============================================================================
// Computes sum(|a[i] - b[i]|) across n bytes.
// NEON path: 16 bytes/iteration using UABD + pairwise accumulate.
// Uses 32-bit accumulators (V3.4S) to avoid overflow on large rows.
// Scalar tail for remaining bytes.
TEXT ·SadRow(SB), NOSPLIT, $0-32
	MOVD a+0(FP), R0            // pointer to a
	MOVD b+8(FP), R1            // pointer to b
	MOVD n+16(FP), R2           // byte count

	MOVD ZR, R5                 // scalar accumulator = 0

	CMP  $0, R2
	BLE  sadrow_done

	// Zero the NEON accumulators
	// We use a two-level accumulate: UADALP into V3.8H, then periodically
	// widen V3.8H into V4.4S to avoid uint16 overflow for large rows.
	VEOR V3.B16, V3.B16, V3.B16 // V3.8H = halfword accumulator
	VEOR V4.B16, V4.B16, V4.B16 // V4.4S = word accumulator
	MOVD $0, R6                  // iteration counter for periodic flush

	CMP  $16, R2
	BLT  sadrow_flush

sadrow_loop16:
	VLD1.P 16(R0), [V0.B16]    // load 16 bytes from a
	VLD1.P 16(R1), [V1.B16]    // load 16 bytes from b
	UABD_16B(2, 0, 1)          // V2.16B = |a - b| per byte
	UADALP_8H(3, 2)            // V3.8H += pairwise_add(V2.16B)
	ADD  $1, R6, R6

	// Flush V3.8H into V4.4S every 128 iterations to avoid uint16 overflow.
	// Max per-halfword per iteration = 2*255 = 510. 128*510 = 65280 ≤ 65535.
	CMP  $128, R6
	BLT  sadrow_no_flush
	// Widen-accumulate: V4.4S += pairwise_add(V3.8H)
	// Use UADDLP to get V3.8H → V5.4S, then VADD V5.4S into V4.4S
	UADDLP_8H(5, 3)            // V5.4S ← pairwise add V3.8H (reuse macro — actually need .4S version)
	// Actually we need UADDLP Vd.4S, Vn.8H which is a different encoding.
	// Let's just reduce via UADDLV and add to scalar, then reset V3.
	UADDLV_H8(5, 3)            // V5.S[0] = sum of V3.8H
	VMOV V5.S[0], R7
	ADD  R7, R5, R5             // add to scalar total
	VEOR V3.B16, V3.B16, V3.B16 // reset halfword accumulator
	MOVD $0, R6

sadrow_no_flush:
	SUB  $16, R2, R2
	CMP  $16, R2
	BGE  sadrow_loop16

sadrow_flush:
	// Final flush of V3.8H into scalar
	UADDLV_H8(5, 3)            // V5.S[0] = sum of remaining halfwords
	VMOV V5.S[0], R7
	ADD  R7, R5, R5

	// Scalar tail for remaining < 16 bytes
	CBZ  R2, sadrow_done

sadrow_tail_loop:
	MOVBU (R0), R3
	MOVBU (R1), R4
	SUB   R4, R3, R3

	// Branchless absolute value
	ASR   $63, R3, R7           // R7 = sign mask
	EOR   R7, R3, R3
	SUB   R7, R3, R3            // R3 = |a[i] - b[i]|
	ADD   R3, R5, R5

	ADD   $1, R0
	ADD   $1, R1
	SUB   $1, R2, R2
	CBNZ  R2, sadrow_tail_loop

sadrow_done:
	MOVD R5, ret+24(FP)
	RET


// ============================================================================
// func SadBlock16x16HpelH(cur, ref *byte, curStride, refStride int) uint32
// ============================================================================
// Horizontal half-pel SAD: interp = URHADD(ref[x], ref[x+1]), then UABD vs cur.
// ref must have 17 valid bytes per row (16 + 1 for the horizontal neighbor).
TEXT ·SadBlock16x16HpelH(SB), NOSPLIT, $0-36
	MOVD cur+0(FP), R0            // current block pointer
	MOVD ref+8(FP), R1            // reference block pointer
	MOVD curStride+16(FP), R2     // current stride
	MOVD refStride+24(FP), R3     // reference stride

	VEOR V3.B16, V3.B16, V3.B16  // halfword accumulator = 0
	MOVD $16, R4                  // row counter

hpelh_neon_loop:
	VLD1 (R0), [V0.B16]          // load 16 bytes of cur row
	VLD1 (R1), [V1.B16]          // load ref[x..x+15]
	ADD  $1, R1, R5               // R5 = ref + 1
	VLD1 (R5), [V2.B16]          // load ref[x+1..x+16]
	URHADD_16B(1, 1, 2)          // V1 = (ref[x] + ref[x+1] + 1) >> 1
	UABD_16B(2, 0, 1)            // V2 = |cur - interp|
	UADALP_8H(3, 2)              // V3.8H += pairwise(V2)
	ADD  R2, R0                   // advance cur
	ADD  R3, R1                   // advance ref
	SUB  $1, R4, R4
	CBNZ R4, hpelh_neon_loop

	UADDLV_H8(3, 3)              // reduce to scalar
	VMOV V3.S[0], R0
	MOVW R0, ret+32(FP)
	RET


// ============================================================================
// func SadBlock16x16HpelV(cur, ref *byte, curStride, refStride int) uint32
// ============================================================================
// Vertical half-pel SAD: interp = URHADD(ref[y], ref[y+stride]), then UABD vs cur.
// ref must have 17 valid rows (16 + 1 for the vertical neighbor).
TEXT ·SadBlock16x16HpelV(SB), NOSPLIT, $0-36
	MOVD cur+0(FP), R0            // current block pointer
	MOVD ref+8(FP), R1            // reference block pointer
	MOVD curStride+16(FP), R2     // current stride
	MOVD refStride+24(FP), R3     // reference stride

	VEOR V3.B16, V3.B16, V3.B16  // halfword accumulator = 0
	MOVD $16, R4                  // row counter

	// R5 = ref + refStride (next row pointer)
	ADD  R3, R1, R5

hpelv_neon_loop:
	VLD1 (R0), [V0.B16]          // load 16 bytes of cur row
	VLD1 (R1), [V1.B16]          // load ref row Y
	VLD1 (R5), [V2.B16]          // load ref row Y+1
	URHADD_16B(1, 1, 2)          // V1 = (ref[y] + ref[y+stride] + 1) >> 1
	UABD_16B(2, 0, 1)            // V2 = |cur - interp|
	UADALP_8H(3, 2)              // V3.8H += pairwise(V2)
	ADD  R2, R0                   // advance cur
	ADD  R3, R1                   // advance ref row Y
	ADD  R3, R5                   // advance ref row Y+1
	SUB  $1, R4, R4
	CBNZ R4, hpelv_neon_loop

	UADDLV_H8(3, 3)              // reduce to scalar
	VMOV V3.S[0], R0
	MOVW R0, ret+32(FP)
	RET


// ============================================================================
// func SadBlock16x16HpelD(cur, ref *byte, curStride, refStride int) uint32
// ============================================================================
// Diagonal half-pel SAD: interp = URHADD(URHADD(ref[0],ref[1]), URHADD(ref[s],ref[s+1])).
// Uses cascaded URHADD which may differ by ±1 LSB from (a+b+c+d+2)>>2.
// ref must have 17 valid bytes per row and 17 valid rows.
TEXT ·SadBlock16x16HpelD(SB), NOSPLIT, $0-36
	MOVD cur+0(FP), R0            // current block pointer
	MOVD ref+8(FP), R1            // reference block pointer
	MOVD curStride+16(FP), R2     // current stride
	MOVD refStride+24(FP), R3     // reference stride

	VEOR V6.B16, V6.B16, V6.B16  // halfword accumulator = 0
	MOVD $16, R4                  // row counter

	// R5 = ref + refStride (next row pointer)
	ADD  R3, R1, R5

hpeld_neon_loop:
	VLD1 (R0), [V0.B16]          // load 16 bytes of cur row
	// Top row horizontal average
	VLD1 (R1), [V1.B16]          // ref[y][x..x+15]
	ADD  $1, R1, R6               // R6 = ref row Y + 1
	VLD1 (R6), [V2.B16]          // ref[y][x+1..x+16]
	URHADD_16B(1, 1, 2)          // V1 = avg(top_left, top_right)
	// Bottom row horizontal average
	VLD1 (R5), [V3.B16]          // ref[y+1][x..x+15]
	ADD  $1, R5, R6               // R6 = ref row Y+1 + 1
	VLD1 (R6), [V4.B16]          // ref[y+1][x+1..x+16]
	URHADD_16B(3, 3, 4)          // V3 = avg(bot_left, bot_right)
	// Diagonal average
	URHADD_16B(1, 1, 3)          // V1 = avg(top_avg, bot_avg)
	UABD_16B(2, 0, 1)            // V2 = |cur - interp|
	UADALP_8H(6, 2)              // V6.8H += pairwise(V2)
	ADD  R2, R0                   // advance cur
	ADD  R3, R1                   // advance ref row Y
	ADD  R3, R5                   // advance ref row Y+1
	SUB  $1, R4, R4
	CBNZ R4, hpeld_neon_loop

	UADDLV_H8(6, 6)              // reduce to scalar
	VMOV V6.S[0], R0
	MOVW R0, ret+32(FP)
	RET
