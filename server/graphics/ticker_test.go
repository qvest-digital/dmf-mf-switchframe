package graphics

import (
	"testing"

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
