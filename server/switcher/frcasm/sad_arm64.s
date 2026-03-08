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
