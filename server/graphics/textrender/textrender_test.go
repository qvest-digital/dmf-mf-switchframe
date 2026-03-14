package textrender

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewRenderer(t *testing.T) {
	r, err := NewRenderer()
	require.NoError(t, err)
	require.NotNil(t, r)
}

func TestMeasureText(t *testing.T) {
	r, err := NewRenderer()
	require.NoError(t, err)

	w, h := r.MeasureText(TextOptions{
		Text:     "Hello World",
		FontSize: 48,
		Bold:     false,
	})
	require.Greater(t, w, 0, "width should be positive")
	require.Greater(t, h, 0, "height should be positive")
}

func TestMeasureText_Bold(t *testing.T) {
	r, err := NewRenderer()
	require.NoError(t, err)

	wRegular, _ := r.MeasureText(TextOptions{
		Text:     "Hello",
		FontSize: 48,
	})
	wBold, _ := r.MeasureText(TextOptions{
		Text:     "Hello",
		FontSize: 48,
		Bold:     true,
	})
	// Bold text is typically wider
	require.GreaterOrEqual(t, wBold, wRegular)
}

func TestMeasureText_EmptyString(t *testing.T) {
	r, err := NewRenderer()
	require.NoError(t, err)

	w, h := r.MeasureText(TextOptions{
		Text:     "",
		FontSize: 48,
	})
	require.Equal(t, 0, w)
	require.Greater(t, h, 0, "height should reflect font size even for empty text")
}
