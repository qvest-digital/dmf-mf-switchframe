package transition

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func makeRGBFrame(w, h int, r, g, b byte) []byte {
	buf := make([]byte, w*h*3)
	for i := 0; i < w*h; i++ {
		buf[i*3] = r
		buf[i*3+1] = g
		buf[i*3+2] = b
	}
	return buf
}

func TestBlenderNew(t *testing.T) {
	fb := NewFrameBlender(1920, 1080)
	require.NotNil(t, fb)
	require.Equal(t, 1920, fb.width)
	require.Equal(t, 1080, fb.height)
}

func TestBlendMixPosition0(t *testing.T) {
	fb := NewFrameBlender(4, 4)
	a := makeRGBFrame(4, 4, 255, 0, 0) // red
	b := makeRGBFrame(4, 4, 0, 0, 255) // blue

	out := fb.BlendMix(a, b, 0.0)
	for i := 0; i < 4*4; i++ {
		require.Equal(t, byte(255), out[i*3], "R at pixel %d", i)
		require.Equal(t, byte(0), out[i*3+1], "G at pixel %d", i)
		require.Equal(t, byte(0), out[i*3+2], "B at pixel %d", i)
	}
}

func TestBlendMixPosition1(t *testing.T) {
	fb := NewFrameBlender(4, 4)
	a := makeRGBFrame(4, 4, 255, 0, 0)
	b := makeRGBFrame(4, 4, 0, 0, 255)

	out := fb.BlendMix(a, b, 1.0)
	for i := 0; i < 4*4; i++ {
		require.Equal(t, byte(0), out[i*3], "R at pixel %d", i)
		require.Equal(t, byte(0), out[i*3+1], "G at pixel %d", i)
		require.Equal(t, byte(255), out[i*3+2], "B at pixel %d", i)
	}
}

func TestBlendMixPosition05(t *testing.T) {
	fb := NewFrameBlender(4, 4)
	a := makeRGBFrame(4, 4, 200, 0, 0)
	b := makeRGBFrame(4, 4, 0, 0, 200)

	out := fb.BlendMix(a, b, 0.5)
	for i := 0; i < 4*4; i++ {
		require.InDelta(t, 100, int(out[i*3]), 1, "R at pixel %d", i)
		require.Equal(t, byte(0), out[i*3+1], "G at pixel %d", i)
		require.InDelta(t, 100, int(out[i*3+2]), 1, "B at pixel %d", i)
	}
}

func TestBlendDipPhase1(t *testing.T) {
	fb := NewFrameBlender(4, 4)
	a := makeRGBFrame(4, 4, 200, 200, 200)
	b := makeRGBFrame(4, 4, 100, 100, 100)

	out := fb.BlendDip(a, b, 0.25)
	for i := 0; i < 4*4; i++ {
		require.InDelta(t, 100, int(out[i*3]), 1, "pixel %d", i)
	}
}

func TestBlendDipMidpoint(t *testing.T) {
	fb := NewFrameBlender(4, 4)
	a := makeRGBFrame(4, 4, 255, 255, 255)
	b := makeRGBFrame(4, 4, 255, 255, 255)

	out := fb.BlendDip(a, b, 0.5)
	for i := 0; i < len(out); i++ {
		require.Equal(t, byte(0), out[i], "byte %d should be 0 at midpoint", i)
	}
}

func TestBlendDipPhase2(t *testing.T) {
	fb := NewFrameBlender(4, 4)
	a := makeRGBFrame(4, 4, 100, 100, 100)
	b := makeRGBFrame(4, 4, 200, 200, 200)

	out := fb.BlendDip(a, b, 0.75)
	for i := 0; i < 4*4; i++ {
		require.InDelta(t, 100, int(out[i*3]), 1, "pixel %d", i)
	}
}

func TestBlendFTBHalf(t *testing.T) {
	fb := NewFrameBlender(4, 4)
	a := makeRGBFrame(4, 4, 200, 200, 200)

	out := fb.BlendFTB(a, 0.5)
	for i := 0; i < 4*4; i++ {
		require.InDelta(t, 100, int(out[i*3]), 1, "pixel %d", i)
	}
}

func TestBlendFTBFull(t *testing.T) {
	fb := NewFrameBlender(4, 4)
	a := makeRGBFrame(4, 4, 200, 200, 200)

	out := fb.BlendFTB(a, 1.0)
	for i := 0; i < len(out); i++ {
		require.Equal(t, byte(0), out[i], "byte %d should be 0", i)
	}
}

func TestBlendFTBZero(t *testing.T) {
	fb := NewFrameBlender(4, 4)
	a := makeRGBFrame(4, 4, 200, 200, 200)

	out := fb.BlendFTB(a, 0.0)
	for i := 0; i < 4*4; i++ {
		require.Equal(t, byte(200), out[i*3], "pixel %d", i)
	}
}

func TestBlenderReusesBuffer(t *testing.T) {
	fb := NewFrameBlender(4, 4)
	a := makeRGBFrame(4, 4, 100, 100, 100)
	b := makeRGBFrame(4, 4, 200, 200, 200)

	out1 := fb.BlendMix(a, b, 0.5)
	ptr1 := &out1[0]
	out2 := fb.BlendMix(a, b, 0.7)
	ptr2 := &out2[0]

	require.Equal(t, ptr1, ptr2, "blender should reuse output buffer")
}
