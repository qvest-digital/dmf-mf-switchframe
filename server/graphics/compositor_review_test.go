package graphics

import (
	"encoding/binary"
	"image"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestCompositorProcessYUV10_SubFrameFadeLinear is a regression test for the
// "fade applied twice" bug in ProcessYUV10's sub-frame branch. Previously the
// overlay alpha was pre-scaled by fadePosition via applyFadeAlpha AND
// AlphaBlendRGBARect10 re-applied fadePosition as alphaScale, squaring the fade
// (at fadePosition=0.5 the overlay composited at ~0.25). The blend must be
// linear in fadePosition.
func TestCompositorProcessYUV10_SubFrameFadeLinear(t *testing.T) {
	c := NewCompositor()

	id, err := c.AddLayer()
	require.NoError(t, err)

	overlayW, overlayH := 100, 50
	rgba := make([]byte, overlayW*overlayH*4)
	for i := 0; i < overlayW*overlayH; i++ {
		rgba[i*4+0] = 255 // R
		rgba[i*4+1] = 255 // G
		rgba[i*4+2] = 255 // B (white => Y10 ~= 940)
		rgba[i*4+3] = 255 // A (opaque)
	}
	require.NoError(t, c.SetOverlay(id, rgba, overlayW, overlayH, "test"))
	require.NoError(t, c.On(id))
	require.NoError(t, c.SetLayerRect(id, image.Rect(200, 100, 300, 150)))

	// Mid-fade: fadePosition = 0.5.
	c.mu.Lock()
	c.layers[id].fadePosition = 0.5
	c.mu.Unlock()

	frameW, frameH := 1920, 1080
	// Background Y=64 (10-bit black), neutral chroma.
	yuv := make10bitGraphicsFrame(frameW, frameH, 64, 512, 512)

	result := c.ProcessYUV10(yuv, frameW, frameH)

	// Pixel inside the rect. Correct linear blend of white(940) over black(64)
	// at alpha 0.5 is ~502. The squared bug would give ~283.
	insideY := int(binary.LittleEndian.Uint16(result[(110*frameW+210)*2:]))
	require.InDelta(t, 502, insideY, 30,
		"sub-frame fade must be linear (~502), not squared (~283); got %d", insideY)
	require.Greater(t, insideY, 350,
		"sub-frame fade applied twice (squared) regression: got %d", insideY)
}

// TestCompositorCancelFadeConcurrent is a regression test for the double-close
// panic in cancelLayerFadeLocked/cancelLayerAnimationLocked. Two concurrent
// Off/RemoveLayer callers on the same layer while an AutoOn fade is in flight
// previously raced to close(layer.fadeCancel) twice ("panic: close of closed
// channel"). The cancel helpers must nil the channel under lock before waiting.
func TestCompositorCancelFadeConcurrent(t *testing.T) {
	for iter := 0; iter < 50; iter++ {
		c := NewCompositor()
		id, err := c.AddLayer()
		require.NoError(t, err)

		rgba := make([]byte, 4*4*4)
		for i := 0; i < len(rgba); i++ {
			rgba[i] = 255
		}
		require.NoError(t, c.SetOverlay(id, rgba, 4, 4, "test"))

		// Start a long fade so it is in flight while we cancel concurrently.
		require.NoError(t, c.AutoOn(id, 5*time.Second))

		// Give the fade goroutine a moment to start selecting on fadeCancel.
		time.Sleep(time.Millisecond)

		var wg sync.WaitGroup
		// Multiple concurrent cancellers via the unguarded public entry points.
		for g := 0; g < 4; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = c.Off(id)
			}()
		}
		wg.Wait()
	}
}

// TestCompositorCancelFadeMixedConcurrent stresses the cancel-channel lifecycle
// with mixed Off / RemoveLayer / On callers racing against an in-flight AutoOn
// fade. All three public entry points route through cancelLayerFadeLocked, so
// without the channel being niled under lock before the <-done wait, two of them
// could close(layer.fadeCancel) twice ("panic: close of closed channel"). Run
// under -race; a clean pass means the lifecycle is sound.
func TestCompositorCancelFadeMixedConcurrent(t *testing.T) {
	for iter := 0; iter < 50; iter++ {
		c := NewCompositor()
		id, err := c.AddLayer()
		require.NoError(t, err)

		rgba := make([]byte, 4*4*4)
		for i := 0; i < len(rgba); i++ {
			rgba[i] = 255
		}
		require.NoError(t, c.SetOverlay(id, rgba, 4, 4, "test"))

		// Long fade so it is in flight while we mutate the layer concurrently.
		require.NoError(t, c.AutoOn(id, 5*time.Second))
		time.Sleep(time.Millisecond)

		var wg sync.WaitGroup
		// Mixed concurrent operations on the same layer: Off, RemoveLayer, On.
		// Errors are expected and ignored (layer may already be removed); the
		// invariant under test is that none of these panics on a double-close.
		ops := []func(){
			func() { _ = c.Off(id) },
			func() { _ = c.RemoveLayer(id) },
			func() { _ = c.On(id) },
			func() { _ = c.Off(id) },
		}
		for _, op := range ops {
			wg.Add(1)
			go func(fn func()) {
				defer wg.Done()
				fn()
			}(op)
		}
		wg.Wait()
	}
}

// TestCompositorCancelAnimationConcurrent exercises the animation cancel path
// (cancelLayerAnimationLocked) under the same concurrent-cancel scenario.
func TestCompositorCancelAnimationConcurrent(t *testing.T) {
	for iter := 0; iter < 50; iter++ {
		c := NewCompositor()
		id, err := c.AddLayer()
		require.NoError(t, err)

		rgba := make([]byte, 4*4*4)
		for i := 0; i < len(rgba); i++ {
			rgba[i] = 255
		}
		require.NoError(t, c.SetOverlay(id, rgba, 4, 4, "test"))
		require.NoError(t, c.On(id))

		// Start a pulse animation so animCancel is live.
		require.NoError(t, c.Animate(id, AnimationConfig{
			Mode:     "pulse",
			SpeedHz:  1.0,
			MinAlpha: 0.2,
			MaxAlpha: 1.0,
		}))

		time.Sleep(time.Millisecond)

		var wg sync.WaitGroup
		for g := 0; g < 4; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = c.Off(id)
			}()
		}
		wg.Wait()
	}
}

// TestCompositorCancelAnimationMixedConcurrent stresses RemoveLayer racing
// against Off/StopAnimation while a pulse animation is in flight. The animation
// goroutine's deferred cleanup must close animDone even when RemoveLayer has
// already deleted the layer from the map — otherwise a canceller blocked on
// <-done in cancelLayerAnimationLocked deadlocks (10m test timeout). Run under
// -race; a clean, prompt pass means the lifecycle is sound.
func TestCompositorCancelAnimationMixedConcurrent(t *testing.T) {
	for iter := 0; iter < 50; iter++ {
		c := NewCompositor()
		id, err := c.AddLayer()
		require.NoError(t, err)

		rgba := make([]byte, 4*4*4)
		for i := 0; i < len(rgba); i++ {
			rgba[i] = 255
		}
		require.NoError(t, c.SetOverlay(id, rgba, 4, 4, "test"))
		require.NoError(t, c.On(id))
		require.NoError(t, c.Animate(id, AnimationConfig{
			Mode:     "pulse",
			SpeedHz:  1.0,
			MinAlpha: 0.2,
			MaxAlpha: 1.0,
		}))

		time.Sleep(time.Millisecond)

		var wg sync.WaitGroup
		ops := []func(){
			func() { _ = c.Off(id) },
			func() { _ = c.RemoveLayer(id) },
			func() { _ = c.StopAnimation(id) },
			func() { _ = c.Off(id) },
		}
		for _, op := range ops {
			wg.Add(1)
			go func(fn func()) {
				defer wg.Done()
				fn()
			}(op)
		}
		wg.Wait()
	}
}
