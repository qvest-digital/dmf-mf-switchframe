package switcher

import (
	"encoding/binary"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// 10-bit SAD Correctness Tests — Spec-Derived
//
// SAD (Sum of Absolute Differences) has well-defined mathematical properties:
// - SAD(A, A) = 0 (identity)
// - SAD(A, B) = SAD(B, A) (symmetry/commutativity)
// - SAD(A, C) <= SAD(A, B) + SAD(B, C) (triangle inequality)
// - Maximum SAD for 16x16 block of 10-bit values: 16*16*1023 = 261,888
//
// These properties are mathematical facts, independent of any implementation.
// ============================================================================

// makeUniformBlock10 creates a 10-bit Y block with uniform value.
func makeUniformBlock10(stride, bw, bh int, val uint16) []byte {
	buf := make([]byte, stride*bh*2)
	for y := 0; y < bh; y++ {
		for x := 0; x < bw; x++ {
			binary.LittleEndian.PutUint16(buf[(y*stride+x)*2:], val)
		}
	}
	return buf
}

// makeRandomBlock10 creates a 10-bit Y block with random values.
func makeRandomBlock10(rng *rand.Rand, stride, bw, bh int) []byte {
	buf := make([]byte, stride*bh*2)
	for y := 0; y < bh; y++ {
		for x := 0; x < bw; x++ {
			binary.LittleEndian.PutUint16(buf[(y*stride+x)*2:], uint16(rng.Intn(1024)))
		}
	}
	return buf
}

// referenceSAD10 is a pure reference implementation for cross-checking.
func referenceSAD10(a, b []byte, aOff, bOff, stride, bw, bh int) int {
	sum := 0
	for y := 0; y < bh; y++ {
		for x := 0; x < bw; x++ {
			aVal := int(binary.LittleEndian.Uint16(a[aOff+x*2:]))
			bVal := int(binary.LittleEndian.Uint16(b[bOff+x*2:]))
			diff := aVal - bVal
			if diff < 0 {
				diff = -diff
			}
			sum += diff
		}
		aOff += stride * 2
		bOff += stride * 2
	}
	return sum
}

// ============================================================================
// SIMD vs Scalar Cross-Validation
// ============================================================================

// TestSAD10Correctness_SIMDvsScalar runs 100 random trials comparing SAD10
// (which may dispatch to SIMD) against a pure Go reference implementation.
func TestSAD10Correctness_SIMDvsScalar(t *testing.T) {
	const bw, bh = 16, 16
	stride := bw

	rng := rand.New(rand.NewSource(42))

	for trial := 0; trial < 100; trial++ {
		a := makeRandomBlock10(rng, stride, bw, bh)
		b := makeRandomBlock10(rng, stride, bw, bh)

		gotSIMD := SAD10(a, b, 0, 0, stride, bw, bh)
		gotRef := referenceSAD10(a, b, 0, 0, stride, bw, bh)

		assert.Equal(t, gotRef, gotSIMD,
			"trial %d: SAD10 dispatch (%d) != reference (%d)", trial, gotSIMD, gotRef)
	}
}

// ============================================================================
// SAD Identical Blocks = 0 (Verified with Varied Content)
// ============================================================================

// TestSAD10Correctness_IdenticalVaried verifies SAD(block, same_block) = 0
// using varied (non-uniform) content. This catches bugs that might only appear
// with non-trivial data patterns.
func TestSAD10Correctness_IdenticalVaried(t *testing.T) {
	const bw, bh = 16, 16
	stride := bw

	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 20; trial++ {
		block := makeRandomBlock10(rng, stride, bw, bh)
		got := SAD10(block, block, 0, 0, stride, bw, bh)
		assert.Equal(t, 0, got,
			"trial %d: SAD of identical varied block must be 0, got %d", trial, got)
	}
}

// ============================================================================
// SAD Maximum (16x16 block)
// ============================================================================

// TestSAD10Correctness_Maximum16x16 verifies the maximum SAD for a 16x16 block:
// Block A = all 0, Block B = all 1023
// SAD = 16 * 16 * 1023 = 261,888
func TestSAD10Correctness_Maximum16x16(t *testing.T) {
	const bw, bh = 16, 16
	stride := bw

	a := makeUniformBlock10(stride, bw, bh, 0)
	b := makeUniformBlock10(stride, bw, bh, 1023)

	got := SAD10(a, b, 0, 0, stride, bw, bh)
	expected := 16 * 16 * 1023
	assert.Equal(t, expected, got,
		"SAD(all-0, all-1023) for 16x16 should be %d, got %d", expected, got)
}

// ============================================================================
// SAD Known Small Delta
// ============================================================================

// TestSAD10Correctness_SmallDelta verifies SAD with a known constant difference.
// Block A = all 500, Block B = all 503
// SAD = 16 * 16 * 3 = 768
func TestSAD10Correctness_SmallDelta(t *testing.T) {
	const bw, bh = 16, 16
	stride := bw

	a := makeUniformBlock10(stride, bw, bh, 500)
	b := makeUniformBlock10(stride, bw, bh, 503)

	got := SAD10(a, b, 0, 0, stride, bw, bh)
	expected := bw * bh * 3
	assert.Equal(t, expected, got,
		"SAD(all-500, all-503) = %d, got %d", expected, got)
}

// ============================================================================
// SAD Symmetry (Commutativity)
// ============================================================================

// TestSAD10Correctness_Symmetry verifies SAD(A, B) == SAD(B, A) for random data.
// This must hold because |a-b| = |b-a|.
func TestSAD10Correctness_Symmetry(t *testing.T) {
	const bw, bh = 16, 16
	stride := bw

	rng := rand.New(rand.NewSource(13))

	for trial := 0; trial < 50; trial++ {
		a := makeRandomBlock10(rng, stride, bw, bh)
		b := makeRandomBlock10(rng, stride, bw, bh)

		ab := SAD10(a, b, 0, 0, stride, bw, bh)
		ba := SAD10(b, a, 0, 0, stride, bw, bh)
		assert.Equal(t, ab, ba,
			"trial %d: SAD(A,B)=%d != SAD(B,A)=%d", trial, ab, ba)
	}
}

// ============================================================================
// SAD Triangle Inequality
// ============================================================================

// TestSAD10Correctness_TriangleInequality verifies SAD(A, C) <= SAD(A, B) + SAD(B, C).
// This is a mathematical property of the L1 norm (Manhattan distance).
func TestSAD10Correctness_TriangleInequality(t *testing.T) {
	const bw, bh = 16, 16
	stride := bw

	rng := rand.New(rand.NewSource(17))

	for trial := 0; trial < 50; trial++ {
		a := makeRandomBlock10(rng, stride, bw, bh)
		b := makeRandomBlock10(rng, stride, bw, bh)
		c := makeRandomBlock10(rng, stride, bw, bh)

		ac := SAD10(a, c, 0, 0, stride, bw, bh)
		ab := SAD10(a, b, 0, 0, stride, bw, bh)
		bc := SAD10(b, c, 0, 0, stride, bw, bh)

		require.LessOrEqual(t, ac, ab+bc,
			"trial %d: SAD(A,C)=%d > SAD(A,B)+SAD(B,C)=%d+%d=%d — triangle inequality violated",
			trial, ac, ab, bc, ab+bc)
	}
}

// ============================================================================
// Scene Change Detection
// ============================================================================

// TestDetectSceneChange10Correctness_Identical verifies that identical 10-bit
// frames do not trigger scene change detection.
func TestDetectSceneChange10Correctness_Identical(t *testing.T) {
	w, h := 64, 32
	ySize := w * h * 2

	frame := make([]byte, ySize)
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(frame[i*2:], 500)
	}

	got := detectSceneChange10(frame, frame, w, h)
	assert.False(t, got, "identical frames should not trigger scene change")
}

// TestDetectSceneChange10Correctness_MaxDiff verifies that maximally different
// 10-bit frames (all 0 vs all 1023) trigger scene change detection.
func TestDetectSceneChange10Correctness_MaxDiff(t *testing.T) {
	w, h := 64, 32
	ySize := w * h * 2

	frameA := make([]byte, ySize)
	frameB := make([]byte, ySize)
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint16(frameA[i*2:], 0)
		binary.LittleEndian.PutUint16(frameB[i*2:], 1023)
	}

	got := detectSceneChange10(frameA, frameB, w, h)
	assert.True(t, got, "maximally different frames should trigger scene change")
}
