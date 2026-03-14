package graphics

import (
	"image"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/graphics/textrender"
)

func newTestRenderer(t *testing.T) *textrender.Renderer {
	t.Helper()
	r, err := textrender.NewRenderer()
	require.NoError(t, err)
	return r
}

func TestTickerEngine_StartStop(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	c.SetResolutionProvider(func() (int, int) { return 1920, 1080 })

	renderer := newTestRenderer(t)
	te := NewTickerEngine(c, renderer)

	id, err := c.AddLayer()
	require.NoError(t, err)

	// Start ticker
	err = te.Start(id, TickerConfig{
		Text:     "Breaking News: Hello World",
		FontSize: 32,
		Speed:    100, // pixels per second
	})
	require.NoError(t, err)

	// Ticker should be running
	require.True(t, te.IsRunning(id))

	// Stop ticker
	err = te.Stop(id)
	require.NoError(t, err)
	require.False(t, te.IsRunning(id))
}

func TestTickerEngine_RejectDuplicateStart(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	c.SetResolutionProvider(func() (int, int) { return 1920, 1080 })

	renderer := newTestRenderer(t)
	te := NewTickerEngine(c, renderer)

	id, err := c.AddLayer()
	require.NoError(t, err)

	require.NoError(t, te.Start(id, TickerConfig{Text: "Test", FontSize: 24, Speed: 100}))
	defer te.Stop(id)

	// Starting again should fail
	err = te.Start(id, TickerConfig{Text: "Test 2", FontSize: 24, Speed: 100})
	require.ErrorIs(t, err, ErrTickerActive)
}

func TestTickerEngine_StopNonExistent(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	renderer := newTestRenderer(t)
	te := NewTickerEngine(c, renderer)

	err := te.Stop(999)
	require.ErrorIs(t, err, ErrTickerNotFound)
}

func TestTickerEngine_SetsOverlay(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	c.SetResolutionProvider(func() (int, int) { return 1920, 1080 })

	renderer := newTestRenderer(t)
	te := NewTickerEngine(c, renderer)

	id, err := c.AddLayer()
	require.NoError(t, err)

	// Need to activate the layer to see overlay effects
	rgba := make([]byte, 1920*1080*4)
	require.NoError(t, c.SetOverlay(id, rgba, 1920, 1080, "test"))

	err = te.Start(id, TickerConfig{
		Text:     "Test ticker text that should render",
		FontSize: 32,
		Speed:    200,
	})
	require.NoError(t, err)

	// Wait for a few frames to be rendered
	time.Sleep(100 * time.Millisecond)

	// Verify the compositor has received an overlay update
	c.mu.RLock()
	layer := c.layers[id]
	hasOverlay := layer.overlay != nil && len(layer.overlay) > 0
	c.mu.RUnlock()
	require.True(t, hasOverlay, "ticker should have set overlay data on the layer")

	require.NoError(t, te.Stop(id))
}

func TestTickerEngine_UpdateText(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	c.SetResolutionProvider(func() (int, int) { return 1920, 1080 })

	renderer := newTestRenderer(t)
	te := NewTickerEngine(c, renderer)

	id, err := c.AddLayer()
	require.NoError(t, err)

	require.NoError(t, te.Start(id, TickerConfig{Text: "Original", FontSize: 24, Speed: 100}))

	// Update text
	require.NoError(t, te.UpdateText(id, "Updated text"))
	require.True(t, te.IsRunning(id))

	// Wait for new render
	time.Sleep(100 * time.Millisecond)

	require.NoError(t, te.Stop(id))
}

func TestTickerEngine_SetsLayerRect(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	c.SetResolutionProvider(func() (int, int) { return 1920, 1080 })

	renderer := newTestRenderer(t)
	te := NewTickerEngine(c, renderer)

	id, err := c.AddLayer()
	require.NoError(t, err)

	err = te.Start(id, TickerConfig{
		Text:     "Ticker rect test",
		FontSize: 24,
		Speed:    100,
	})
	require.NoError(t, err)

	// Wait for the ticker goroutine to set the rect
	time.Sleep(100 * time.Millisecond)

	// Verify the layer rect is positioned at the bottom, not full-frame
	c.mu.RLock()
	layer := c.layers[id]
	rect := layer.rect
	c.mu.RUnlock()

	require.NotEqual(t, image.Rectangle{}, rect, "ticker should set layer rect")
	require.Equal(t, 0, rect.Min.X)
	require.Equal(t, 1920, rect.Max.X)
	// Rect should be at the bottom of the screen, not full height
	require.Greater(t, rect.Min.Y, 0, "ticker rect should not start at top")
	require.Equal(t, rect.Max.Y, 1080, "ticker rect should end at bottom")
	barH := rect.Dy()
	require.Less(t, barH, 200, "ticker bar should be much smaller than full frame")

	require.NoError(t, te.Stop(id))

	// After stop, rect should be reset to zero (full-frame)
	c.mu.RLock()
	rectAfter := c.layers[id].rect
	c.mu.RUnlock()
	require.Equal(t, image.Rectangle{}, rectAfter, "ticker stop should reset layer rect to full-frame")
}

func TestTickerEngine_Close(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	c.SetResolutionProvider(func() (int, int) { return 1920, 1080 })

	renderer := newTestRenderer(t)
	te := NewTickerEngine(c, renderer)

	id1, _ := c.AddLayer()
	id2, _ := c.AddLayer()

	require.NoError(t, te.Start(id1, TickerConfig{Text: "A", FontSize: 24, Speed: 100}))
	require.NoError(t, te.Start(id2, TickerConfig{Text: "B", FontSize: 24, Speed: 100}))

	te.Close()

	require.False(t, te.IsRunning(id1))
	require.False(t, te.IsRunning(id2))
}

func TestTickerEngine_ConcurrentUpdateTextAndStop(t *testing.T) {
	// Regression test: UpdateText and Stop racing must not double-close the cancel channel.
	c := NewCompositor()
	defer c.Close()
	c.SetResolutionProvider(func() (int, int) { return 1920, 1080 })

	renderer := newTestRenderer(t)

	for i := 0; i < 20; i++ {
		te := NewTickerEngine(c, renderer)
		id, err := c.AddLayer()
		require.NoError(t, err)

		require.NoError(t, te.Start(id, TickerConfig{Text: "Race test", FontSize: 24, Speed: 100}))
		time.Sleep(10 * time.Millisecond) // let the goroutine start

		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			_ = te.UpdateText(id, "Updated text")
		}()
		go func() {
			defer wg.Done()
			_ = te.Stop(id)
		}()

		wg.Wait()
		te.Close()
		_ = c.RemoveLayer(id)
	}
}

func TestTickerEngine_CloseIdempotent(t *testing.T) {
	// Calling Close multiple times must not panic.
	c := NewCompositor()
	defer c.Close()
	c.SetResolutionProvider(func() (int, int) { return 1920, 1080 })

	renderer := newTestRenderer(t)
	te := NewTickerEngine(c, renderer)

	id, _ := c.AddLayer()
	require.NoError(t, te.Start(id, TickerConfig{Text: "A", FontSize: 24, Speed: 100}))

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			te.Close()
		}()
	}
	wg.Wait()

	require.False(t, te.IsRunning(id))
}

func TestTickerEngine_ConcurrentCloseAndStop(t *testing.T) {
	// Close and Stop racing must not panic from double-close.
	c := NewCompositor()
	defer c.Close()
	c.SetResolutionProvider(func() (int, int) { return 1920, 1080 })

	renderer := newTestRenderer(t)

	for i := 0; i < 20; i++ {
		te := NewTickerEngine(c, renderer)
		id, err := c.AddLayer()
		require.NoError(t, err)

		require.NoError(t, te.Start(id, TickerConfig{Text: "Close race", FontSize: 24, Speed: 100}))
		time.Sleep(10 * time.Millisecond)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			te.Close()
		}()
		go func() {
			defer wg.Done()
			_ = te.Stop(id)
		}()
		wg.Wait()
		_ = c.RemoveLayer(id)
	}
}
