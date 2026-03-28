//go:build correctness

package switcher

import (
	"math/rand"
	"testing"

	"github.com/zsiec/switchframe/server/switcher/frcasm"
)

// TestSADSIMDvsScalar computes SAD on the same random 16x16 blocks using
// both the platform-specific implementation (SIMD on amd64/arm64, generic
// on other platforms) and a naive reference. They must produce identical results.
func TestSADSIMDvsScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	for trial := 0; trial < 100; trial++ {
		// Generate two random 16x16 blocks with configurable stride
		stride := 16 + rng.Intn(32) // stride >= 16, possibly larger
		a := make([]byte, stride*16)
		b := make([]byte, stride*16)
		for i := range a {
			a[i] = byte(rng.Intn(256))
			b[i] = byte(rng.Intn(256))
		}

		// Platform-specific SAD
		got := frcasm.SadBlock16x16(&a[0], &b[0], stride, stride)

		// Naive reference SAD
		var expected uint32
		for row := 0; row < 16; row++ {
			for col := 0; col < 16; col++ {
				av := int(a[row*stride+col])
				bv := int(b[row*stride+col])
				d := av - bv
				if d < 0 {
					d = -d
				}
				expected += uint32(d)
			}
		}

		if got != expected {
			t.Fatalf("trial %d: SadBlock16x16 = %d, reference = %d", trial, got, expected)
		}
	}
}

// TestSAD4x verifies that SadBlock16x16x4 produces the same results as
// 4 individual SadBlock16x16 calls.
func TestSAD4x(t *testing.T) {
	rng := rand.New(rand.NewSource(99))

	stride := 32
	cur := make([]byte, stride*16)
	refs := [4][]byte{
		make([]byte, stride*16),
		make([]byte, stride*16),
		make([]byte, stride*16),
		make([]byte, stride*16),
	}

	for trial := 0; trial < 50; trial++ {
		for i := range cur {
			cur[i] = byte(rng.Intn(256))
		}
		for r := 0; r < 4; r++ {
			for i := range refs[r] {
				refs[r][i] = byte(rng.Intn(256))
			}
		}

		refPtrs := [4]*byte{&refs[0][0], &refs[1][0], &refs[2][0], &refs[3][0]}
		got4 := frcasm.SadBlock16x16x4(&cur[0], refPtrs, stride, stride)

		for r := 0; r < 4; r++ {
			individual := frcasm.SadBlock16x16(&cur[0], &refs[r][0], stride, stride)
			if got4[r] != individual {
				t.Fatalf("trial %d ref %d: batch=%d, individual=%d", trial, r, got4[r], individual)
			}
		}
	}
}

// TestSADIdenticalBlocks verifies that SAD of two identical blocks is zero.
func TestSADIdenticalBlocks(t *testing.T) {
	stride := 16
	block := make([]byte, stride*16)
	for i := range block {
		block[i] = byte(i & 0xFF)
	}

	sad := frcasm.SadBlock16x16(&block[0], &block[0], stride, stride)
	if sad != 0 {
		t.Fatalf("SAD of identical blocks = %d, expected 0", sad)
	}
}

// TestSADMaximum verifies the maximum possible SAD (all 0 vs all 255).
func TestSADMaximum(t *testing.T) {
	stride := 16
	a := make([]byte, stride*16)
	b := make([]byte, stride*16)
	for i := range b {
		b[i] = 255
	}

	sad := frcasm.SadBlock16x16(&a[0], &b[0], stride, stride)
	expected := uint32(16 * 16 * 255) // 65280
	if sad != expected {
		t.Fatalf("SAD(zeros, 255s) = %d, expected %d", sad, expected)
	}
}

// TestMotionVectorSearch verifies that diamond search finds a block at a known
// displacement. We create two frames from the same random content, with the
// second frame shifted by a known amount. This simulates a global translation
// (panning camera) where every block has the same motion vector.
func TestMotionVectorSearch(t *testing.T) {
	const width = 128
	const height = 96
	const blockSize = 16

	rng := rand.New(rand.NewSource(7))

	// Generate a large random source image (bigger than both frames combined
	// with the shift margin)
	const srcW = width + 32
	const srcH = height + 32
	source := make([]byte, srcW*srcH)
	for i := range source {
		source[i] = byte(rng.Intn(256))
	}

	// Displacement: (dx, dy) = (4, 2) — within search range
	const dx, dy = 4, 2

	// "cur" frame: copy from source at origin (0, 0)
	cur := make([]byte, width*height)
	for row := 0; row < height; row++ {
		for col := 0; col < width; col++ {
			cur[row*width+col] = source[row*srcW+col]
		}
	}

	// "ref" frame: copy from source at origin (dx, dy)
	// ref[row][col] = source[row+dy][col+dx]
	// So cur[bx,by] block = source[by..by+15][bx..bx+15]
	// The same content in ref is at ref[by-dy, bx-dx].
	// Diamond search finds MV such that ref[by+mvy][bx+mvx] matches cur[by][bx].
	// Since ref is cur shifted by (dx,dy), the matching MV is (-dx, -dy).
	ref := make([]byte, width*height)
	for row := 0; row < height; row++ {
		for col := 0; col < width; col++ {
			ref[row*width+col] = source[(row+dy)*srcW+(col+dx)]
		}
	}

	// Search at block (48, 32). Since ref is cur shifted by (dx,dy),
	// the diamond search should find MV=(-dx, -dy) with SAD=0.
	const bx, by = 48, 32
	mvx, mvy, sad := diamondSearch(
		cur, width,
		ref, width,
		bx, by,
		width, height,
		16, // search range
		blockSize,
		0, 0, // no predictor
	)

	t.Logf("MV=(%d, %d), SAD=%d", mvx, mvy, sad)

	expectedMVX, expectedMVY := -dx, -dy
	if int(mvx) != expectedMVX || int(mvy) != expectedMVY {
		t.Errorf("expected MV=(%d, %d), got MV=(%d, %d) with SAD=%d",
			expectedMVX, expectedMVY, mvx, mvy, sad)
	}

	if sad != 0 {
		t.Errorf("expected SAD=0 for exact match, got SAD=%d", sad)
	}
}

// TestDownsampleSIMDvsScalar verifies that DownsampleY2x produces the same
// output as a naive Go reference implementation.
func TestDownsampleSIMDvsScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(55))

	srcW := 64
	srcH := 48
	src := make([]byte, srcW*srcH)
	for i := range src {
		src[i] = byte(rng.Intn(256))
	}

	dstW := srcW / 2
	dstH := srcH / 2

	// Platform-specific downsample
	got := make([]byte, dstW*dstH)
	frcasm.DownsampleY2x(&got[0], &src[0], srcW, srcH)

	// Naive reference using cascaded rounding (matches PAVGB/URHADD semantics)
	expected := make([]byte, dstW*dstH)
	for row := 0; row < dstH; row++ {
		srcRow0 := row * 2 * srcW
		srcRow1 := srcRow0 + srcW
		for col := 0; col < dstW; col++ {
			sc := col * 2
			avgH0 := (uint(src[srcRow0+sc]) + uint(src[srcRow0+sc+1]) + 1) >> 1
			avgH1 := (uint(src[srcRow1+sc]) + uint(src[srcRow1+sc+1]) + 1) >> 1
			expected[row*dstW+col] = byte((avgH0 + avgH1 + 1) >> 1)
		}
	}

	for i := range expected {
		if got[i] != expected[i] {
			row := i / dstW
			col := i % dstW
			t.Fatalf("mismatch at (%d,%d): got %d, expected %d", row, col, got[i], expected[i])
		}
	}
}

// TestDownsampleUniform verifies that downsampling a uniform frame preserves
// the pixel value exactly.
func TestDownsampleUniform(t *testing.T) {
	srcW := 64
	srcH := 48
	src := make([]byte, srcW*srcH)
	for i := range src {
		src[i] = 128
	}

	dstW := srcW / 2
	dstH := srcH / 2
	dst := make([]byte, dstW*dstH)
	frcasm.DownsampleY2x(&dst[0], &src[0], srcW, srcH)

	for i, v := range dst {
		if v != 128 {
			t.Fatalf("uniform downsample: dst[%d]=%d, expected 128", i, v)
		}
	}
}

// TestSadRow verifies SadRow against a naive reference.
func TestSadRow(t *testing.T) {
	rng := rand.New(rand.NewSource(77))

	for _, n := range []int{16, 64, 256, 1000, 1920} {
		a := make([]byte, n)
		b := make([]byte, n)
		for i := range a {
			a[i] = byte(rng.Intn(256))
			b[i] = byte(rng.Intn(256))
		}

		got := frcasm.SadRow(&a[0], &b[0], n)

		var expected uint64
		for i := 0; i < n; i++ {
			d := int(a[i]) - int(b[i])
			if d < 0 {
				d = -d
			}
			expected += uint64(d)
		}

		if got != expected {
			t.Fatalf("n=%d: SadRow=%d, reference=%d", n, got, expected)
		}
	}
}
