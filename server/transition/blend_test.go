package transition

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// makeYUVFrame creates a YUV420 frame with uniform Y, Cb, Cr values.
// Layout: Y[w*h] + Cb[w/2*h/2] + Cr[w/2*h/2]
func makeYUVFrame(w, h int, y, cb, cr byte) []byte {
	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	buf := make([]byte, ySize+2*uvSize)
	for i := 0; i < ySize; i++ {
		buf[i] = y
	}
	for i := 0; i < uvSize; i++ {
		buf[ySize+i] = cb
	}
	for i := 0; i < uvSize; i++ {
		buf[ySize+uvSize+i] = cr
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
	a := makeYUVFrame(4, 4, 200, 90, 240) // bright reddish
	b := makeYUVFrame(4, 4, 50, 200, 60)  // dark bluish

	out := fb.BlendMix(a, b, 0.0)
	ySize := 4 * 4
	uvSize := 2 * 2
	for i := 0; i < ySize; i++ {
		require.Equal(t, byte(200), out[i], "Y at pixel %d", i)
	}
	for i := 0; i < uvSize; i++ {
		require.Equal(t, byte(90), out[ySize+i], "Cb at pixel %d", i)
		require.Equal(t, byte(240), out[ySize+uvSize+i], "Cr at pixel %d", i)
	}
}

func TestBlendMixPosition1(t *testing.T) {
	fb := NewFrameBlender(4, 4)
	a := makeYUVFrame(4, 4, 200, 90, 240)
	b := makeYUVFrame(4, 4, 50, 200, 60)

	out := fb.BlendMix(a, b, 1.0)
	ySize := 4 * 4
	uvSize := 2 * 2
	for i := 0; i < ySize; i++ {
		require.Equal(t, byte(50), out[i], "Y at pixel %d", i)
	}
	for i := 0; i < uvSize; i++ {
		require.Equal(t, byte(200), out[ySize+i], "Cb at pixel %d", i)
		require.Equal(t, byte(60), out[ySize+uvSize+i], "Cr at pixel %d", i)
	}
}

func TestBlendMixPosition05(t *testing.T) {
	fb := NewFrameBlender(4, 4)
	a := makeYUVFrame(4, 4, 200, 100, 200)
	b := makeYUVFrame(4, 4, 100, 200, 100)

	out := fb.BlendMix(a, b, 0.5)
	ySize := 4 * 4
	uvSize := 2 * 2
	for i := 0; i < ySize; i++ {
		require.InDelta(t, 150, int(out[i]), 1, "Y at pixel %d", i)
	}
	for i := 0; i < uvSize; i++ {
		require.InDelta(t, 150, int(out[ySize+i]), 1, "Cb at pixel %d", i)
		require.InDelta(t, 150, int(out[ySize+uvSize+i]), 1, "Cr at pixel %d", i)
	}
}

func TestBlendDipPhase1(t *testing.T) {
	fb := NewFrameBlender(4, 4)
	a := makeYUVFrame(4, 4, 200, 100, 200)
	b := makeYUVFrame(4, 4, 100, 200, 100)

	out := fb.BlendDip(a, b, 0.25)
	ySize := 4 * 4
	uvSize := 2 * 2
	// gain = 1.0 - 2*0.25 = 0.5, invGain = 0.5
	// Y: 200*0.5 = 100
	for i := 0; i < ySize; i++ {
		require.InDelta(t, 100, int(out[i]), 1, "Y at pixel %d", i)
	}
	// Cb: 100*0.5 + 128*0.5 = 114
	for i := 0; i < uvSize; i++ {
		require.InDelta(t, 114, int(out[ySize+i]), 1, "Cb at pixel %d", i)
	}
	// Cr: 200*0.5 + 128*0.5 = 164
	for i := 0; i < uvSize; i++ {
		require.InDelta(t, 164, int(out[ySize+uvSize+i]), 1, "Cr at pixel %d", i)
	}
}

func TestBlendDipMidpoint(t *testing.T) {
	fb := NewFrameBlender(4, 4)
	a := makeYUVFrame(4, 4, 255, 200, 200)
	b := makeYUVFrame(4, 4, 255, 200, 200)

	out := fb.BlendDip(a, b, 0.5)
	ySize := 4 * 4
	uvSize := 2 * 2
	// At midpoint: black = Y=0, Cb=128, Cr=128
	for i := 0; i < ySize; i++ {
		require.Equal(t, byte(0), out[i], "Y at pixel %d should be 0 at midpoint", i)
	}
	for i := 0; i < uvSize; i++ {
		require.Equal(t, byte(128), out[ySize+i], "Cb at pixel %d should be 128 at midpoint", i)
		require.Equal(t, byte(128), out[ySize+uvSize+i], "Cr at pixel %d should be 128 at midpoint", i)
	}
}

func TestBlendDipPhase2(t *testing.T) {
	fb := NewFrameBlender(4, 4)
	a := makeYUVFrame(4, 4, 100, 100, 100)
	b := makeYUVFrame(4, 4, 200, 160, 200)

	out := fb.BlendDip(a, b, 0.75)
	ySize := 4 * 4
	uvSize := 2 * 2
	// gain = 2*0.75 - 1 = 0.5, invGain = 0.5
	// Y: 200*0.5 = 100
	for i := 0; i < ySize; i++ {
		require.InDelta(t, 100, int(out[i]), 1, "Y at pixel %d", i)
	}
	// Cb: 160*0.5 + 128*0.5 = 144
	for i := 0; i < uvSize; i++ {
		require.InDelta(t, 144, int(out[ySize+i]), 1, "Cb at pixel %d", i)
	}
	// Cr: 200*0.5 + 128*0.5 = 164
	for i := 0; i < uvSize; i++ {
		require.InDelta(t, 164, int(out[ySize+uvSize+i]), 1, "Cr at pixel %d", i)
	}
}

func TestBlendFTBHalf(t *testing.T) {
	fb := NewFrameBlender(4, 4)
	a := makeYUVFrame(4, 4, 200, 100, 200)

	out := fb.BlendFTB(a, 0.5)
	ySize := 4 * 4
	uvSize := 2 * 2
	// gain=0.5, invGain=0.5
	// Y: 200*0.5 = 100
	for i := 0; i < ySize; i++ {
		require.InDelta(t, 100, int(out[i]), 1, "Y at pixel %d", i)
	}
	// Cb: 100*0.5 + 128*0.5 = 114
	for i := 0; i < uvSize; i++ {
		require.InDelta(t, 114, int(out[ySize+i]), 1, "Cb at pixel %d", i)
	}
	// Cr: 200*0.5 + 128*0.5 = 164
	for i := 0; i < uvSize; i++ {
		require.InDelta(t, 164, int(out[ySize+uvSize+i]), 1, "Cr at pixel %d", i)
	}
}

func TestBlendFTBFull(t *testing.T) {
	fb := NewFrameBlender(4, 4)
	a := makeYUVFrame(4, 4, 200, 200, 200)

	out := fb.BlendFTB(a, 1.0)
	ySize := 4 * 4
	uvSize := 2 * 2
	// Fully black: Y=0, Cb=128, Cr=128
	for i := 0; i < ySize; i++ {
		require.Equal(t, byte(0), out[i], "Y byte %d should be 0", i)
	}
	for i := 0; i < uvSize; i++ {
		require.Equal(t, byte(128), out[ySize+i], "Cb byte %d should be 128", i)
		require.Equal(t, byte(128), out[ySize+uvSize+i], "Cr byte %d should be 128", i)
	}
}

func TestBlendFTBZero(t *testing.T) {
	fb := NewFrameBlender(4, 4)
	a := makeYUVFrame(4, 4, 200, 100, 200)

	out := fb.BlendFTB(a, 0.0)
	ySize := 4 * 4
	uvSize := 2 * 2
	for i := 0; i < ySize; i++ {
		require.Equal(t, byte(200), out[i], "Y at pixel %d", i)
	}
	for i := 0; i < uvSize; i++ {
		require.Equal(t, byte(100), out[ySize+i], "Cb at pixel %d", i)
		require.Equal(t, byte(200), out[ySize+uvSize+i], "Cr at pixel %d", i)
	}
}

func TestBlenderReusesBuffer(t *testing.T) {
	fb := NewFrameBlender(4, 4)
	a := makeYUVFrame(4, 4, 100, 128, 128)
	b := makeYUVFrame(4, 4, 200, 128, 128)

	out1 := fb.BlendMix(a, b, 0.5)
	ptr1 := &out1[0]
	out2 := fb.BlendMix(a, b, 0.7)
	ptr2 := &out2[0]

	require.Equal(t, ptr1, ptr2, "blender should reuse output buffer")
}

func TestBlenderYUVBufferSize(t *testing.T) {
	fb := NewFrameBlender(1920, 1080)
	// YUV420: Y=1920*1080 + Cb=960*540 + Cr=960*540 = 3110400
	expected := 1920*1080 + 2*(960*540)
	require.Equal(t, expected, len(fb.yuvBufOut))
}
