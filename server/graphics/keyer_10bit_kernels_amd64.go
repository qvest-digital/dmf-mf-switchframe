//go:build amd64

package graphics

import "golang.org/x/sys/cpu"

// avx2Available10 is set at init time if the CPU supports AVX2.
// Referenced from assembly via ·avx2Available10(SB).
var avx2Available10 = cpu.X86.HasAVX2 //nolint:unused // used in keyer_10bit_kernels_amd64.s

// lumaKey10Row generates a luma key mask for count uint16 LE Y samples.
// For each sample: mask = 255 if y >= lowThresh && y <= highThresh, else 0.
//
// AMD64 AVX2 implementation processes 16 samples per iteration using:
//   - VPMAXUW/VPMINUW + VPCMPEQW for unsigned threshold comparison
//   - VPAND to combine both conditions
//   - VPSRLW + VPACKUSWB + VPERMQ to narrow 16-bit mask to 8-bit output
//
// lowThresh and highThresh are passed as int but must be in [0, 1023].
//
//go:noescape
func lumaKey10Row(mask *byte, y *byte, count int, lowThresh, highThresh int)
