package graphics

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/graphics/textrender"
)

func newTestTextRenderer(t *testing.T) *textrender.Renderer {
	t.Helper()
	r, err := textrender.NewRenderer()
	require.NoError(t, err)
	return r
}

func TestTextAnimationEngine_Typewriter(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	c.SetResolutionProvider(func() (int, int) { return 1920, 1080 })

	renderer := newTestTextRenderer(t)
	tae := NewTextAnimationEngine(c, renderer)
	defer tae.Close()

	id, err := c.AddLayer()
	require.NoError(t, err)

	err = tae.Start(id, TextAnimationConfig{
		Mode:        "typewriter",
		Text:        "Hello",
		FontSize:    32,
		CharsPerSec: 20,
	})
	require.NoError(t, err)
	require.True(t, tae.IsRunning(id))

	// Wait for animation to play (5 chars at 20/sec = 0.25s)
	time.Sleep(500 * time.Millisecond)

	// Should have completed and stopped
	require.False(t, tae.IsRunning(id), "typewriter should auto-stop after revealing all characters")
}

func TestTextAnimationEngine_FadeWord(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	c.SetResolutionProvider(func() (int, int) { return 1920, 1080 })

	renderer := newTestTextRenderer(t)
	tae := NewTextAnimationEngine(c, renderer)
	defer tae.Close()

	id, err := c.AddLayer()
	require.NoError(t, err)

	err = tae.Start(id, TextAnimationConfig{
		Mode:           "fade-word",
		Text:           "Hello World",
		FontSize:       32,
		WordDelayMs:    100,
		FadeDurationMs: 50,
	})
	require.NoError(t, err)
	require.True(t, tae.IsRunning(id))

	// Wait for animation (2 words x 100ms delay + 50ms fade = ~250ms)
	time.Sleep(500 * time.Millisecond)

	require.False(t, tae.IsRunning(id), "fade-word should auto-stop after all words revealed")
}

func TestTextAnimationEngine_StopManual(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	c.SetResolutionProvider(func() (int, int) { return 1920, 1080 })

	renderer := newTestTextRenderer(t)
	tae := NewTextAnimationEngine(c, renderer)
	defer tae.Close()

	id, err := c.AddLayer()
	require.NoError(t, err)

	err = tae.Start(id, TextAnimationConfig{
		Mode:        "typewriter",
		Text:        "A very long text that will take a while to type out",
		FontSize:    32,
		CharsPerSec: 2, // very slow
	})
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	require.NoError(t, tae.Stop(id))
	require.False(t, tae.IsRunning(id))
}

func TestTextAnimationEngine_RejectDuplicate(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	c.SetResolutionProvider(func() (int, int) { return 1920, 1080 })

	renderer := newTestTextRenderer(t)
	tae := NewTextAnimationEngine(c, renderer)
	defer tae.Close()

	id, err := c.AddLayer()
	require.NoError(t, err)

	require.NoError(t, tae.Start(id, TextAnimationConfig{
		Mode: "typewriter", Text: "A", FontSize: 24, CharsPerSec: 1,
	}))

	err = tae.Start(id, TextAnimationConfig{
		Mode: "typewriter", Text: "B", FontSize: 24, CharsPerSec: 1,
	})
	require.ErrorIs(t, err, ErrTextAnimActive)
}

func TestTextAnimationEngine_StopNonExistent(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	renderer := newTestTextRenderer(t)
	tae := NewTextAnimationEngine(c, renderer)

	err := tae.Stop(999)
	require.ErrorIs(t, err, ErrTextAnimNotFound)
}
