package dve

import (
	"bytes"
	"encoding/binary"
	"image"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/internal/video"
)

func ms(v int) *int { return &v }

// newTestSqueeze returns a 64x64 squeeze-back into the bottom-right
// quarter with a fake clock.
func newTestSqueeze(t *testing.T, background string) (*Squeeze, func(time.Duration)) {
	t.Helper()
	s := NewSqueeze(64, 64)
	clock, advance := fakeClock()
	s.now = clock
	require.NoError(t, s.Set(SqueezeConfig{
		Rect:       Rect{X: 32, Y: 32, W: 32, H: 32},
		Background: background,
		DurationMs: 1000,
	}))
	return s, advance
}

// splitFrame is a 4:2:0 frame whose left half is luma 200 and right half
// luma 100: scaled, both halves survive; cropped, one would not.
func splitFrame(w, h int) []byte {
	f := makeYUV420(w, h, 100, 128, 128)
	for y := range h {
		for x := range w / 2 {
			f[y*w+x] = 200
		}
	}
	return f
}

func TestSqueeze_RectFollowsAnimation(t *testing.T) {
	s, advance := newTestSqueeze(t, "")
	require.NoError(t, s.On(SqueezeMove{}))

	_, ok := s.Frame(64, 64)
	require.False(t, ok, "full frame at t=0: nothing to draw")

	advance(500 * time.Millisecond)
	r, ok := s.Frame(64, 64)
	require.True(t, ok)
	require.Equal(t, image.Rect(16, 16, 64, 64), r, "halfway at t=0.5")

	advance(500 * time.Millisecond)
	r, ok = s.Frame(64, 64)
	require.True(t, ok)
	require.Equal(t, image.Rect(32, 32, 64, 64), r, "in the configured rect at t=1")
	require.False(t, s.Snapshot().Animating)
	require.True(t, s.Snapshot().On)
}

func TestSqueeze_ProgramScaledIntoRectOverBackground(t *testing.T) {
	s, _ := newTestSqueeze(t, "cam2")
	s.IngestSourceFrame("cam2", makeYUV420(64, 64, 50, 90, 160), 64, 64)
	require.NoError(t, s.On(SqueezeMove{DurationMs: ms(0)}))

	out := s.ProcessFrame(splitFrame(64, 64), 64, 64)

	// Scaled, not cropped: both halves of the program inside the rect.
	require.Equal(t, byte(200), out[40*64+34], "left half of the program")
	require.Equal(t, byte(100), out[40*64+62], "right half of the program")
	// Outside: the background, luma and chroma.
	require.Equal(t, byte(50), out[0])
	require.Equal(t, byte(50), out[10*64+50], "above the rect")
	cb := video.YUV420_8bit.CbOffset(64, 64)
	cr := video.YUV420_8bit.CrOffset(64, 64)
	require.Equal(t, byte(90), out[cb])
	require.Equal(t, byte(160), out[cr])
	require.Equal(t, byte(128), out[cb+20*32+20], "program chroma inside the rect")
}

func TestSqueeze_BlackWithoutBackground(t *testing.T) {
	s, _ := newTestSqueeze(t, "")
	require.NoError(t, s.On(SqueezeMove{DurationMs: ms(0)}))
	out := s.ProcessFrame(makeYUV420(64, 64, 235, 128, 128), 64, 64)
	require.Equal(t, byte(16), out[0], "black around the program")
	require.Equal(t, byte(235), out[40*64+40])
}

func TestSqueeze_BackgroundScaledToFrame(t *testing.T) {
	s, _ := newTestSqueeze(t, "cam2")
	s.IngestSourceFrame("cam2", makeYUV420(32, 32, 50, 128, 128), 32, 32)
	require.NoError(t, s.On(SqueezeMove{DurationMs: ms(0)}))
	out := s.ProcessFrame(makeYUV420(64, 64, 235, 128, 128), 64, 64)
	require.Equal(t, byte(50), out[0])
	require.Equal(t, byte(50), out[30*64+63], "background fills the whole frame")
}

func TestSqueeze_IgnoresOtherSources(t *testing.T) {
	s, _ := newTestSqueeze(t, "cam2")
	require.True(t, s.NeedsSource("cam2"), "fed while configured, before On")
	require.False(t, s.NeedsSource("cam1"))
	s.IngestSourceFrame("cam1", makeYUV420(64, 64, 50, 128, 128), 64, 64)
	require.NoError(t, s.On(SqueezeMove{DurationMs: ms(0)}))
	out := s.ProcessFrame(makeYUV420(64, 64, 235, 128, 128), 64, 64)
	require.Equal(t, byte(16), out[0], "cam1 is not the background")
}

func TestSqueeze_10bit(t *testing.T) {
	f := video.YUV422_10bit
	s, _ := newTestSqueeze(t, "cam2")
	s.IngestSourceFrame10("cam2", video.NewFrame(f, 64, 64, 200, 300, 700), 64, 64)
	require.NoError(t, s.On(SqueezeMove{DurationMs: ms(0)}))

	prog := video.NewFrame(f, 64, 64, 940, 512, 512)
	for y := range 64 {
		for x := range 32 {
			binary.LittleEndian.PutUint16(prog[(y*64+x)*2:], 400)
		}
	}
	out := s.ProcessFrame10(prog, 64, 64)

	luma := func(x, y int) uint16 { return binary.LittleEndian.Uint16(out[(y*64+x)*2:]) }
	chroma := func(plane, x, y int) uint16 {
		return binary.LittleEndian.Uint16(out[plane+(y*32+x)*2:])
	}
	require.Equal(t, uint16(400), luma(34, 40), "left half of the program")
	require.Equal(t, uint16(940), luma(62, 40), "right half of the program")
	require.Equal(t, uint16(200), luma(0, 0), "background")
	require.Equal(t, uint16(200), luma(50, 10), "background above the rect")
	cb, cr := f.CbOffset(64, 64), f.CrOffset(64, 64)
	require.Equal(t, uint16(300), chroma(cb, 0, 0))
	require.Equal(t, uint16(700), chroma(cr, 0, 33), "4:2:2 chroma rows are full height")
	require.Equal(t, uint16(512), chroma(cb, 20, 33), "program chroma inside the rect")
	require.Equal(t, uint16(512), chroma(cr, 20, 62), "program chroma down to the bottom row")
	require.Equal(t, uint16(700), chroma(cr, 20, 31), "background chroma above the rect")
}

func TestSqueeze_NewBackgroundDropsOldFrame(t *testing.T) {
	s, _ := newTestSqueeze(t, "cam2")
	s.IngestSourceFrame("cam2", makeYUV420(64, 64, 50, 128, 128), 64, 64)
	require.NoError(t, s.Set(SqueezeConfig{Rect: Rect{X: 32, Y: 32, W: 32, H: 32}, Background: "cam3"}))
	require.NoError(t, s.On(SqueezeMove{DurationMs: ms(0)}))
	out := s.ProcessFrame(makeYUV420(64, 64, 235, 128, 128), 64, 64)
	require.Equal(t, byte(16), out[0], "black until cam3 delivers, not the last cam2 frame")
}

func TestSqueeze_10bitIgnores8bitBackground(t *testing.T) {
	f := video.YUV422_10bit
	s, _ := newTestSqueeze(t, "cam2")
	s.IngestSourceFrame("cam2", makeYUV420(64, 64, 50, 128, 128), 64, 64)
	require.NoError(t, s.On(SqueezeMove{DurationMs: ms(0)}))
	out := s.ProcessFrame10(video.NewFrame(f, 64, 64, 940, 512, 512), 64, 64)
	require.Equal(t, uint16(64), binary.LittleEndian.Uint16(out), "black, not 8-bit bytes read as 10-bit")
}

func TestSqueeze_OffAtFullFrameLeavesFrameUntouched(t *testing.T) {
	s, advance := newTestSqueeze(t, "cam2")
	s.IngestSourceFrame("cam2", makeYUV420(64, 64, 50, 128, 128), 64, 64)

	in := splitFrame(64, 64)
	want := bytes.Clone(in)
	require.Equal(t, want, s.ProcessFrame(in, 64, 64), "configured but never on")

	idle := 0
	s.SetOnArrive(func() { idle++ })
	require.NoError(t, s.On(SqueezeMove{DurationMs: ms(0)}))
	require.NoError(t, s.Off(SqueezeMove{}))
	require.True(t, s.Active(), "animating back out")
	require.Equal(t, "cam2", s.BackgroundOnAir(), "background on air until the program fills the frame")
	advance(time.Second)

	in = splitFrame(64, 64)
	out := s.ProcessFrame(in, 64, 64)
	require.Equal(t, want, out, "back at full frame")
	require.Equal(t, 1, idle, "idle reported once the background left the air")
	require.False(t, s.Active())
	require.Empty(t, s.BackgroundOnAir())

	s.ProcessFrame(in, 64, 64)
	require.Equal(t, 1, idle, "idle reported once")
}

// State reports animating until the move arrives, so an On arrival has
// to be reported as much as an Off one.
func TestSqueeze_OnArrivalReported(t *testing.T) {
	s, advance := newTestSqueeze(t, "")
	arrived := 0
	s.SetOnArrive(func() { arrived++ })
	require.NoError(t, s.On(SqueezeMove{}))
	s.Frame(64, 64)
	require.Zero(t, arrived)
	advance(time.Second)
	s.Frame(64, 64)
	require.Equal(t, 1, arrived, "On arrival reported")
	require.False(t, s.Snapshot().Animating)
	require.True(t, s.Active(), "still squeezed")
	s.Frame(64, 64)
	require.Equal(t, 1, arrived, "reported once")
}

func TestSqueeze_RetargetsFromCurrentRect(t *testing.T) {
	s, advance := newTestSqueeze(t, "")
	require.NoError(t, s.On(SqueezeMove{}))
	advance(500 * time.Millisecond)

	// Off halfway in starts from where the program is, not from the rect.
	require.NoError(t, s.Off(SqueezeMove{}))
	r, ok := s.Frame(64, 64)
	require.True(t, ok)
	require.Equal(t, image.Rect(16, 16, 64, 64), r, "no jump on retarget")

	advance(500 * time.Millisecond)
	r, ok = s.Frame(64, 64)
	require.True(t, ok)
	require.Equal(t, image.Rect(8, 8, 64, 64), r)
}

func TestSqueeze_SetWhileOnMovesToNewRect(t *testing.T) {
	s, advance := newTestSqueeze(t, "")
	require.NoError(t, s.On(SqueezeMove{DurationMs: ms(0)}))
	require.NoError(t, s.Set(SqueezeConfig{Rect: Rect{X: 0, Y: 0, W: 32, H: 32}, DurationMs: 1000}))

	advance(500 * time.Millisecond)
	r, ok := s.Frame(64, 64)
	require.True(t, ok)
	require.Equal(t, image.Rect(16, 16, 48, 48), r)
}

func TestSqueeze_OvershootDoesNotEndEarly(t *testing.T) {
	s, advance := newTestSqueeze(t, "")
	// y1 = y2 = 2 passes 1 well before the end.
	require.NoError(t, s.On(SqueezeMove{Easing: "cubic-bezier(0, 2, 1, 2)"}))
	advance(500 * time.Millisecond)
	s.Frame(64, 64)
	require.True(t, s.Snapshot().Animating, "the animation runs its full duration")
}

func TestSqueeze_FrameScalesRectToFrameSize(t *testing.T) {
	s, _ := newTestSqueeze(t, "")
	require.NoError(t, s.On(SqueezeMove{DurationMs: ms(0)}))
	r, ok := s.Frame(128, 128)
	require.True(t, ok)
	require.Equal(t, image.Rect(64, 64, 128, 128), r)
}

func TestSqueeze_Validation(t *testing.T) {
	s := NewSqueeze(64, 64)
	require.ErrorIs(t, s.On(SqueezeMove{}), ErrSqueezeNotConfigured)
	for name, r := range map[string]Rect{
		"odd x":      {X: 1, Y: 0, W: 32, H: 32},
		"odd h":      {X: 0, Y: 0, W: 32, H: 31},
		"fraction":   {X: 2.5, Y: 0, W: 32, H: 32},
		"empty":      {X: 0, Y: 0, W: 0, H: 32},
		"outside":    {X: 48, Y: 0, W: 32, H: 32},
		"negative y": {X: 0, Y: -2, W: 32, H: 32},
	} {
		require.Error(t, s.Set(SqueezeConfig{Rect: r}), name)
	}
	require.Error(t, s.Set(SqueezeConfig{Rect: Rect{W: 32, H: 32}, Easing: "bouncy"}))
	require.Error(t, s.Set(SqueezeConfig{Rect: Rect{W: 32, H: 32}, DurationMs: -1}))
	require.NoError(t, s.Set(SqueezeConfig{Rect: Rect{W: 64, H: 64}}), "the full frame is a valid rect")
	require.Error(t, s.On(SqueezeMove{DurationMs: ms(-1)}))
}

func TestSqueeze_DurationCapped(t *testing.T) {
	s, _ := newTestSqueeze(t, "")
	full := Rect{W: 64, H: 64}
	require.NoError(t, s.Set(SqueezeConfig{Rect: full, DurationMs: MaxMorphDurationMs}))
	require.Error(t, s.Set(SqueezeConfig{Rect: full, DurationMs: MaxMorphDurationMs + 1}))
	require.NoError(t, s.On(SqueezeMove{DurationMs: ms(MaxMorphDurationMs)}))
	require.Error(t, s.On(SqueezeMove{DurationMs: ms(MaxMorphDurationMs + 1)}))
	// Past the cap, time.Duration overflows and the move never arrives.
	require.Error(t, s.Off(SqueezeMove{DurationMs: ms(math.MaxInt)}))
}
