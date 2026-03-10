#include "textflag.h"

// ARM64 NEON kernel for mask-based YUV plane blending.
//
// func blendMaskY(bg *byte, fill *byte, mask *byte, n int)
//
// NEON path processes 16 bytes per iteration:
//   w.8H = mask + (mask >> 7)           // 0-255 → 0-256
//   inv.8H = 256 - w
//   bg[i] = (bg[i]*inv + fill[i]*w + 128) >> 8
//
// Max value of bg*inv + fill*w + 128 = 255*256 + 128 = 65408 < 65535,
// so all arithmetic fits in uint16.
//
// Registers:
//   R0 = bg ptr, R1 = fill ptr, R2 = mask ptr, R3 = n (loop counter)
//
// NEON constants:
//   V30.8H = 256, V31.8H = 128 (rounding bias)

// --- NEON instruction macros ---
#define USHLL_8H(Vd, Vn) WORD $(0x2F08A400 | ((Vn)<<5) | (Vd))
#define USHLL2_8H(Vd, Vn) WORD $(0x6F08A400 | ((Vn)<<5) | (Vd))
#define VMUL_8H(Vd, Vn, Vm) WORD $(0x4E609C00 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
#define VMLA_8H(Vd, Vn, Vm) WORD $(0x4E609400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
#define VADD_8H(Vd, Vn, Vm) WORD $(0x4E608400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
#define VSUB_8H(Vd, Vn, Vm) WORD $(0x6E608400 | ((Vm)<<16) | ((Vn)<<5) | (Vd))
#define VUSHR_8H(Vd, Vn, shift) WORD $(0x6F000400 | ((32-(shift))<<16) | ((Vn)<<5) | (Vd))
#define VDUP_8H(Vd, Rn) WORD $(0x4E060C00 | ((Rn)<<5) | (Vd))
#define VXTN_8B(Vd, Vn) WORD $(0x0E212800 | ((Vn)<<5) | (Vd))
#define VXTN2_16B(Vd, Vn) WORD $(0x4E212800 | ((Vn)<<5) | (Vd))
// LD1 {Vt.16B}, [Xn] — load 16 bytes, no post-increment
#define VLD1_16B(Vt, Rn) WORD $(0x4C407C00 | ((Rn)<<5) | (Vt))
// ST1 {Vt.16B}, [Xn] — store 16 bytes, no post-increment
#define VST1_16B(Vt, Rn) WORD $(0x4C007C00 | ((Rn)<<5) | (Vt))

TEXT ·blendMaskY(SB), NOSPLIT, $0-32
	MOVD bg+0(FP), R0
	MOVD fill+8(FP), R1
	MOVD mask+16(FP), R2
	MOVD n+24(FP), R3

	CMP  $0, R3
	BLE  bmy_done

	CMP  $16, R3
	BLT  bmy_scalar

	// Set up NEON constants
	MOVD $256, R4
	VDUP_8H(30, 4)            // V30.8H = 256
	MOVD $128, R4
	VDUP_8H(31, 4)            // V31.8H = 128

bmy_neon16:
	// Load 16 bytes each of bg, fill, mask
	VLD1_16B(0, 0)            // V0 = bg[0..15]
	VLD1_16B(1, 1)            // V1 = fill[0..15]
	VLD1_16B(2, 2)            // V2 = mask[0..15]

	// === Lower 8 bytes ===
	// Widen to uint16
	USHLL_8H(3, 0)            // V3.8H = bg_lo
	USHLL_8H(4, 1)            // V4.8H = fill_lo
	USHLL_8H(5, 2)            // V5.8H = mask_lo

	// w = mask + (mask >> 7)
	VUSHR_8H(6, 5, 7)         // V6 = mask >> 7
	VADD_8H(5, 5, 6)          // V5 = w

	// inv = 256 - w
	VSUB_8H(6, 30, 5)         // V6 = inv = 256 - w

	// result = (bg * inv + fill * w + 128) >> 8
	VMUL_8H(3, 3, 6)          // V3 = bg * inv
	VMLA_8H(3, 4, 5)          // V3 += fill * w
	VADD_8H(3, 3, 31)         // V3 += 128
	VUSHR_8H(3, 3, 8)         // V3 >> 8 → result_lo

	// === Upper 8 bytes ===
	USHLL2_8H(7, 0)           // V7.8H = bg_hi
	USHLL2_8H(8, 1)           // V8.8H = fill_hi
	USHLL2_8H(9, 2)           // V9.8H = mask_hi

	VUSHR_8H(10, 9, 7)        // V10 = mask >> 7
	VADD_8H(9, 9, 10)         // V9 = w_hi

	VSUB_8H(10, 30, 9)        // V10 = inv_hi

	VMUL_8H(7, 7, 10)         // V7 = bg * inv
	VMLA_8H(7, 8, 9)          // V7 += fill * w
	VADD_8H(7, 7, 31)         // V7 += 128
	VUSHR_8H(7, 7, 8)         // V7 >> 8 → result_hi

	// Narrow: 8H → 8B + 8B → 16B
	VXTN_8B(3, 3)             // V3.8B from V3.8H (lower result)
	VXTN2_16B(3, 7)           // V3.16B upper 8 from V7.8H

	// Store 16 result bytes
	VST1_16B(3, 0)            // store to bg

	ADD  $16, R0, R0
	ADD  $16, R1, R1
	ADD  $16, R2, R2
	SUB  $16, R3, R3
	CMP  $16, R3
	BGE  bmy_neon16

bmy_scalar:
	CMP  $0, R3
	BLE  bmy_done

bmy_loop:
	// w = mask + (mask >> 7)
	MOVBU (R2), R4             // R4 = mask[i]
	LSR   $7, R4, R5           // R5 = mask >> 7
	ADD   R5, R4, R4           // R4 = w

	CBZ   R4, bmy_skip         // skip if transparent

	// inv = 256 - w
	MOVD  $256, R5
	SUB   R4, R5, R5           // R5 = inv

	// bg[i] = (bg[i]*inv + fill[i]*w + 128) >> 8
	MOVBU (R0), R6             // R6 = bg[i]
	MOVBU (R1), R7             // R7 = fill[i]
	MUL   R5, R6, R6           // R6 = bg * inv
	MUL   R4, R7, R7           // R7 = fill * w
	ADD   R7, R6, R6
	ADD   $128, R6, R6
	LSR   $8, R6, R6
	MOVB  R6, (R0)             // store result

bmy_skip:
	ADD   $1, R0, R0
	ADD   $1, R1, R1
	ADD   $1, R2, R2
	SUB   $1, R3, R3
	CBNZ  R3, bmy_loop

bmy_done:
	RET
