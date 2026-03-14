package graphics

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBlendMaskY_AllTransparent(t *testing.T) {
	t.Parallel()
	n := 32
	bg := make([]byte, n)
	for i := range bg {
		bg[i] = 128
	}
	origBg := make([]byte, n)
	copy(origBg, bg)

	fill := make([]byte, n)
	for i := range fill {
		fill[i] = 200
	}
	mask := make([]byte, n) // all zeros

	blendMaskY(&bg[0], &fill[0], &mask[0], n)

	for i := 0; i < n; i++ {
		assert.Equal(t, origBg[i], bg[i], "bg[%d] should be unchanged for transparent mask", i)
	}
}

func TestBlendMaskY_FullOpaque(t *testing.T) {
	t.Parallel()
	n := 32
	bg := make([]byte, n)
	for i := range bg {
		bg[i] = 50
	}

	fill := make([]byte, n)
	for i := range fill {
		fill[i] = 200
	}
	mask := make([]byte, n)
	for i := range mask {
		mask[i] = 255
	}

	blendMaskY(&bg[0], &fill[0], &mask[0], n)

	// mask=255 → w=256, inv=0 → result = (50*0 + 200*256 + 128) >> 8 = 200
	for i := 0; i < n; i++ {
		assert.Equal(t, byte(200), bg[i], "bg[%d] should equal fill for full opaque mask", i)
	}
}

func TestBlendMaskY_HalfAlpha(t *testing.T) {
	t.Parallel()
	n := 32
	bg := make([]byte, n)
	for i := range bg {
		bg[i] = 0
	}

	fill := make([]byte, n)
	for i := range fill {
		fill[i] = 200
	}
	mask := make([]byte, n)
	for i := range mask {
		mask[i] = 128
	}

	blendMaskY(&bg[0], &fill[0], &mask[0], n)

	// mask=128 → w=128+1=129, inv=127
	// result = (0*127 + 200*129 + 128) >> 8 = (25800 + 128) >> 8 = 25928 >> 8 = 101
	for i := 0; i < n; i++ {
		assert.InDelta(t, 101, int(bg[i]), 1, "bg[%d] should be ~101 for half-alpha", i)
	}
}

func TestBlendMaskY_Rounding(t *testing.T) {
	t.Parallel()
	// Reproduce the rounding test from TestKeyProcessor_BlendRoundingNotTruncated.
	// bg=40, fill=153, mask=85 → expected 78 (rounded), not 77 (truncated).
	n := 16
	bg := make([]byte, n)
	for i := range bg {
		bg[i] = 40
	}

	fill := make([]byte, n)
	for i := range fill {
		fill[i] = 153
	}
	mask := make([]byte, n)
	for i := range mask {
		mask[i] = 85
	}

	blendMaskY(&bg[0], &fill[0], &mask[0], n)

	// w = 85 + (85>>7) = 85 + 0 = 85
	// inv = 171
	// (40*171 + 153*85 + 128) >> 8 = (6840 + 13005 + 128) >> 8 = 19973 >> 8 = 78
	for i := 0; i < n; i++ {
		assert.Equal(t, byte(78), bg[i],
			"bg[%d] should be 78 (rounded), got %d", i, bg[i])
	}
}

// TestBlendMaskY_MatchesReference cross-validates the integer fixed-point
// kernel against a float32 reference implementation at 1920 width (full HD row).
func TestBlendMaskY_MatchesReference(t *testing.T) {
	t.Parallel()
	n := 1920

	// Create varied data
	bg := make([]byte, n)
	fill := make([]byte, n)
	mask := make([]byte, n)
	for i := 0; i < n; i++ {
		bg[i] = byte((i * 7) % 256)
		fill[i] = byte((i*13 + 50) % 256)
		mask[i] = byte((i * 3) % 256)
	}

	// Reference: float32 implementation
	refBg := make([]byte, n)
	copy(refBg, bg)
	for i := 0; i < n; i++ {
		alpha := float32(mask[i]) / 255.0
		if alpha < 1.0/255.0 {
			continue
		}
		invAlpha := 1.0 - alpha
		refBg[i] = uint8(clampFloat(float32(refBg[i])*invAlpha+float32(fill[i])*alpha+0.5, 0, 255))
	}

	// Kernel
	blendMaskY(&bg[0], &fill[0], &mask[0], n)

	mismatches := 0
	for i := 0; i < n; i++ {
		diff := int(bg[i]) - int(refBg[i])
		if diff < -1 || diff > 1 {
			mismatches++
			if mismatches <= 10 {
				t.Errorf("bg[%d]: kernel=%d, reference=%d, diff=%d (mask=%d)",
					i, bg[i], refBg[i], diff, mask[i])
			}
		}
	}
	if mismatches > 0 {
		t.Errorf("total mismatches (>1 tolerance): %d / %d", mismatches, n)
	}
}

func TestBlendMaskY_OddSizes(t *testing.T) {
	t.Parallel()
	for _, n := range []int{1, 3, 7, 15, 17, 31, 33} {
		bg := make([]byte, n)
		fill := make([]byte, n)
		mask := make([]byte, n)
		for i := 0; i < n; i++ {
			bg[i] = 50
			fill[i] = 200
			mask[i] = 128
		}

		blendMaskY(&bg[0], &fill[0], &mask[0], n)

		for i := 0; i < n; i++ {
			require.True(t, bg[i] > 50 && bg[i] < 200,
				"n=%d: bg[%d]=%d should be between 50 and 200", n, i, bg[i])
		}
	}
}

func TestBlendMaskY_ZeroLength(t *testing.T) {
	t.Parallel()
	var b byte
	blendMaskY(&b, &b, &b, 0) // should not panic
}

func TestDownsampleMask2x2_Uniform(t *testing.T) {
	t.Parallel()
	width, height := 8, 8
	mask := make([]byte, width*height)
	for i := range mask {
		mask[i] = 200
	}

	chromaWidth := width / 2
	chromaHeight := height / 2
	dst := make([]byte, chromaWidth*chromaHeight)
	downsampleMask2x2(dst, mask, width, height)

	// avg of 4×200 = 200
	for i := range dst {
		assert.Equal(t, byte(200), dst[i], "dst[%d] should be 200 for uniform mask", i)
	}
}

func TestDownsampleMask2x2_Checkerboard(t *testing.T) {
	t.Parallel()
	width, height := 4, 4
	mask := make([]byte, width*height)
	// Checkerboard: 0 and 255 alternating
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if (x+y)%2 == 0 {
				mask[y*width+x] = 255
			}
		}
	}

	chromaWidth := width / 2
	chromaHeight := height / 2
	dst := make([]byte, chromaWidth*chromaHeight)
	downsampleMask2x2(dst, mask, width, height)

	// Each 2x2 block has 2×255 + 2×0 = 510, avg = (510+2)>>2 = 128
	for i := range dst {
		assert.Equal(t, byte(128), dst[i], "dst[%d] should be 128 for checkerboard", i)
	}
}

func TestDownsampleMask2x2_AllZeros(t *testing.T) {
	t.Parallel()
	width, height := 8, 4
	mask := make([]byte, width*height)
	dst := make([]byte, (width/2)*(height/2))
	downsampleMask2x2(dst, mask, width, height)

	for i := range dst {
		assert.Equal(t, byte(0), dst[i])
	}
}

// BenchmarkBlendMaskY_1080p_Y benchmarks the Y-plane kernel on 1080p resolution.
func BenchmarkBlendMaskY_1080p_Y(b *testing.B) {
	n := 1920 * 1080
	bg := make([]byte, n)
	fill := make([]byte, n)
	mask := make([]byte, n)
	for i := 0; i < n; i++ {
		bg[i] = 128
		fill[i] = byte(i % 256)
		mask[i] = byte((i * 3) % 256)
	}
	bgCopy := make([]byte, n)

	b.ReportAllocs()
	b.SetBytes(int64(n))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		copy(bgCopy, bg)
		blendMaskY(&bgCopy[0], &fill[0], &mask[0], n)
	}
}
