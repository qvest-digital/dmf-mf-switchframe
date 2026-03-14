package graphics

import (
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
