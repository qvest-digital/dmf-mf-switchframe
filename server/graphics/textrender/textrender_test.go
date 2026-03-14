package textrender

import (
	"image/color"
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

func TestMeasureText_WordWrap(t *testing.T) {
	r, err := NewRenderer()
	require.NoError(t, err)

	// Without wrap: single long line
	wNoWrap, hNoWrap := r.MeasureText(TextOptions{
		Text:     "The quick brown fox jumps over the lazy dog",
		FontSize: 24,
	})

	// With narrow max width: should wrap to multiple lines
	wWrapped, hWrapped := r.MeasureText(TextOptions{
		Text:     "The quick brown fox jumps over the lazy dog",
		FontSize: 24,
		MaxWidth: wNoWrap / 3,
	})

	require.Less(t, wWrapped, wNoWrap, "wrapped width should be less than unwrapped")
	require.Greater(t, hWrapped, hNoWrap, "wrapped height should be greater (more lines)")
}

func TestRenderToRGBA(t *testing.T) {
	r, err := NewRenderer()
	require.NoError(t, err)

	img, rgba, err := r.RenderToRGBA(TextOptions{
		Text:     "Test",
		FontSize: 48,
	})
	require.NoError(t, err)
	require.NotNil(t, img)
	require.NotEmpty(t, rgba)

	// RGBA data should be width * height * 4
	bounds := img.Bounds()
	require.Equal(t, bounds.Dx()*bounds.Dy()*4, len(rgba))

	// At least some pixels should be non-zero (text was rendered)
	hasContent := false
	for i := 3; i < len(rgba); i += 4 { // check alpha channel
		if rgba[i] > 0 {
			hasContent = true
			break
		}
	}
	require.True(t, hasContent, "rendered image should contain visible text")
}

func TestRenderText_FixedDimensions(t *testing.T) {
	r, err := NewRenderer()
	require.NoError(t, err)

	img, rgba, err := r.RenderText(200, 50, TextOptions{
		Text:     "Hello",
		FontSize: 24,
	})
	require.NoError(t, err)
	require.Equal(t, 200, img.Bounds().Dx())
	require.Equal(t, 50, img.Bounds().Dy())
	require.Equal(t, 200*50*4, len(rgba))
}

func TestRenderText_InvalidDimensions(t *testing.T) {
	r, err := NewRenderer()
	require.NoError(t, err)

	_, _, err = r.RenderText(0, 50, TextOptions{Text: "x", FontSize: 24})
	require.Error(t, err)

	_, _, err = r.RenderText(50, -1, TextOptions{Text: "x", FontSize: 24})
	require.Error(t, err)
}

func TestRenderText_CustomColor(t *testing.T) {
	r, err := NewRenderer()
	require.NoError(t, err)

	_, rgba, err := r.RenderToRGBA(TextOptions{
		Text:     "Red",
		FontSize: 48,
		Color:    color.RGBA{R: 255, G: 0, B: 0, A: 255},
	})
	require.NoError(t, err)

	// Find a non-transparent pixel and verify it has red channel
	for i := 0; i < len(rgba); i += 4 {
		if rgba[i+3] > 0 { // alpha > 0
			require.Greater(t, rgba[i], uint8(0), "red channel should be non-zero for red text")
			break
		}
	}
}

func TestWordWrap(t *testing.T) {
	r, err := NewRenderer()
	require.NoError(t, err)

	r.mu.Lock()
	face, err := r.faceLocked(24, false)
	r.mu.Unlock()
	require.NoError(t, err)

	lines := wordWrap(face, "a b c", 10000)
	require.Equal(t, []string{"a b c"}, lines, "wide width should not wrap")

	lines = wordWrap(face, "a b c", 1)
	require.Equal(t, 3, len(lines), "narrow width should wrap each word")
}

func TestFontFaceCache(t *testing.T) {
	r, err := NewRenderer()
	require.NoError(t, err)

	r.mu.Lock()

	// First call creates a new face
	face1, err := r.faceLocked(24, false)
	require.NoError(t, err)

	// Second call should return the same cached face
	face2, err := r.faceLocked(24, false)
	require.NoError(t, err)
	require.Equal(t, face1, face2, "same size should return cached face")

	// Different size should create a new face
	face3, err := r.faceLocked(48, false)
	require.NoError(t, err)
	require.NotEqual(t, face1, face3, "different size should create new face")

	// Bold variant should be separate
	face4, err := r.faceLocked(24, true)
	require.NoError(t, err)
	require.NotEqual(t, face1, face4, "bold should be separate from regular")

	r.mu.Unlock()
}

func BenchmarkRenderText(b *testing.B) {
	r, err := NewRenderer()
	if err != nil {
		b.Fatal(err)
	}

	opts := TextOptions{
		Text:     "Breaking News: The quick brown fox jumps over the lazy dog",
		FontSize: 32,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = r.RenderToRGBA(opts)
	}
}
