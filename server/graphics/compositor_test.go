package graphics

import (
	"image"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func makeTestOverlay(w, h int) []byte {
	return make([]byte, w*h*4) // transparent black
}

func TestCompositor_AddRemoveLayer(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	id1, err := c.AddLayer()
	require.NoError(t, err)
	require.Equal(t, 0, id1)

	id2, err := c.AddLayer()
	require.NoError(t, err)
	require.Equal(t, 1, id2)

	id3, err := c.AddLayer()
	require.NoError(t, err)
	require.Equal(t, 2, id3)

	status := c.Status()
	require.Len(t, status.Layers, 3)

	require.NoError(t, c.RemoveLayer(id2))
	status = c.Status()
	require.Len(t, status.Layers, 2)

	// IDs should be id1 and id3
	ids := make(map[int]bool)
	for _, l := range status.Layers {
		ids[l.ID] = true
	}
	require.True(t, ids[id1])
	require.True(t, ids[id3])
}

func TestCompositor_MaxLayers(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	for i := 0; i < DefaultMaxLayers; i++ {
		_, err := c.AddLayer()
		require.NoError(t, err)
	}

	_, err := c.AddLayer()
	require.ErrorIs(t, err, ErrTooManyLayers)
}

func TestCompositor_LayerNotFound(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	require.ErrorIs(t, c.On(999), ErrLayerNotFound)
	require.ErrorIs(t, c.Off(999), ErrLayerNotFound)
	require.ErrorIs(t, c.RemoveLayer(999), ErrLayerNotFound)
	require.ErrorIs(t, c.SetOverlay(999, nil, 0, 0, ""), ErrLayerNotFound)
}

func TestCompositor_MultiLayerProcessYUV(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	id1, _ := c.AddLayer()
	id2, _ := c.AddLayer()

	// Layer 1: red overlay (bottom z-order)
	overlay1 := makeRGBA(4, 4, 255, 0, 0, 128)
	require.NoError(t, c.SetOverlay(id1, overlay1, 4, 4, "red"))
	require.NoError(t, c.On(id1))

	// Layer 2: blue overlay (top z-order)
	overlay2 := makeRGBA(4, 4, 0, 0, 255, 128)
	require.NoError(t, c.SetOverlay(id2, overlay2, 4, 4, "blue"))
	require.NoError(t, c.On(id2))

	yuv := makeYUV420(4, 4, 0, 128, 128)
	result := c.ProcessYUV(yuv, 4, 4, nil)

	// Both layers should have blended — result should differ from black.
	allZero := true
	for i := 0; i < 4*4; i++ {
		if result[i] != 0 {
			allZero = false
			break
		}
	}
	require.False(t, allZero, "Y plane should be modified by two active layers")
}

func TestCompositor_PerLayerFade(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	id1, _ := c.AddLayer()
	id2, _ := c.AddLayer()

	overlay := makeTestOverlay(320, 240)
	require.NoError(t, c.SetOverlay(id1, overlay, 320, 240, "a"))
	require.NoError(t, c.SetOverlay(id2, overlay, 320, 240, "b"))

	// Fade in layer 1
	require.NoError(t, c.AutoOn(id1, 50*time.Millisecond))
	time.Sleep(100 * time.Millisecond)

	status := c.Status()
	var l1, l2 LayerState
	for _, l := range status.Layers {
		if l.ID == id1 {
			l1 = l
		}
		if l.ID == id2 {
			l2 = l
		}
	}
	require.True(t, l1.Active, "layer 1 should be active after fade-in")
	require.Equal(t, 1.0, l1.FadePosition, "layer 1 should be fully faded in")
	require.False(t, l2.Active, "layer 2 should remain inactive")
}

func TestCompositor_PerLayerAnimation(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	id1, _ := c.AddLayer()
	id2, _ := c.AddLayer()

	overlay := makeTestOverlay(320, 240)
	require.NoError(t, c.SetOverlay(id1, overlay, 320, 240, "a"))
	require.NoError(t, c.SetOverlay(id2, overlay, 320, 240, "b"))
	require.NoError(t, c.On(id1))
	require.NoError(t, c.On(id2))

	// Animate layer 1 only
	cfg := AnimationConfig{
		Mode:     "pulse",
		MinAlpha: 0.3,
		MaxAlpha: 1.0,
		SpeedHz:  5.0,
	}
	require.NoError(t, c.Animate(id1, cfg))
	time.Sleep(200 * time.Millisecond)

	// Layer 2 should still be at full fade
	status := c.Status()
	for _, l := range status.Layers {
		if l.ID == id2 {
			require.Equal(t, 1.0, l.FadePosition, "layer 2 fadePosition should remain 1.0")
		}
	}

	require.NoError(t, c.StopAnimation(id1))
}

func TestCompositor_RemoveLayerCancelsFade(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	id, _ := c.AddLayer()
	overlay := makeTestOverlay(320, 240)
	require.NoError(t, c.SetOverlay(id, overlay, 320, 240, "test"))
	require.NoError(t, c.AutoOn(id, 5*time.Second))

	// Remove should cancel the in-progress fade.
	require.NoError(t, c.RemoveLayer(id))

	status := c.Status()
	require.Len(t, status.Layers, 0, "layer should be removed")
}

func TestCompositor_ZOrder(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	id1, _ := c.AddLayer()
	id2, _ := c.AddLayer()
	id3, _ := c.AddLayer()

	// Set z-orders: id3=0 (bottom), id1=1 (middle), id2=2 (top)
	require.NoError(t, c.SetLayerZOrder(id3, 0))
	require.NoError(t, c.SetLayerZOrder(id1, 1))
	require.NoError(t, c.SetLayerZOrder(id2, 2))

	status := c.Status()
	require.Len(t, status.Layers, 3)
	require.Equal(t, id3, status.Layers[0].ID, "z-order 0 should be first")
	require.Equal(t, id1, status.Layers[1].ID, "z-order 1 should be second")
	require.Equal(t, id2, status.Layers[2].ID, "z-order 2 should be third")
}

func TestCompositor_IsActiveMultiLayer(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	require.False(t, c.IsActive(), "no layers = not active")

	id1, _ := c.AddLayer()
	id2, _ := c.AddLayer()
	require.False(t, c.IsActive(), "layers exist but none active")

	overlay := makeTestOverlay(4, 4)
	require.NoError(t, c.SetOverlay(id1, overlay, 4, 4, ""))
	require.NoError(t, c.On(id1))
	require.True(t, c.IsActive(), "one layer active = active")

	require.NoError(t, c.Off(id1))
	require.False(t, c.IsActive(), "layer turned off = not active")

	require.NoError(t, c.SetOverlay(id2, overlay, 4, 4, ""))
	require.NoError(t, c.On(id2))
	require.True(t, c.IsActive(), "second layer active = active")
}

func TestCompositor_CloseMultiLayer(t *testing.T) {
	c := NewCompositor()

	id1, _ := c.AddLayer()
	id2, _ := c.AddLayer()

	overlay := makeTestOverlay(320, 240)
	_ = c.SetOverlay(id1, overlay, 320, 240, "")
	_ = c.SetOverlay(id2, overlay, 320, 240, "")
	_ = c.On(id1)
	_ = c.On(id2)

	cfg := AnimationConfig{Mode: "pulse", MinAlpha: 0.2, MaxAlpha: 1.0, SpeedHz: 3.0}
	_ = c.Animate(id1, cfg)
	_ = c.Animate(id2, cfg)
	time.Sleep(50 * time.Millisecond)

	c.Close()

	err := c.On(id1)
	require.ErrorIs(t, err, ErrCompositorClosed)
}

// Tests migrated from old single-layer API:

func TestCompositor_Passthrough(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	status := c.Status()
	require.Empty(t, status.Layers, "expected no layers initially")
	require.False(t, c.IsActive())
}

func TestCompositor_ActiveBlend(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	id, _ := c.AddLayer()
	overlay := makeTestOverlay(320, 240)
	for i := 0; i < len(overlay); i += 4 {
		overlay[i] = 255
		overlay[i+1] = 0
		overlay[i+2] = 0
		overlay[i+3] = 128
	}

	require.NoError(t, c.SetOverlay(id, overlay, 320, 240, "lower-third"))
	require.NoError(t, c.On(id))

	status := c.Status()
	require.Len(t, status.Layers, 1)
	require.True(t, status.Layers[0].Active)
	require.Equal(t, "lower-third", status.Layers[0].Template)
	require.Equal(t, 1.0, status.Layers[0].FadePosition)
}

func TestCompositor_Toggle(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	id, _ := c.AddLayer()
	overlay := makeTestOverlay(320, 240)
	_ = c.SetOverlay(id, overlay, 320, 240, "full-screen")

	require.NoError(t, c.On(id))
	require.True(t, c.IsActive())

	require.NoError(t, c.Off(id))
	require.False(t, c.IsActive())

	require.NoError(t, c.On(id))
	require.True(t, c.IsActive())
}

func TestCompositor_OnWithoutOverlayReturnsError(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	id, _ := c.AddLayer()
	err := c.On(id)
	require.ErrorIs(t, err, ErrNoOverlay)
}

func TestCompositor_AutoOnAutoOff(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	var stateChanges int32
	c.OnStateChange(func(_ State) {
		atomic.AddInt32(&stateChanges, 1)
	})

	id, _ := c.AddLayer()
	overlay := makeTestOverlay(320, 240)
	_ = c.SetOverlay(id, overlay, 320, 240, "ticker")

	require.NoError(t, c.AutoOn(id, 50*time.Millisecond))
	time.Sleep(100 * time.Millisecond)

	status := c.Status()
	require.Len(t, status.Layers, 1)
	require.True(t, status.Layers[0].Active)
	require.Equal(t, 1.0, status.Layers[0].FadePosition)

	require.NoError(t, c.AutoOff(id, 50*time.Millisecond))
	time.Sleep(100 * time.Millisecond)

	status = c.Status()
	require.False(t, status.Layers[0].Active)
	require.Equal(t, 0.0, status.Layers[0].FadePosition)

	changes := atomic.LoadInt32(&stateChanges)
	require.GreaterOrEqual(t, changes, int32(2))
}

func TestCompositor_Close_Ops(t *testing.T) {
	c := NewCompositor()
	id, _ := c.AddLayer()
	overlay := makeTestOverlay(320, 240)
	_ = c.SetOverlay(id, overlay, 320, 240, "test")
	_ = c.On(id)

	c.Close()

	err := c.On(id)
	require.ErrorIs(t, err, ErrCompositorClosed)

	err = c.Off(id)
	require.ErrorIs(t, err, ErrCompositorClosed)
}

func TestCompositor_ClosedReturnsSentinel(t *testing.T) {
	c := NewCompositor()
	c.Close()

	_, err := c.AddLayer()
	require.ErrorIs(t, err, ErrCompositorClosed)
}

func TestCompositor_SetOverlaySizeMismatch(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	id, _ := c.AddLayer()
	err := c.SetOverlay(id, make([]byte, 10), 320, 240, "test")
	require.Error(t, err)
}

func TestCompositor_FadeActiveError(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	id, _ := c.AddLayer()
	overlay := makeTestOverlay(320, 240)
	_ = c.SetOverlay(id, overlay, 320, 240, "test")

	require.NoError(t, c.AutoOn(id, 5*time.Second))

	err := c.AutoOn(id, 500*time.Millisecond)
	require.ErrorIs(t, err, ErrFadeActive)
}

func TestCompositor_CutDuringFade(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	id, _ := c.AddLayer()
	overlay := makeTestOverlay(320, 240)
	_ = c.SetOverlay(id, overlay, 320, 240, "test")

	require.NoError(t, c.AutoOn(id, 5*time.Second))

	require.NoError(t, c.Off(id))

	require.False(t, c.IsActive())
}

func TestCompositor_AnimatePulse(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	id, _ := c.AddLayer()
	overlay := makeTestOverlay(320, 240)
	require.NoError(t, c.SetOverlay(id, overlay, 320, 240, "test"))
	require.NoError(t, c.On(id))

	cfg := AnimationConfig{
		Mode:     "pulse",
		MinAlpha: 0.3,
		MaxAlpha: 1.0,
		SpeedHz:  5.0,
	}
	require.NoError(t, c.Animate(id, cfg))
	time.Sleep(200 * time.Millisecond)

	status := c.Status()
	require.True(t, status.Layers[0].Active)
	require.GreaterOrEqual(t, status.Layers[0].FadePosition, 0.3)
	require.LessOrEqual(t, status.Layers[0].FadePosition, 1.0)

	require.NoError(t, c.StopAnimation(id))
}

func TestCompositor_AnimateRequiresActive(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	id, _ := c.AddLayer()
	overlay := makeTestOverlay(320, 240)
	require.NoError(t, c.SetOverlay(id, overlay, 320, 240, "test"))

	cfg := AnimationConfig{Mode: "pulse", MinAlpha: 0.3, MaxAlpha: 1.0, SpeedHz: 2.0}
	err := c.Animate(id, cfg)
	require.ErrorIs(t, err, ErrNotActive)
}

func TestCompositor_AnimateStopsOnOff(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	id, _ := c.AddLayer()
	overlay := makeTestOverlay(320, 240)
	require.NoError(t, c.SetOverlay(id, overlay, 320, 240, "test"))
	require.NoError(t, c.On(id))

	cfg := AnimationConfig{Mode: "pulse", MinAlpha: 0.2, MaxAlpha: 0.9, SpeedHz: 3.0}
	require.NoError(t, c.Animate(id, cfg))
	time.Sleep(50 * time.Millisecond)

	require.NoError(t, c.Off(id))

	c.mu.RLock()
	layer := c.layers[id]
	animDone := layer.animDone
	c.mu.RUnlock()
	require.Nil(t, animDone)
}

func TestCompositor_StopAnimation(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	id, _ := c.AddLayer()
	overlay := makeTestOverlay(320, 240)
	require.NoError(t, c.SetOverlay(id, overlay, 320, 240, "test"))
	require.NoError(t, c.On(id))

	cfg := AnimationConfig{Mode: "pulse", MinAlpha: 0.2, MaxAlpha: 0.8, SpeedHz: 3.0}
	require.NoError(t, c.Animate(id, cfg))
	time.Sleep(50 * time.Millisecond)

	require.NoError(t, c.StopAnimation(id))

	status := c.Status()
	require.True(t, status.Layers[0].Active)
	require.Equal(t, 1.0, status.Layers[0].FadePosition)

	c.mu.RLock()
	layer := c.layers[id]
	require.Nil(t, layer.animConfig)
	require.Nil(t, layer.animDone)
	c.mu.RUnlock()
}

func TestCompositor_AnimateWhileFading(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	id, _ := c.AddLayer()
	overlay := makeTestOverlay(320, 240)
	require.NoError(t, c.SetOverlay(id, overlay, 320, 240, "test"))

	require.NoError(t, c.AutoOn(id, 5*time.Second))

	cfg := AnimationConfig{Mode: "pulse", MinAlpha: 0.3, MaxAlpha: 1.0, SpeedHz: 2.0}
	err := c.Animate(id, cfg)
	require.ErrorIs(t, err, ErrFadeActive)
}

func TestCompositor_AnimateStateCallback(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	var stateChanges int32
	c.OnStateChange(func(s State) {
		atomic.AddInt32(&stateChanges, 1)
	})

	id, _ := c.AddLayer()
	overlay := makeTestOverlay(320, 240)
	require.NoError(t, c.SetOverlay(id, overlay, 320, 240, "test"))
	require.NoError(t, c.On(id))
	atomic.StoreInt32(&stateChanges, 0)

	cfg := AnimationConfig{Mode: "pulse", MinAlpha: 0.2, MaxAlpha: 1.0, SpeedHz: 4.0}
	require.NoError(t, c.Animate(id, cfg))

	time.Sleep(300 * time.Millisecond)
	require.NoError(t, c.StopAnimation(id))

	changes := atomic.LoadInt32(&stateChanges)
	require.GreaterOrEqual(t, changes, int32(2))
}

func TestCompositor_CloseCancelsAnimation(t *testing.T) {
	c := NewCompositor()

	id, _ := c.AddLayer()
	overlay := makeTestOverlay(320, 240)
	require.NoError(t, c.SetOverlay(id, overlay, 320, 240, "test"))
	require.NoError(t, c.On(id))

	cfg := AnimationConfig{Mode: "pulse", MinAlpha: 0.2, MaxAlpha: 1.0, SpeedHz: 3.0}
	require.NoError(t, c.Animate(id, cfg))
	time.Sleep(50 * time.Millisecond)

	c.Close()

	err := c.On(id)
	require.ErrorIs(t, err, ErrCompositorClosed)
}

func TestCompositor_AnimateWhileAnimating(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	id, _ := c.AddLayer()
	overlay := makeTestOverlay(320, 240)
	require.NoError(t, c.SetOverlay(id, overlay, 320, 240, "test"))
	require.NoError(t, c.On(id))

	cfg := AnimationConfig{Mode: "pulse", MinAlpha: 0.3, MaxAlpha: 1.0, SpeedHz: 2.0}
	require.NoError(t, c.Animate(id, cfg))

	err := c.Animate(id, cfg)
	require.ErrorIs(t, err, ErrFadeActive)

	require.NoError(t, c.StopAnimation(id))
}

func TestCompositor_AutoOnWhileAnimating(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	id, _ := c.AddLayer()
	overlay := makeTestOverlay(320, 240)
	require.NoError(t, c.SetOverlay(id, overlay, 320, 240, "test"))
	require.NoError(t, c.On(id))

	cfg := AnimationConfig{Mode: "pulse", MinAlpha: 0.3, MaxAlpha: 1.0, SpeedHz: 2.0}
	require.NoError(t, c.Animate(id, cfg))

	err := c.AutoOff(id, 500*time.Millisecond)
	require.ErrorIs(t, err, ErrFadeActive)

	require.NoError(t, c.StopAnimation(id))
}

func TestCompositor_AnimateStateFields(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	id, _ := c.AddLayer()
	overlay := makeTestOverlay(320, 240)
	require.NoError(t, c.SetOverlay(id, overlay, 320, 240, "test"))
	require.NoError(t, c.On(id))

	cfg := AnimationConfig{Mode: "pulse", MinAlpha: 0.3, MaxAlpha: 1.0, SpeedHz: 2.5}
	require.NoError(t, c.Animate(id, cfg))

	status := c.Status()
	require.Equal(t, "pulse", status.Layers[0].AnimationMode)
	require.Equal(t, 2.5, status.Layers[0].AnimationHz)

	require.NoError(t, c.StopAnimation(id))

	status = c.Status()
	require.Empty(t, status.Layers[0].AnimationMode)
	require.Equal(t, float64(0), status.Layers[0].AnimationHz)
}

func TestCompositor_AutoOn_AlreadyActive_NoFlash(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	id, _ := c.AddLayer()
	overlay := makeTestOverlay(320, 240)
	require.NoError(t, c.SetOverlay(id, overlay, 320, 240, "test"))
	require.NoError(t, c.On(id))

	status := c.Status()
	require.True(t, status.Layers[0].Active)
	require.Equal(t, 1.0, status.Layers[0].FadePosition)

	require.NoError(t, c.AutoOn(id, 500*time.Millisecond))

	status = c.Status()
	require.True(t, status.Layers[0].Active)
	require.Equal(t, 1.0, status.Layers[0].FadePosition)
}

func TestCompositor_TransitionAnimation(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	c.SetResolutionProvider(func() (int, int) { return 1920, 1080 })

	id, _ := c.AddLayer()
	overlay := makeTestOverlay(320, 240)
	require.NoError(t, c.SetOverlay(id, overlay, 320, 240, "test"))
	require.NoError(t, c.On(id))

	require.NoError(t, c.SetLayerRect(id, fullFrameRect(100, 100)))

	cfg := AnimationConfig{
		Mode:       "transition",
		ToRect:     &RectState{X: 200, Y: 200, Width: 100, Height: 100},
		DurationMs: 50,
		Easing:     "linear",
	}
	require.NoError(t, c.Animate(id, cfg))
	time.Sleep(100 * time.Millisecond)

	status := c.Status()
	require.Equal(t, 200, status.Layers[0].Rect.X)
	require.Equal(t, 200, status.Layers[0].Rect.Y)
}

func TestCompositor_AnimationEasing(t *testing.T) {
	require.Equal(t, 0.0, applyEasing(0.0, "smoothstep"))
	require.Equal(t, 1.0, applyEasing(1.0, "smoothstep"))
	require.Equal(t, 0.5, applyEasing(0.5, "smoothstep"))

	require.Equal(t, 0.5, applyEasing(0.5, "linear"))
	require.Equal(t, 0.25, applyEasing(0.25, "linear"))

	val := applyEasing(0.25, "ease-in-out")
	require.Less(t, val, 0.25, "ease-in-out should be slower at start")
}

func TestCompositor_UpdateLayerRect_NoStateBroadcast(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	var stateChanges int32
	c.OnStateChange(func(_ State) {
		atomic.AddInt32(&stateChanges, 1)
	})

	id, _ := c.AddLayer()
	atomic.StoreInt32(&stateChanges, 0)

	// UpdateLayerRect should NOT trigger state broadcast.
	require.NoError(t, c.UpdateLayerRect(id, fullFrameRect(100, 100)))
	require.Equal(t, int32(0), atomic.LoadInt32(&stateChanges))

	// SetLayerRect SHOULD trigger state broadcast.
	require.NoError(t, c.SetLayerRect(id, fullFrameRect(200, 200)))
	require.Equal(t, int32(1), atomic.LoadInt32(&stateChanges))
}

func TestCompositor_FlyIn(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	c.SetResolutionProvider(func() (int, int) { return 1920, 1080 })

	id, err := c.AddLayer()
	require.NoError(t, err)
	require.NoError(t, c.SetOverlay(id, makeTestOverlay(100, 50), 100, 50, "test"))
	require.NoError(t, c.SetLayerRect(id, image.Rect(100, 200, 200, 250)))
	require.NoError(t, c.On(id))

	require.NoError(t, c.FlyIn(id, "left", 50))

	// Animation should be running.
	status := c.Status()
	require.Len(t, status.Layers, 1)
	require.Equal(t, "transition", status.Layers[0].AnimationMode)

	// Wait for completion.
	time.Sleep(100 * time.Millisecond)

	// After fly-in, rect should return to the target (100, 200).
	status = c.Status()
	require.Equal(t, 100, status.Layers[0].Rect.X)
	require.Equal(t, 200, status.Layers[0].Rect.Y)
}

func TestCompositor_FlyIn_InvalidDirection(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	id, err := c.AddLayer()
	require.NoError(t, err)
	require.NoError(t, c.SetOverlay(id, makeTestOverlay(4, 4), 4, 4, "test"))
	require.NoError(t, c.On(id))

	// FlyIn with invalid direction still works (noop animation from current to current).
	require.NoError(t, c.FlyIn(id, "invalid", 50))
	time.Sleep(100 * time.Millisecond)
}

func TestCompositor_FlyOut(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	c.SetResolutionProvider(func() (int, int) { return 1920, 1080 })

	id, err := c.AddLayer()
	require.NoError(t, err)
	require.NoError(t, c.SetOverlay(id, makeTestOverlay(100, 50), 100, 50, "test"))
	require.NoError(t, c.SetLayerRect(id, image.Rect(100, 200, 200, 250)))
	require.NoError(t, c.On(id))

	require.NoError(t, c.FlyOut(id, "right", 50))

	status := c.Status()
	require.Equal(t, "transition", status.Layers[0].AnimationMode)

	time.Sleep(100 * time.Millisecond)

	// After fly-out, rect should be off-screen to the right.
	status = c.Status()
	require.GreaterOrEqual(t, status.Layers[0].Rect.X, 1920)
}

func TestCompositor_FlyIn_RequiresActive(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	id, err := c.AddLayer()
	require.NoError(t, err)

	err = c.FlyIn(id, "left", 500)
	require.ErrorIs(t, err, ErrNotActive)
}

func TestCompositor_FlyOut_LayerNotFound(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	err := c.FlyOut(999, "left", 500)
	require.ErrorIs(t, err, ErrLayerNotFound)
}

func TestCompositor_SlideLayer(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	id, err := c.AddLayer()
	require.NoError(t, err)
	require.NoError(t, c.SetOverlay(id, makeTestOverlay(4, 4), 4, 4, "test"))
	require.NoError(t, c.SetLayerRect(id, image.Rect(0, 0, 100, 100)))
	require.NoError(t, c.On(id))

	target := image.Rect(500, 500, 600, 600)
	require.NoError(t, c.SlideLayer(id, target, 50))

	time.Sleep(100 * time.Millisecond)

	status := c.Status()
	require.Equal(t, 500, status.Layers[0].Rect.X)
	require.Equal(t, 500, status.Layers[0].Rect.Y)
}

func TestCompositor_ConcurrentOps(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	yuv := make([]byte, 8*8*3/2)
	overlay := makeTestOverlay(8, 8)

	var wg sync.WaitGroup
	const goroutines = 8

	// Concurrent add/remove/toggle/process.
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				id, err := c.AddLayer()
				if err != nil {
					// Max layers reached, try removing one.
					_ = c.RemoveLayer(n % 8)
					continue
				}
				_ = c.SetOverlay(id, overlay, 8, 8, "test")
				_ = c.On(id)
				c.ProcessYUV(yuv, 8, 8, nil)
				_ = c.Off(id)
				_ = c.RemoveLayer(id)
			}
		}(i)
	}
	wg.Wait()

	// Should not panic or deadlock. Final state should be consistent.
	status := c.Status()
	_ = status
}

func TestCompositor_ProcessYUV_ShortBuffer(t *testing.T) {
	c := NewCompositor()
	defer c.Close()

	id, err := c.AddLayer()
	require.NoError(t, err)
	require.NoError(t, c.SetOverlay(id, makeTestOverlay(4, 4), 4, 4, "test"))
	require.NoError(t, c.On(id))

	// Short buffer should be returned unchanged (no panic).
	short := make([]byte, 10)
	result := c.ProcessYUV(short, 4, 4, nil)
	require.Equal(t, short, result)
}
