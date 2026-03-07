package transition

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"
)

// --- blendUniform kernel tests ---

func TestKernelUniform_ZeroPosition(t *testing.T) {
	t.Parallel()
	// pos=0, inv=256: dst = a
	a := []byte{10, 20, 30, 40, 50}
	b := []byte{200, 210, 220, 230, 240}
	dst := make([]byte, 5)
	blendUniform(&dst[0], &a[0], &b[0], 5, 0, 256)
	require.Equal(t, a, dst)
}

func TestKernelUniform_FullPosition(t *testing.T) {
	t.Parallel()
	// pos=256, inv=0: dst = b
	a := []byte{10, 20, 30, 40, 50}
	b := []byte{200, 210, 220, 230, 240}
	dst := make([]byte, 5)
	blendUniform(&dst[0], &a[0], &b[0], 5, 256, 0)
	require.Equal(t, b, dst)
}

func TestKernelUniform_HalfPosition(t *testing.T) {
	t.Parallel()
	// pos=128, inv=128: dst = (a+b)/2 (approximately)
	a := []byte{100, 200, 0, 255, 128}
	b := []byte{200, 100, 255, 0, 128}
	dst := make([]byte, 5)
	blendUniform(&dst[0], &a[0], &b[0], 5, 128, 128)
	for i := range dst {
		expected := (int(a[i])*128 + int(b[i])*128) >> 8
		require.Equal(t, byte(expected), dst[i], "pixel %d", i)
	}
}

func TestKernelUniform_VariousSizes(t *testing.T) {
	t.Parallel()
	// Test sizes that exercise tail handling: 0, 1, 15, 16, 17, 31, 32, 33
	sizes := []int{0, 1, 15, 16, 17, 31, 32, 33, 63, 64, 65}
	for _, n := range sizes {
		if n == 0 {
			// n=0 should not panic
			blendUniform(nil, nil, nil, 0, 128, 128)
			continue
		}
		a := make([]byte, n)
		b := make([]byte, n)
		dst := make([]byte, n)
		for i := range a {
			a[i] = byte(i % 256)
			b[i] = byte((i * 7 + 13) % 256)
		}
		blendUniform(&dst[0], &a[0], &b[0], n, 100, 156)
		for i := 0; i < n; i++ {
			expected := byte((int(a[i])*156 + int(b[i])*100) >> 8)
			require.Equal(t, expected, dst[i], "size=%d pixel=%d", n, i)
		}
	}
}

func TestKernelUniform_1080p(t *testing.T) {
	t.Parallel()
	n := 1920 * 1080 * 3 / 2
	rng := rand.New(rand.NewSource(42))
	a := make([]byte, n)
	b := make([]byte, n)
	dst := make([]byte, n)
	for i := range a {
		a[i] = byte(rng.Intn(256))
		b[i] = byte(rng.Intn(256))
	}
	pos := 170
	inv := 256 - pos
	blendUniform(&dst[0], &a[0], &b[0], n, pos, inv)
	// Spot check first, middle, last pixels
	for _, i := range []int{0, n / 2, n - 1} {
		expected := byte((int(a[i])*inv + int(b[i])*pos) >> 8)
		require.Equal(t, expected, dst[i], "pixel %d", i)
	}
}

// --- blendFadeConst kernel tests ---

func TestKernelFadeConst_FullGain(t *testing.T) {
	t.Parallel()
	// gain=256, constTerm=0: dst = src
	src := []byte{10, 20, 30, 40, 50}
	dst := make([]byte, 5)
	blendFadeConst(&dst[0], &src[0], 5, 256, 0)
	require.Equal(t, src, dst)
}

func TestKernelFadeConst_ZeroGain(t *testing.T) {
	t.Parallel()
	// gain=0: dst = constTerm >> 8
	src := []byte{10, 20, 30, 40, 50}
	dst := make([]byte, 5)
	constTerm := 128 * 256 // = 32768
	blendFadeConst(&dst[0], &src[0], 5, 0, constTerm)
	for i := range dst {
		require.Equal(t, byte(128), dst[i], "pixel %d", i)
	}
}

func TestKernelFadeConst_HalfGain_ChromaNeutral(t *testing.T) {
	t.Parallel()
	// gain=128, constTerm=128*128=16384 (chroma plane at half fade)
	src := []byte{200, 200, 200, 200}
	dst := make([]byte, 4)
	blendFadeConst(&dst[0], &src[0], 4, 128, 128*128)
	for i := range dst {
		expected := byte((200*128 + 128*128) >> 8)
		require.Equal(t, expected, dst[i], "pixel %d", i)
	}
}

func TestKernelFadeConst_LimitedRangeBlack(t *testing.T) {
	t.Parallel()
	// Y plane fade to limited-range black (Y=16)
	// gain=128, constTerm=16*(256-128)=2048
	src := []byte{200, 200, 200, 200}
	dst := make([]byte, 4)
	blendFadeConst(&dst[0], &src[0], 4, 128, 16*128)
	for i := range dst {
		expected := byte((200*128 + 16*128) >> 8)
		require.Equal(t, expected, dst[i], "pixel %d", i)
	}
}

func TestKernelFadeConst_VariousSizes(t *testing.T) {
	t.Parallel()
	sizes := []int{0, 1, 15, 16, 17, 31, 32, 33, 63, 64, 65}
	for _, n := range sizes {
		if n == 0 {
			blendFadeConst(nil, nil, 0, 128, 128*128)
			continue
		}
		src := make([]byte, n)
		dst := make([]byte, n)
		for i := range src {
			src[i] = byte(i % 256)
		}
		gain := 200
		constTerm := 16 * (256 - gain) // limited-range Y
		blendFadeConst(&dst[0], &src[0], n, gain, constTerm)
		for i := 0; i < n; i++ {
			expected := byte((int(src[i])*gain + constTerm) >> 8)
			require.Equal(t, expected, dst[i], "size=%d pixel=%d", n, i)
		}
	}
}

// --- blendAlpha kernel tests ---

func TestKernelAlpha_ZeroAlpha(t *testing.T) {
	t.Parallel()
	// alpha=0: w=0, dst = a
	a := []byte{100, 150, 200, 250}
	b := []byte{10, 20, 30, 40}
	alpha := []byte{0, 0, 0, 0}
	dst := make([]byte, 4)
	blendAlpha(&dst[0], &a[0], &b[0], &alpha[0], 4)
	require.Equal(t, a, dst)
}

func TestKernelAlpha_FullAlpha(t *testing.T) {
	t.Parallel()
	// alpha=255: w=255+(255>>7)=255+1=256, dst = b
	a := []byte{100, 150, 200, 250}
	b := []byte{10, 20, 30, 40}
	alpha := []byte{255, 255, 255, 255}
	dst := make([]byte, 4)
	blendAlpha(&dst[0], &a[0], &b[0], &alpha[0], 4)
	require.Equal(t, b, dst)
}

func TestKernelAlpha_HalfAlpha(t *testing.T) {
	t.Parallel()
	// alpha=128: w=128+(128>>7)=128+1=129
	a := []byte{200, 200, 200, 200}
	b := []byte{100, 100, 100, 100}
	alpha := []byte{128, 128, 128, 128}
	dst := make([]byte, 4)
	blendAlpha(&dst[0], &a[0], &b[0], &alpha[0], 4)
	for i := range dst {
		w := 128 + (128 >> 7)
		inv := 256 - w
		expected := byte((200*inv + 100*w) >> 8)
		require.Equal(t, expected, dst[i], "pixel %d", i)
	}
}

func TestKernelAlpha_PerPixel(t *testing.T) {
	t.Parallel()
	// Different alpha per pixel
	a := []byte{200, 100, 50, 255}
	b := []byte{50, 200, 255, 0}
	alpha := []byte{0, 64, 192, 255}
	dst := make([]byte, 4)
	blendAlpha(&dst[0], &a[0], &b[0], &alpha[0], 4)
	for i := range dst {
		ai := int(alpha[i])
		w := ai + (ai >> 7)
		inv := 256 - w
		expected := byte((int(a[i])*inv + int(b[i])*w) >> 8)
		require.Equal(t, expected, dst[i], "pixel %d", i)
	}
}

func TestKernelAlpha_VariousSizes(t *testing.T) {
	t.Parallel()
	sizes := []int{0, 1, 15, 16, 17, 31, 32, 33, 63, 64, 65}
	for _, n := range sizes {
		if n == 0 {
			blendAlpha(nil, nil, nil, nil, 0)
			continue
		}
		a := make([]byte, n)
		b := make([]byte, n)
		alpha := make([]byte, n)
		dst := make([]byte, n)
		for i := range a {
			a[i] = byte(i % 256)
			b[i] = byte((i * 7 + 13) % 256)
			alpha[i] = byte((i * 3) % 256)
		}
		blendAlpha(&dst[0], &a[0], &b[0], &alpha[0], n)
		for i := 0; i < n; i++ {
			ai := int(alpha[i])
			w := ai + (ai >> 7)
			inv := 256 - w
			expected := byte((int(a[i])*inv + int(b[i])*w) >> 8)
			require.Equal(t, expected, dst[i], "size=%d pixel=%d", n, i)
		}
	}
}

// --- Cross-validation: kernel results match original inline implementation ---

func TestKernelUniform_CrossValidateWithBlendMix(t *testing.T) {
	t.Parallel()
	// Verify kernel produces identical results to the original inline loop
	rng := rand.New(rand.NewSource(99))
	n := 1920 * 1080 * 3 / 2
	a := make([]byte, n)
	b := make([]byte, n)
	for i := range a {
		a[i] = byte(rng.Intn(256))
		b[i] = byte(rng.Intn(256))
	}

	for _, posF := range []float64{0.0, 0.25, 0.5, 0.75, 1.0} {
		pos := int(posF*256 + 0.5)
		if pos > 256 {
			pos = 256
		}
		inv := 256 - pos

		// Kernel result
		kernelDst := make([]byte, n)
		blendUniform(&kernelDst[0], &a[0], &b[0], n, pos, inv)

		// Reference (original scalar)
		refDst := make([]byte, n)
		for i := 0; i < n; i++ {
			refDst[i] = byte((int(a[i])*inv + int(b[i])*pos) >> 8)
		}

		require.Equal(t, refDst, kernelDst, "pos=%.2f mismatch", posF)
	}
}

func TestKernelFadeConst_CrossValidateWithBlendFTB(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(77))
	ySize := 1920 * 1080
	uvSize := 960 * 540
	n := ySize + 2*uvSize
	src := make([]byte, n)
	for i := range src {
		src[i] = byte(rng.Intn(256))
	}

	for _, posF := range []float64{0.0, 0.25, 0.5, 0.75, 1.0} {
		gain256 := int((1.0-posF)*256 + 0.5)
		if gain256 > 256 {
			gain256 = 256
		}
		if gain256 < 0 {
			gain256 = 0
		}
		invGain256 := 256 - gain256
		blackYi := 16 // limited range

		// Kernel results
		kernelDst := make([]byte, n)
		// Y plane
		blendFadeConst(&kernelDst[0], &src[0], ySize, gain256, blackYi*invGain256)
		// Cb plane
		blendFadeConst(&kernelDst[ySize], &src[ySize], uvSize, gain256, 128*invGain256)
		// Cr plane
		blendFadeConst(&kernelDst[ySize+uvSize], &src[ySize+uvSize], uvSize, gain256, 128*invGain256)

		// Reference
		refDst := make([]byte, n)
		for i := 0; i < ySize; i++ {
			refDst[i] = byte((int(src[i])*gain256 + blackYi*invGain256) >> 8)
		}
		for i := 0; i < uvSize; i++ {
			refDst[ySize+i] = byte((int(src[ySize+i])*gain256 + 128*invGain256) >> 8)
		}
		for i := 0; i < uvSize; i++ {
			refDst[ySize+uvSize+i] = byte((int(src[ySize+uvSize+i])*gain256 + 128*invGain256) >> 8)
		}

		require.Equal(t, refDst, kernelDst, "pos=%.2f mismatch", posF)
	}
}

func TestKernelAlpha_CrossValidateWithBlendWipe(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(55))
	n := 1920 * 1080
	a := make([]byte, n)
	b := make([]byte, n)
	alpha := make([]byte, n)
	for i := range a {
		a[i] = byte(rng.Intn(256))
		b[i] = byte(rng.Intn(256))
		alpha[i] = byte(rng.Intn(256))
	}

	// Kernel result
	kernelDst := make([]byte, n)
	blendAlpha(&kernelDst[0], &a[0], &b[0], &alpha[0], n)

	// Reference
	refDst := make([]byte, n)
	for i := 0; i < n; i++ {
		ai := int(alpha[i])
		w := ai + (ai >> 7)
		inv := 256 - w
		refDst[i] = byte((int(a[i])*inv + int(b[i])*w) >> 8)
	}

	require.Equal(t, refDst, kernelDst)
}
