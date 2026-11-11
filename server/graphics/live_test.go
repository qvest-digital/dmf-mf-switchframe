package graphics

import (
	"encoding/binary"
	"errors"
	"image"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/internal/video"
)

func yuv420(w, h int, y, cb, cr byte) []byte {
	buf := make([]byte, video.YUV420FrameSize(w, h))
	ySize, uv := w*h, (w/2)*(h/2)
	for i := range buf {
		switch {
		case i < ySize:
			buf[i] = y
		case i < ySize+uv:
			buf[i] = cb
		default:
			buf[i] = cr
		}
	}
	return buf
}

func yuv422p10(w, h int, y, cb, cr uint16) []byte {
	buf := make([]byte, video.YUV422_10bitFrameSize(w, h))
	ySize, c := w*h, (w/2)*h
	for i := 0; i < len(buf)/2; i++ {
		v := cr
		switch {
		case i < ySize:
			v = y
		case i < ySize+c:
			v = cb
		}
		binary.LittleEndian.PutUint16(buf[2*i:], v)
	}
	return buf
}

func sample10(buf []byte, i int) int { return int(binary.LittleEndian.Uint16(buf[2*i:])) }

// liveLayer sets up an on-air live layer fed with one fill and key picture.
func liveLayer(t *testing.T, fill, key []byte, w, h int, tenBit, premultiplied bool) *Compositor {
	t.Helper()
	c := NewCompositor()
	id, err := c.AddLayer()
	require.NoError(t, err)
	require.NoError(t, c.SetLive(id, "gfx-fill", "gfx-key", LiveOptions{Premultiplied: premultiplied}))
	require.NoError(t, c.On(id))
	c.SetLiveFrames(id,
		LivePicture{Data: fill, Width: w, Height: h, TenBit: tenBit},
		LivePicture{Data: key, Width: w, Height: h, TenBit: tenBit})
	return c
}

// The key is a legal-range luma signal: black is no fill and white is all
// fill, so the full 0-255 weight spans 16-235, not 0-255.
func TestLiveKeyLUT8(t *testing.T) {
	var lut [256]byte
	liveKeyLUT8(&lut, 1)
	require.Equal(t, byte(0), lut[16])
	require.Equal(t, byte(128), lut[126])
	require.Equal(t, byte(255), lut[235])
	require.Equal(t, byte(0), lut[0], "super-black stays transparent")
	require.Equal(t, byte(255), lut[255], "super-white stays opaque")

	liveKeyLUT8(&lut, 0.5)
	require.Equal(t, byte(128), lut[235], "fade scales the weight")
}

func TestLiveKeyLUT10(t *testing.T) {
	var lut [1024]byte
	liveKeyLUT10(&lut, 1)
	require.Equal(t, byte(0), lut[64])
	require.Equal(t, byte(128), lut[502])
	require.Equal(t, byte(255), lut[940])
	require.Equal(t, byte(0), lut[4])
	require.Equal(t, byte(255), lut[1019])
}

func TestLive8Unshaped(t *testing.T) {
	const w, h = 4, 4
	fill := yuv420(w, h, 235, 200, 60)
	key := yuv420(w, h, 126, 128, 128) // half key
	c := liveLayer(t, fill, key, w, h, false, false)

	bg := yuv420(w, h, 16, 128, 128)
	c.ProcessYUV(bg, w, h, nil, nil)
	// Half way between black and the fill.
	require.InDelta(t, 126, int(bg[0]), 1)
	require.InDelta(t, 164, int(bg[w*h]), 1)
	require.InDelta(t, 94, int(bg[len(bg)-1]), 1)
}

// A shaped fill already carries the key, so it must come out where the
// unshaped one does; mixed in again it would be keyed twice and dark.
func TestLive8PremultipliedMatchesUnshaped(t *testing.T) {
	const w, h = 4, 4
	key := yuv420(w, h, 126, 128, 128)
	// 235 shaped by a 128/255 key: 16 + 219*128/255 = 126.
	shaped := yuv420(w, h, 126, 164, 94)

	c := liveLayer(t, shaped, key, w, h, false, true)
	bg := yuv420(w, h, 16, 128, 128)
	c.ProcessYUV(bg, w, h, nil, nil)
	require.InDelta(t, 126, int(bg[0]), 1)
	require.InDelta(t, 164, int(bg[w*h]), 1)
	require.InDelta(t, 94, int(bg[len(bg)-1]), 1)

	wrong := liveLayer(t, shaped, key, w, h, false, false)
	bg = yuv420(w, h, 16, 128, 128)
	wrong.ProcessYUV(bg, w, h, nil, nil)
	require.InDelta(t, 71, int(bg[0]), 1, "the same fill read as unshaped is keyed twice")
}

func TestLive8PremultipliedKeepsBackground(t *testing.T) {
	const w, h = 4, 4
	// Fully transparent key and a black shaped fill: background untouched.
	c := liveLayer(t, yuv420(w, h, 16, 128, 128), yuv420(w, h, 16, 128, 128), w, h, false, true)
	bg := yuv420(w, h, 180, 90, 170)
	want := append([]byte(nil), bg...)
	c.ProcessYUV(bg, w, h, nil, nil)
	require.Equal(t, want, bg)
}

func TestLive8FadeFolded(t *testing.T) {
	const w, h = 4, 4
	for _, premultiplied := range []bool{false, true} {
		c := liveLayer(t, yuv420(w, h, 235, 128, 128), yuv420(w, h, 235, 128, 128), w, h, false, premultiplied)
		c.mu.Lock()
		c.layers[0].fadePosition = 0.5
		c.mu.Unlock()
		bg := yuv420(w, h, 16, 128, 128)
		c.ProcessYUV(bg, w, h, nil, nil)
		require.InDelta(t, 126, int(bg[0]), 1, "premultiplied=%v", premultiplied)
	}
}

func TestLive10Unshaped(t *testing.T) {
	const w, h = 4, 2
	fill := yuv422p10(w, h, 940, 800, 240)
	key := yuv422p10(w, h, 502, 512, 512)
	c := liveLayer(t, fill, key, w, h, true, false)

	// The blend weight is 8-bit, a step of 1/256 of the 10-bit range.
	bg := yuv422p10(w, h, 64, 512, 512)
	c.ProcessYUV10(bg, w, h)
	require.InDelta(t, 502, sample10(bg, 0), 4)
	require.InDelta(t, 656, sample10(bg, w*h), 4)
	require.InDelta(t, 376, sample10(bg, len(bg)/2-1), 4)
}

func TestLive10Premultiplied(t *testing.T) {
	const w, h = 4, 2
	key := yuv422p10(w, h, 502, 512, 512)
	// 940/800/240 shaped by a half key: 64+438, 512+144, 512-136.
	shaped := yuv422p10(w, h, 502, 656, 376)
	c := liveLayer(t, shaped, key, w, h, true, true)

	// The blend weight is 8-bit, a step of 1/256 of the 10-bit range.
	bg := yuv422p10(w, h, 64, 512, 512)
	c.ProcessYUV10(bg, w, h)
	require.InDelta(t, 502, sample10(bg, 0), 4)
	require.InDelta(t, 656, sample10(bg, w*h), 4)
	require.InDelta(t, 376, sample10(bg, len(bg)/2-1), 4)
}

func TestLiveMismatchSkipped(t *testing.T) {
	const w, h = 4, 4
	// The fill is twice the program size: nothing is drawn.
	c := NewCompositor()
	id, _ := c.AddLayer()
	require.NoError(t, c.SetLive(id, "f", "k", LiveOptions{}))
	require.NoError(t, c.On(id))
	c.SetLiveFrames(id,
		LivePicture{Data: yuv420(8, 8, 235, 128, 128), Width: 8, Height: 8},
		LivePicture{Data: yuv420(w, h, 235, 128, 128), Width: w, Height: h})
	bg := yuv420(w, h, 16, 128, 128)
	want := append([]byte(nil), bg...)
	c.ProcessYUV(bg, w, h, nil, nil)
	require.Equal(t, want, bg)

	// An 8-bit pair on a 10-bit program is skipped too.
	c.SetLiveFrames(id,
		LivePicture{Data: yuv420(w, h, 235, 128, 128), Width: w, Height: h},
		LivePicture{Data: yuv420(w, h, 235, 128, 128), Width: w, Height: h})
	bg10 := yuv422p10(w, h, 64, 512, 512)
	want10 := append([]byte(nil), bg10...)
	c.ProcessYUV10(bg10, w, h)
	require.Equal(t, want10, bg10)
}

func TestLiveFramesClearedDrawNothing(t *testing.T) {
	const w, h = 4, 4
	c := liveLayer(t, yuv420(w, h, 235, 128, 128), yuv420(w, h, 235, 128, 128), w, h, false, false)
	c.ClearLiveFrames()
	bg := yuv420(w, h, 16, 128, 128)
	c.ProcessYUV(bg, w, h, nil, nil)
	require.Equal(t, byte(16), bg[0])
	require.Empty(t, c.SnapshotVisibleLayers())
}

func TestLiveLayerGoesOnWithoutOverlay(t *testing.T) {
	c := NewCompositor()
	id, _ := c.AddLayer()
	require.ErrorIs(t, c.On(id), ErrNoOverlay)

	require.NoError(t, c.SetLive(id, "f", "k", LiveOptions{}))
	require.NoError(t, c.On(id))
	require.True(t, c.HasActiveLayers())
	require.Equal(t, []LiveSource{{ID: id, Fill: "f", Key: "k"}}, c.LiveSources())
	fills, keys := c.LiveOnAir()
	require.Equal(t, []string{"f"}, fills)
	require.Equal(t, []string{"k"}, keys)

	st := c.Status().Layers[0]
	require.Equal(t, "f", st.LiveFill)
	require.Equal(t, "k", st.LiveKey)

	require.NoError(t, c.Off(id))
	require.Empty(t, c.LiveSources())
	fills, _ = c.LiveOnAir()
	require.Empty(t, fills)

	require.NoError(t, c.AutoOn(id, 0), "auto on needs no overlay either")
}

func TestLiveLayerRejectsPositioning(t *testing.T) {
	c := NewCompositor()
	id, _ := c.AddLayer()
	require.NoError(t, c.SetLayerRect(id, image.Rect(0, 0, 100, 100)))
	require.NoError(t, c.SetLive(id, "f", "k", LiveOptions{}))
	require.Equal(t, RectState{}, c.Status().Layers[0].Rect, "a live layer is full frame")

	require.ErrorIs(t, c.FlyOn(id, "left", 100), ErrLiveLayer)
	require.NoError(t, c.On(id))
	require.ErrorIs(t, c.SetLayerRect(id, image.Rect(0, 0, 10, 10)), ErrLiveLayer)
	require.ErrorIs(t, c.UpdateLayerRect(id, image.Rect(0, 0, 10, 10)), ErrLiveLayer)
	require.NoError(t, c.SetLayerRect(id, image.Rectangle{}), "full frame stays allowed")
	require.ErrorIs(t, c.FlyIn(id, "left", 100), ErrLiveLayer)
	require.ErrorIs(t, c.FlyOut(id, "left", 100), ErrLiveLayer)
	require.ErrorIs(t, c.SlideLayer(id, image.Rect(0, 0, 10, 10), 100), ErrLiveLayer)
	require.NoError(t, c.SetLayerZOrder(id, 3))
	require.NoError(t, c.Animate(id, AnimationConfig{Mode: "pulse", MinAlpha: 0.5, MaxAlpha: 1, SpeedHz: 1}))
	require.NoError(t, c.StopAnimation(id))
}

func TestSetLiveValidates(t *testing.T) {
	c := NewCompositor()
	id, _ := c.AddLayer()
	require.ErrorIs(t, c.SetLive(id, "a", "a", LiveOptions{}), ErrLiveSources)
	require.ErrorIs(t, c.SetLive(id, "", "a", LiveOptions{}), ErrLiveSources)
	require.ErrorIs(t, c.SetLive(id+1, "a", "b", LiveOptions{}), ErrLayerNotFound)

	refused := errors.New("refused")
	c.SetLiveGuard(func(o LiveOptions) error {
		if o.Premultiplied {
			return refused
		}
		return nil
	})
	require.ErrorIs(t, c.SetLive(id, "a", "b", LiveOptions{Premultiplied: true}), refused)
	require.NoError(t, c.SetLive(id, "a", "b", LiveOptions{}))
}

func TestClearLive(t *testing.T) {
	c := NewCompositor()
	id, _ := c.AddLayer()
	require.NoError(t, c.SetLive(id, "f", "k", LiveOptions{}))
	require.NoError(t, c.On(id))
	require.NoError(t, c.ClearLive(id))
	st := c.Status().Layers[0]
	require.False(t, st.Active, "nothing left to show")
	require.Empty(t, st.LiveFill)

	// With an overlay the layer stays on and shows it again.
	require.NoError(t, c.SetOverlay(id, make([]byte, 4*4*4), 4, 4, ""))
	require.NoError(t, c.SetLive(id, "f", "k", LiveOptions{}))
	require.NoError(t, c.On(id))
	require.NoError(t, c.ClearLive(id))
	require.True(t, c.Status().Layers[0].Active)
}

func TestSnapshotVisibleLayersLive(t *testing.T) {
	const w, h = 4, 4
	fill := yuv420(w, h, 235, 128, 128)
	key := yuv420(w, h, 126, 128, 128)
	c := liveLayer(t, fill, key, w, h, false, true)
	snaps := c.SnapshotVisibleLayers()
	require.Len(t, snaps, 1)
	require.NotNil(t, snaps[0].LiveFill)
	require.Equal(t, &fill[0], &snaps[0].LiveFill.Data[0], "pictures are passed, not copied")
	require.Equal(t, &key[0], &snaps[0].LiveKey.Data[0])
	require.True(t, snaps[0].Premultiplied)
	require.Nil(t, snaps[0].Overlay)
	require.Equal(t, byte(128), snaps[0].LiveLUT[126], "the GPU keys with the CPU's LUT")
}
