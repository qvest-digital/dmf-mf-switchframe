package stmap

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewSTMap_ValidDimensions(t *testing.T) {
	m, err := NewSTMap("test", 4, 2)
	require.NoError(t, err)
	require.Equal(t, "test", m.Name)
	require.Equal(t, 4, m.Width)
	require.Equal(t, 2, m.Height)
	require.Len(t, m.S, 4*2)
	require.Len(t, m.T, 4*2)

	// All values should be zero-initialized.
	for i := range m.S {
		require.Zero(t, m.S[i])
		require.Zero(t, m.T[i])
	}
}

func TestNewSTMap_ZeroDimension(t *testing.T) {
	_, err := NewSTMap("test", 0, 2)
	require.ErrorIs(t, err, ErrInvalidDimensions)

	_, err = NewSTMap("test", 4, 0)
	require.ErrorIs(t, err, ErrInvalidDimensions)

	_, err = NewSTMap("test", 0, 0)
	require.ErrorIs(t, err, ErrInvalidDimensions)
}

func TestNewSTMap_NegativeDimension(t *testing.T) {
	_, err := NewSTMap("test", -2, 4)
	require.ErrorIs(t, err, ErrInvalidDimensions)

	_, err = NewSTMap("test", 4, -2)
	require.ErrorIs(t, err, ErrInvalidDimensions)
}

func TestNewSTMap_OddDimension(t *testing.T) {
	_, err := NewSTMap("test", 3, 2)
	require.ErrorIs(t, err, ErrInvalidDimensions)

	_, err = NewSTMap("test", 4, 3)
	require.ErrorIs(t, err, ErrInvalidDimensions)

	_, err = NewSTMap("test", 5, 7)
	require.ErrorIs(t, err, ErrInvalidDimensions)
}

func TestSTMap_Identity(t *testing.T) {
	m := Identity(4, 2)
	require.Equal(t, "identity", m.Name)
	require.Equal(t, 4, m.Width)
	require.Equal(t, 2, m.Height)
	require.Len(t, m.S, 4*2)
	require.Len(t, m.T, 4*2)

	// Verify pixel-center coordinates at corners.
	// Top-left (0,0): S = (0+0.5)/4 = 0.125, T = (0+0.5)/2 = 0.25
	require.InDelta(t, 0.125, m.S[0], 1e-6)
	require.InDelta(t, 0.25, m.T[0], 1e-6)

	// Top-right (3,0): S = (3+0.5)/4 = 0.875, T = (0+0.5)/2 = 0.25
	require.InDelta(t, 0.875, m.S[3], 1e-6)
	require.InDelta(t, 0.25, m.T[3], 1e-6)

	// Bottom-left (0,1): S = (0+0.5)/4 = 0.125, T = (1+0.5)/2 = 0.75
	require.InDelta(t, 0.125, m.S[4], 1e-6)
	require.InDelta(t, 0.75, m.T[4], 1e-6)

	// Bottom-right (3,1): S = (3+0.5)/4 = 0.875, T = (1+0.5)/2 = 0.75
	require.InDelta(t, 0.875, m.S[7], 1e-6)
	require.InDelta(t, 0.75, m.T[7], 1e-6)
}

func TestAnimatedSTMap_FrameAt(t *testing.T) {
	f0 := Identity(4, 2)
	f0.Name = "frame0"
	f1 := Identity(4, 2)
	f1.Name = "frame1"
	f2 := Identity(4, 2)
	f2.Name = "frame2"

	anim := NewAnimatedSTMap("anim", []*STMap{f0, f1, f2}, 30)
	require.Equal(t, "anim", anim.Name)
	require.Equal(t, 30, anim.FPS)
	require.Len(t, anim.Frames, 3)

	// Normal indexing.
	require.Equal(t, f0, anim.FrameAt(0))
	require.Equal(t, f1, anim.FrameAt(1))
	require.Equal(t, f2, anim.FrameAt(2))

	// Wrap-around.
	require.Equal(t, f0, anim.FrameAt(3))
	require.Equal(t, f1, anim.FrameAt(4))
	require.Equal(t, f2, anim.FrameAt(5))

	// Large index.
	require.Equal(t, f1, anim.FrameAt(100))
}

func TestAnimatedSTMap_Advance(t *testing.T) {
	f0 := Identity(4, 2)
	f0.Name = "frame0"
	f1 := Identity(4, 2)
	f1.Name = "frame1"

	anim := NewAnimatedSTMap("anim", []*STMap{f0, f1}, 30)

	// CurrentFrame starts at index 0.
	require.Equal(t, f0, anim.CurrentFrame())

	// Advance increments and returns the new current frame.
	got := anim.Advance()
	require.Equal(t, f1, got)

	// CurrentFrame reflects advanced position.
	require.Equal(t, f1, anim.CurrentFrame())

	// Advance wraps around.
	got = anim.Advance()
	require.Equal(t, f0, got)
}

func TestAnimatedSTMap_CurrentFrame_DoesNotAdvance(t *testing.T) {
	f0 := Identity(4, 2)
	f1 := Identity(4, 2)

	anim := NewAnimatedSTMap("anim", []*STMap{f0, f1}, 30)

	// Calling CurrentFrame multiple times should not advance.
	require.Equal(t, f0, anim.CurrentFrame())
	require.Equal(t, f0, anim.CurrentFrame())
	require.Equal(t, f0, anim.CurrentFrame())
}

func TestValidateName(t *testing.T) {
	// Valid names.
	require.NoError(t, ValidateName("barrel"))
	require.NoError(t, ValidateName("my-map"))
	require.NoError(t, ValidateName("map_v2"))
	require.NoError(t, ValidateName("a"))

	// Invalid: empty.
	require.ErrorIs(t, ValidateName(""), ErrInvalidName)

	// Invalid: dots.
	require.ErrorIs(t, ValidateName("."), ErrInvalidName)
	require.ErrorIs(t, ValidateName(".."), ErrInvalidName)

	// Invalid: path separators.
	require.ErrorIs(t, ValidateName("a/b"), ErrInvalidName)
	require.ErrorIs(t, ValidateName("a\\b"), ErrInvalidName)

	// Invalid: path traversal.
	require.ErrorIs(t, ValidateName("a/../b"), ErrInvalidName)
}
