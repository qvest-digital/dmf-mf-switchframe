package graphics

import (
	"sync"
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

func TestTextAnimationEngine_CloseIdempotent(t *testing.T) {
	// Calling Close multiple times must not panic.
	c := NewCompositor()
	defer c.Close()
	c.SetResolutionProvider(func() (int, int) { return 1920, 1080 })

	renderer := newTestTextRenderer(t)
	tae := NewTextAnimationEngine(c, renderer)

	id, _ := c.AddLayer()
	require.NoError(t, tae.Start(id, TextAnimationConfig{
		Mode: "typewriter", Text: "Close test", FontSize: 24, CharsPerSec: 1,
	}))

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tae.Close()
		}()
	}
	wg.Wait()

	require.False(t, tae.IsRunning(id))
}

func TestTextAnimationEngine_ConcurrentCloseAndStop(t *testing.T) {
	// Close and Stop racing must not panic from double-close.
	c := NewCompositor()
	defer c.Close()
	c.SetResolutionProvider(func() (int, int) { return 1920, 1080 })

	renderer := newTestTextRenderer(t)

	for i := 0; i < 20; i++ {
		tae := NewTextAnimationEngine(c, renderer)
		id, err := c.AddLayer()
		require.NoError(t, err)

		require.NoError(t, tae.Start(id, TextAnimationConfig{
			Mode: "typewriter", Text: "Race test text", FontSize: 24, CharsPerSec: 1,
		}))
		time.Sleep(10 * time.Millisecond)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			tae.Close()
		}()
		go func() {
			defer wg.Done()
			_ = tae.Stop(id)
		}()
		wg.Wait()
		_ = c.RemoveLayer(id)
	}
}

func TestTextAnimEngine_ConcurrentStartStop(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	c.SetResolutionProvider(func() (int, int) { return 1920, 1080 })

	renderer := newTestTextRenderer(t)
	tae := NewTextAnimationEngine(c, renderer)
	defer tae.Close()

	ids := make([]int, 4)
	for i := range ids {
		id, err := c.AddLayer()
		require.NoError(t, err)
		ids[i] = id
	}

	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(layerID int) {
			defer wg.Done()
			cfg := TextAnimationConfig{
				Mode: "typewriter", Text: "Race test words", FontSize: 24, CharsPerSec: 100,
			}
			for i := 0; i < 3; i++ {
				if err := tae.Start(layerID, cfg); err != nil {
					continue
				}
				time.Sleep(20 * time.Millisecond)
				_ = tae.Stop(layerID)
			}
		}(id)
	}
	wg.Wait()
}

func TestTextAnimEngine_StopDuringAnimation(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	c.SetResolutionProvider(func() (int, int) { return 1920, 1080 })

	renderer := newTestTextRenderer(t)
	tae := NewTextAnimationEngine(c, renderer)
	defer tae.Close()

	id, err := c.AddLayer()
	require.NoError(t, err)

	// Start a slow fade-word animation
	err = tae.Start(id, TextAnimationConfig{
		Mode:           "fade-word",
		Text:           "Many words in this sentence to animate slowly",
		FontSize:       32,
		WordDelayMs:    500,
		FadeDurationMs: 300,
	})
	require.NoError(t, err)

	// Stop immediately — should complete cleanly without panic or deadlock
	time.Sleep(30 * time.Millisecond) // let one frame render
	require.NoError(t, tae.Stop(id))
	require.False(t, tae.IsRunning(id))
}

func TestSplitWordsForAnim_Empty(t *testing.T) {
	require.Empty(t, splitWordsForAnim(""))
}

func TestSplitWordsForAnim_SingleWord(t *testing.T) {
	require.Equal(t, []string{"hello"}, splitWordsForAnim("hello"))
}

func TestSplitWordsForAnim_MultipleSpaces(t *testing.T) {
	// Double spaces should not produce empty words
	result := splitWordsForAnim("hello  world")
	require.Equal(t, []string{"hello", "world"}, result)
}

func TestSplitWordsForAnim_LeadingTrailingSpaces(t *testing.T) {
	result := splitWordsForAnim(" hello world ")
	require.Equal(t, []string{"hello", "world"}, result)
}
