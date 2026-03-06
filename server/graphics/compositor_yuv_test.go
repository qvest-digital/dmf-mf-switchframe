package graphics

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompositorProcessYUV_Inactive(t *testing.T) {
	c := NewCompositor()
	yuv := make([]byte, 4*4*3/2)
	yuv[0] = 42 // distinctive value

	result := c.ProcessYUV(yuv, 4, 4)
	// Inactive compositor should return input unchanged
	require.Equal(t, yuv, result)
}

func TestCompositorProcessYUV_Active(t *testing.T) {
	c := NewCompositor()

	// Create a 4x4 RGBA overlay (red, semi-transparent)
	rgba := make([]byte, 4*4*4)
	for i := 0; i < len(rgba); i += 4 {
		rgba[i] = 255   // R
		rgba[i+1] = 0   // G
		rgba[i+2] = 0   // B
		rgba[i+3] = 128 // A (50%)
	}

	require.NoError(t, c.SetOverlay(rgba, 4, 4, "test"))
	require.NoError(t, c.On())

	// Create YUV input (all zeros = black)
	yuv := make([]byte, 4*4*3/2)

	result := c.ProcessYUV(yuv, 4, 4)
	// Result should be modified (overlay blended onto black)
	require.NotEqual(t, make([]byte, 4*4*3/2), result)
}

func TestCompositorProcessYUV_ResolutionMismatch(t *testing.T) {
	c := NewCompositor()

	rgba := make([]byte, 8*8*4) // 8x8 overlay
	for i := 3; i < len(rgba); i += 4 {
		rgba[i] = 255
	}
	require.NoError(t, c.SetOverlay(rgba, 8, 8, "test"))
	require.NoError(t, c.On())

	// 4x4 YUV frame — doesn't match 8x8 overlay
	yuv := make([]byte, 4*4*3/2)
	yuv[0] = 42

	result := c.ProcessYUV(yuv, 4, 4)
	// Should return input unchanged (resolution mismatch)
	require.Equal(t, yuv, result)
}

func TestCompositorProcessYUV_OddDimensions(t *testing.T) {
	c := NewCompositor()
	rgba := make([]byte, 4*4*4)
	for i := 3; i < len(rgba); i += 4 {
		rgba[i] = 255
	}
	require.NoError(t, c.SetOverlay(rgba, 4, 4, "test"))
	require.NoError(t, c.On())

	// Odd width
	yuv := []byte{1, 2, 3}
	result := c.ProcessYUV(yuv, 3, 4)
	require.Equal(t, yuv, result, "odd width should return input unchanged")

	// Odd height
	result = c.ProcessYUV(yuv, 4, 3)
	require.Equal(t, yuv, result, "odd height should return input unchanged")

	// Zero dimensions
	result = c.ProcessYUV(yuv, 0, 4)
	require.Equal(t, yuv, result, "zero width should return input unchanged")

	// Negative dimensions
	result = c.ProcessYUV(yuv, -2, 4)
	require.Equal(t, yuv, result, "negative width should return input unchanged")
}

func TestCompositorProcessYUV_FadePosition(t *testing.T) {
	c := NewCompositor()

	rgba := make([]byte, 4*4*4)
	for i := 0; i < len(rgba); i += 4 {
		rgba[i] = 255   // R
		rgba[i+1] = 0   // G
		rgba[i+2] = 0   // B
		rgba[i+3] = 255 // A (fully opaque)
	}
	require.NoError(t, c.SetOverlay(rgba, 4, 4, "test"))
	require.NoError(t, c.On())

	// Manually set fade position to nearly invisible
	c.mu.Lock()
	c.fadePosition = 0.001
	c.mu.Unlock()

	yuv := make([]byte, 4*4*3/2)
	for i := range yuv {
		yuv[i] = 100
	}

	result := c.ProcessYUV(yuv, 4, 4)
	// Very low fade → result should be nearly identical to input
	// (alpha < 1/255 threshold means no blending)
	require.Equal(t, yuv, result)
}
