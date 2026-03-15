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
	defer func() { _ = te.Stop(id) }()

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
	hasOverlay := len(layer.overlay) > 0
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

func TestTickerEngine_ConcurrentStartStop(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	c.SetResolutionProvider(func() (int, int) { return 1920, 1080 })

	renderer := newTestRenderer(t)
	te := NewTickerEngine(c, renderer)

	// Create several layers
	ids := make([]int, 4)
	for i := range ids {
		id, err := c.AddLayer()
		require.NoError(t, err)
		ids[i] = id
	}

	// Start and stop tickers concurrently — race detector should catch issues
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(layerID int) {
			defer wg.Done()
			cfg := TickerConfig{Text: "Race test", FontSize: 24, Speed: 200}
			for i := 0; i < 3; i++ {
				if err := te.Start(layerID, cfg); err != nil {
					continue
				}
				time.Sleep(20 * time.Millisecond)
				_ = te.Stop(layerID)
			}
		}(id)
	}
	wg.Wait()
}

func TestTickerEngine_LoopMode(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	c.SetResolutionProvider(func() (int, int) { return 1920, 1080 })

	renderer := newTestRenderer(t)
	te := NewTickerEngine(c, renderer)

	id, err := c.AddLayer()
	require.NoError(t, err)

	// Very fast ticker with loop — should still be running after enough time
	// for a non-loop ticker to complete
	err = te.Start(id, TickerConfig{
		Text:     "Hi",
		FontSize: 24,
		Speed:    50000, // extremely fast
		Loop:     true,
	})
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)
	require.True(t, te.IsRunning(id), "loop ticker should still be running")

	require.NoError(t, te.Stop(id))
}

func TestTickerEngine_NonLoopCompletion(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	c.SetResolutionProvider(func() (int, int) { return 1920, 1080 })
	c.SetPipelineFPS(30, 1)

	renderer := newTestRenderer(t)
	te := NewTickerEngine(c, renderer)

	id, err := c.AddLayer()
	require.NoError(t, err)

	// Very fast non-loop ticker — should complete naturally when driven by ProcessYUV.
	err = te.Start(id, TickerConfig{
		Text:     "Hi",
		FontSize: 24,
		Speed:    100000, // extremely fast — will scroll off quickly
		Loop:     false,
	})
	require.NoError(t, err)

	// Give goroutine time to set up the ticker state.
	time.Sleep(50 * time.Millisecond)

	// Drive the ticker via ProcessYUV calls until it scrolls off.
	yuv := make([]byte, 1920*1080*3/2)
	for i := 0; i < 200; i++ {
		c.ProcessYUV(yuv, 1920, 1080, nil, nil)
	}

	// Give goroutine time to observe the done signal.
	time.Sleep(50 * time.Millisecond)
	require.False(t, te.IsRunning(id), "non-loop ticker should complete naturally")
}

func TestExtractViewport_Normal(t *testing.T) {
	// Create a small strip: 20 pixels wide, 2 rows high
	strip := image.NewRGBA(image.Rect(0, 0, 20, 2))
	for i := range strip.Pix {
		strip.Pix[i] = byte(i % 256)
	}

	dst := make([]byte, 8*2*4) // viewport: 8 pixels wide, 2 rows
	extractTickerViewport(strip, 4, 8, 2, dst)

	// Verify data matches strip at offset 4
	for y := 0; y < 2; y++ {
		for x := 0; x < 8; x++ {
			srcOff := y*strip.Stride + (4+x)*4
			dstOff := y*8*4 + x*4
			require.Equal(t, strip.Pix[srcOff], dst[dstOff], "pixel mismatch at (%d,%d)", x, y)
		}
	}
}

func TestExtractViewport_ZeroOffset(t *testing.T) {
	strip := image.NewRGBA(image.Rect(0, 0, 10, 1))
	for i := range strip.Pix {
		strip.Pix[i] = byte(i % 256)
	}

	dst := make([]byte, 4*1*4) // 4 pixels wide
	extractTickerViewport(strip, 0, 4, 1, dst)

	for x := 0; x < 4; x++ {
		srcOff := x * 4
		dstOff := x * 4
		require.Equal(t, strip.Pix[srcOff], dst[dstOff])
	}
}

func TestExtractViewport_BoundaryOverflow(t *testing.T) {
	strip := image.NewRGBA(image.Rect(0, 0, 10, 1))
	for i := range strip.Pix {
		strip.Pix[i] = 42
	}

	dst := make([]byte, 8*1*4) // 8 pixels, but strip only has 10 - starts at 5 = 5 pixels available
	extractTickerViewport(strip, 5, 8, 1, dst)

	// First 5 pixels should be copied, rest should be zero/whatever was there
	for x := 0; x < 5; x++ {
		require.Equal(t, byte(42), dst[x*4], "pixel %d should be copied from strip", x)
	}
}

func TestTickerEngine_FrameLockedAdvancement(t *testing.T) {
	// The ticker position must advance by exactly (speed * fpsDen / fpsNum) pixels
	// per ProcessYUV call, producing perfectly smooth scrolling regardless of
	// wall-clock timing. This is the core invariant for frame-locked tickers.
	const (
		progW  = 320
		progH  = 240
		fpsNum = 24
		fpsDen = 1
		speed  = 120.0 // pixels per second → 5 pixels per frame at 24fps
	)

	c := NewCompositor()
	defer c.Close()
	c.SetResolutionProvider(func() (int, int) { return progW, progH })
	c.SetPipelineFPS(fpsNum, fpsDen)

	renderer := newTestRenderer(t)
	te := NewTickerEngine(c, renderer)

	id, err := c.AddLayer()
	require.NoError(t, err)

	err = te.Start(id, TickerConfig{
		Text:     "ABCDEFGHIJKLMNOPQRSTUVWXYZ 0123456789 The quick brown fox",
		FontSize: 20,
		Speed:    speed,
		Loop:     true,
	})
	require.NoError(t, err)
	defer func() { _ = te.Stop(id) }()

	// Give the goroutine time to render the strip and set up state.
	time.Sleep(50 * time.Millisecond)

	yuv := make([]byte, progW*progH*3/2)
	// Fill Y with 128 (mid-gray) so we can detect overlay changes.
	for i := 0; i < progW*progH; i++ {
		yuv[i] = 128
	}

	// Call ProcessYUV multiple times and capture the ticker's xOffset after each.
	// The position should advance by exactly speed * fpsDen / fpsNum = 5.0 pixels per frame.
	pixelsPerFrame := speed * float64(fpsDen) / float64(fpsNum)

	var positions []float64
	for frame := 0; frame < 10; frame++ {
		c.ProcessYUV(yuv, progW, progH, nil, nil)

		// Read the ticker state to verify position.
		c.mu.RLock()
		layer := c.layers[id]
		pos := 0.0
		if layer.ticker != nil {
			pos = layer.ticker.xOffset
		}
		c.mu.RUnlock()
		positions = append(positions, pos)
	}

	// Verify each frame advanced by exactly pixelsPerFrame.
	require.Greater(t, len(positions), 2, "need at least 2 positions to check deltas")
	for i := 1; i < len(positions); i++ {
		delta := positions[i] - positions[i-1]
		require.InDelta(t, pixelsPerFrame, delta, 0.001,
			"frame %d→%d: expected %.3f pixel advance, got %.3f",
			i-1, i, pixelsPerFrame, delta)
	}
}

func TestTickerEngine_FrameLockedMultipleFPS(t *testing.T) {
	// Verify the ticker produces the correct per-frame advancement
	// at different pipeline frame rates (rational num/den).
	for _, tc := range []struct {
		name   string
		fpsNum int
		fpsDen int
		speed  float64
		want   float64 // expected pixels per frame = speed * fpsDen / fpsNum
	}{
		{"24fps/120pps", 24, 1, 120, 5.0},
		{"30fps/120pps", 30, 1, 120, 4.0},
		{"60fps/120pps", 60, 1, 120, 2.0},
		{"25fps/100pps", 25, 1, 100, 4.0},
		{"29.97fps/150pps", 30000, 1001, 150, 150.0 * 1001.0 / 30000.0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := NewCompositor()
			defer c.Close()
			c.SetResolutionProvider(func() (int, int) { return 320, 240 })
			c.SetPipelineFPS(tc.fpsNum, tc.fpsDen)

			renderer := newTestRenderer(t)
			te := NewTickerEngine(c, renderer)

			id, err := c.AddLayer()
			require.NoError(t, err)

			err = te.Start(id, TickerConfig{
				Text:     "Test text for frame rate verification",
				FontSize: 20,
				Speed:    tc.speed,
				Loop:     true,
			})
			require.NoError(t, err)
			defer func() { _ = te.Stop(id) }()

			time.Sleep(50 * time.Millisecond)

			yuv := make([]byte, 320*240*3/2)

			// Advance 5 frames and check final position.
			for i := 0; i < 5; i++ {
				c.ProcessYUV(yuv, 320, 240, nil, nil)
			}

			c.mu.RLock()
			layer := c.layers[id]
			pos := 0.0
			if layer.ticker != nil {
				pos = layer.ticker.xOffset
			}
			c.mu.RUnlock()

			require.InDelta(t, 5*tc.want, pos, 0.01,
				"after 5 frames at %s: expected offset %.3f, got %.3f",
				tc.name, 5*tc.want, pos)
		})
	}
}
