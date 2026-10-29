package playout

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewIdleFrame10_Size(t *testing.T) {
	w, h := 16, 8
	frame := NewIdleFrame10(w, h)
	// YUV422P10LE: Y plane (w*h*2) + Cb plane (w/2*h*2) + Cr plane (w/2*h*2) = w*h*4
	require.Equal(t, w*h*4, len(frame), "10-bit idle frame size should be w*h*4")
}

func TestNewIdleFrame10_BlackValues(t *testing.T) {
	w, h := 16, 8
	frame := NewIdleFrame10(w, h)

	// Check Y plane: every sample should be 64 (BT.709 limited-range black in 10-bit)
	ySize := w * h
	for i := 0; i < ySize; i++ {
		gotY := binary.LittleEndian.Uint16(frame[i*2:])
		if gotY != 64 {
			t.Fatalf("Y sample %d: got %d, want 64", i, gotY)
		}
	}

	// Check Cb plane: every sample should be 512 (neutral chroma in 10-bit)
	cbOff := ySize * 2
	chromaW := w / 2
	chromaSize := chromaW * h
	for i := 0; i < chromaSize; i++ {
		gotCb := binary.LittleEndian.Uint16(frame[cbOff+i*2:])
		if gotCb != 512 {
			t.Fatalf("Cb sample %d: got %d, want 512", i, gotCb)
		}
	}

	// Check Cr plane: every sample should be 512
	crOff := cbOff + chromaSize*2
	for i := 0; i < chromaSize; i++ {
		gotCr := binary.LittleEndian.Uint16(frame[crOff+i*2:])
		if gotCr != 512 {
			t.Fatalf("Cr sample %d: got %d, want 512", i, gotCr)
		}
	}
}

func TestNewIdleFrame10_DifferentDimensions(t *testing.T) {
	cases := []struct {
		w, h int
	}{
		{2, 2},
		{32, 16},
		{1920, 1080},
	}
	for _, tc := range cases {
		frame := NewIdleFrame10(tc.w, tc.h)
		require.Equal(t, tc.w*tc.h*4, len(frame), "frame size for %dx%d", tc.w, tc.h)

		// Spot-check first Y sample
		gotY := binary.LittleEndian.Uint16(frame[0:])
		require.Equal(t, uint16(64), gotY, "first Y sample for %dx%d", tc.w, tc.h)
	}
}

func TestMakeBlackFrame10_ReturnsDecodedFrame(t *testing.T) {
	w, h := 16, 8
	frame := makeBlackFrame10(w, h)

	require.NotNil(t, frame)
	require.Equal(t, w, frame.Width)
	require.Equal(t, h, frame.Height)
	require.True(t, frame.IsKeyframe)
	require.Equal(t, w*h*4, len(frame.YUV))

	// Verify black values
	gotY := binary.LittleEndian.Uint16(frame.YUV[0:])
	require.Equal(t, uint16(64), gotY)
}

// newIdleFrame10Filled creates a YUV422P10LE frame filled with uniform values.
// Helper for blend tests.
func newIdleFrame10Filled(w, h int, yVal, cbVal, crVal uint16) []byte {
	frameSize := w * h * 4
	buf := make([]byte, frameSize)
	ySize := w * h
	chromaW := w / 2
	chromaSize := chromaW * h

	// Fill Y plane
	for i := 0; i < ySize; i++ {
		binary.LittleEndian.PutUint16(buf[i*2:], yVal)
	}
	// Fill Cb plane
	cbOff := ySize * 2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(buf[cbOff+i*2:], cbVal)
	}
	// Fill Cr plane
	crOff := cbOff + chromaSize*2
	for i := 0; i < chromaSize; i++ {
		binary.LittleEndian.PutUint16(buf[crOff+i*2:], crVal)
	}
	return buf
}

func TestBlendPlayout10_Midpoint(t *testing.T) {
	w, h := 16, 8
	a := newIdleFrame10Filled(w, h, 200, 300, 700)
	b := newIdleFrame10Filled(w, h, 800, 700, 300)
	dst := make([]byte, w*h*4)

	err := BlendPlayout10(dst, a, b, w, h, 128) // 50/50 blend
	require.NoError(t, err)

	// Midpoint Y: (200*128 + 800*128 + 128) >> 8 = 500
	gotY := binary.LittleEndian.Uint16(dst[0:])
	require.Equal(t, uint16(500), gotY, "midpoint Y blend")

	// Midpoint Cb: (300*128 + 700*128 + 128) >> 8 = 500
	cbOff := w * h * 2
	gotCb := binary.LittleEndian.Uint16(dst[cbOff:])
	require.Equal(t, uint16(500), gotCb, "midpoint Cb blend")

	// Midpoint Cr: (700*128 + 300*128 + 128) >> 8 = 500
	crOff := cbOff + (w/2)*h*2
	gotCr := binary.LittleEndian.Uint16(dst[crOff:])
	require.Equal(t, uint16(500), gotCr, "midpoint Cr blend")
}

func TestBlendPlayout10_FullA(t *testing.T) {
	w, h := 16, 8
	a := newIdleFrame10Filled(w, h, 200, 300, 700)
	b := newIdleFrame10Filled(w, h, 800, 700, 300)
	dst := make([]byte, w*h*4)

	err := BlendPlayout10(dst, a, b, w, h, 0) // 100% A
	require.NoError(t, err)

	gotY := binary.LittleEndian.Uint16(dst[0:])
	require.Equal(t, uint16(200), gotY, "full A should give A's Y value")
}

func TestBlendPlayout10_FullB(t *testing.T) {
	w, h := 16, 8
	a := newIdleFrame10Filled(w, h, 200, 300, 700)
	b := newIdleFrame10Filled(w, h, 800, 700, 300)
	dst := make([]byte, w*h*4)

	err := BlendPlayout10(dst, a, b, w, h, 256) // 100% B
	require.NoError(t, err)

	gotY := binary.LittleEndian.Uint16(dst[0:])
	require.Equal(t, uint16(800), gotY, "full B should give B's Y value")
}

func TestBlendPlayout10_OddWidth(t *testing.T) {
	err := BlendPlayout10(nil, nil, nil, 15, 8, 128)
	require.Error(t, err, "odd width should fail for 10-bit 4:2:2")
}

func TestNewIdleLoop10_BlackFrame(t *testing.T) {
	w, h := 16, 8
	loop := NewIdleLoop10("", nil, w, h)
	defer loop.Close()

	frame := loop.NextFrame()
	require.NotNil(t, frame)
	require.Equal(t, w, frame.Width)
	require.Equal(t, h, frame.Height)
	require.Equal(t, w*h*4, len(frame.YUV))
	require.True(t, frame.IsKeyframe)

	// Verify 10-bit black values
	gotY := binary.LittleEndian.Uint16(frame.YUV[0:])
	require.Equal(t, uint16(64), gotY, "Y should be 10-bit black (64)")
}

func TestNewIdleLoop10_IncrementsPTS(t *testing.T) {
	w, h := 16, 8
	loop := NewIdleLoop10("", nil, w, h)
	defer loop.Close()

	f1 := loop.NextFrame()
	f2 := loop.NextFrame()
	require.True(t, f2.PTS > f1.PTS, "PTS should increment between frames")
}

func TestNewIdleLoop10_IndependentBuffers(t *testing.T) {
	w, h := 16, 8
	loop := NewIdleLoop10("", nil, w, h)
	defer loop.Close()

	f1 := loop.NextFrame()
	f2 := loop.NextFrame()

	// Each frame should have its own buffer (not shared reference)
	f1.YUV[0] = 0xFF
	require.NotEqual(t, f2.YUV[0], byte(0xFF), "frames should have independent buffers")
}

// The 10-bit idle loop plays its asset again in place, like IdleLoop.
func TestIdleLoop10_RewindsWithoutReopen(t *testing.T) {
	provider := newPassCountingProvider(NewFakeAssetProvider(makeTestFrames(3)))
	loop := NewIdleLoop10("test://idle.ts", provider, 4, 4)
	defer loop.Close()

	for i := 0; i < 12; i++ {
		require.Equal(t, byte(i%3+1), loop.NextFrame().YUV[0], "frame %d", i)
	}
	require.Equal(t, 1, provider.openCount("test://idle.ts"))
	require.Equal(t, 4, provider.passCount("test://idle.ts"))
}
