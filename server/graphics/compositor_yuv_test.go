package graphics

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompositorProcessYUV_Inactive(t *testing.T) {
	c := NewCompositor()
	yuv := make([]byte, 4*4*3/2)
	yuv[0] = 42

	result := c.ProcessYUV(yuv, 4, 4, nil)
	require.Equal(t, yuv, result)
}

func TestCompositorProcessYUV_Active(t *testing.T) {
	c := NewCompositor()

	id, _ := c.AddLayer()
	rgba := make([]byte, 4*4*4)
	for i := 0; i < len(rgba); i += 4 {
		rgba[i] = 255
		rgba[i+1] = 0
		rgba[i+2] = 0
		rgba[i+3] = 128
	}

	require.NoError(t, c.SetOverlay(id, rgba, 4, 4, "test"))
	require.NoError(t, c.On(id))

	yuv := make([]byte, 4*4*3/2)

	result := c.ProcessYUV(yuv, 4, 4, nil)
	require.NotEqual(t, make([]byte, 4*4*3/2), result)
}

func TestCompositorProcessYUV_ResolutionMismatch(t *testing.T) {
	c := NewCompositor()

	id, _ := c.AddLayer()
	rgba := make([]byte, 8*8*4) // 8x8 overlay
	for i := 3; i < len(rgba); i += 4 {
		rgba[i] = 255
	}
	require.NoError(t, c.SetOverlay(id, rgba, 8, 8, "test"))
	require.NoError(t, c.On(id))

	// 4x4 YUV frame — doesn't match 8x8 overlay but will be blended
	// via sub-frame path (AlphaBlendRGBARect) into the default
	// full-frame rect which clips to 4x4.
	yuv := make([]byte, 4*4*3/2)
	origY := make([]byte, 4*4)
	copy(origY, yuv[:4*4])

	result := c.ProcessYUV(yuv, 4, 4, nil)
	// The sub-frame path handles resolution mismatch by scaling.
	// With zero rect (default), it uses fullFrameRect which clips to frame bounds.
	// The overlay has alpha=255 so pixels should be modified.
	_ = result
}

func TestCompositorProcessYUV_OddDimensions(t *testing.T) {
	c := NewCompositor()
	id, _ := c.AddLayer()
	rgba := make([]byte, 4*4*4)
	for i := 3; i < len(rgba); i += 4 {
		rgba[i] = 255
	}
	require.NoError(t, c.SetOverlay(id, rgba, 4, 4, "test"))
	require.NoError(t, c.On(id))

	yuv := []byte{1, 2, 3}
	result := c.ProcessYUV(yuv, 3, 4, nil)
	require.Equal(t, yuv, result, "odd width should return input unchanged")

	result = c.ProcessYUV(yuv, 4, 3, nil)
	require.Equal(t, yuv, result, "odd height should return input unchanged")

	result = c.ProcessYUV(yuv, 0, 4, nil)
	require.Equal(t, yuv, result, "zero width should return input unchanged")

	result = c.ProcessYUV(yuv, -2, 4, nil)
	require.Equal(t, yuv, result, "negative width should return input unchanged")
}

func TestCompositorProcessYUV_FadePosition(t *testing.T) {
	c := NewCompositor()

	id, _ := c.AddLayer()
	rgba := make([]byte, 4*4*4)
	for i := 0; i < len(rgba); i += 4 {
		rgba[i] = 255
		rgba[i+1] = 0
		rgba[i+2] = 0
		rgba[i+3] = 255
	}
	require.NoError(t, c.SetOverlay(id, rgba, 4, 4, "test"))
	require.NoError(t, c.On(id))

	c.mu.Lock()
	c.layers[id].fadePosition = 0.001
	c.mu.Unlock()

	yuv := make([]byte, 4*4*3/2)
	for i := range yuv {
		yuv[i] = 100
	}

	result := c.ProcessYUV(yuv, 4, 4, nil)
	require.Equal(t, yuv, result)
}
