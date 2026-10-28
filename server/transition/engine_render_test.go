package transition

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/internal/video"
)

// renderLeftRight draws from on the left half and to on the right, so the
// result can come from no blend.
func renderLeftRight(calls *int, ok bool) RenderFunc {
	return func(dst, from, to []byte, w, h int, f video.Format, pos float64) bool {
		*calls++
		if !ok {
			return false
		}
		copy(dst, from[:f.FrameSize(w, h)])
		for y := 0; y < h; y++ {
			copy(dst[y*w+w/2:y*w+w], to[y*w+w/2:y*w+w])
		}
		return true
	}
}

// The sync loop renders transitions through BlendRawInto, so a DVE drawn on
// the CPU has to reach it there as well.
func TestBlendRawInto_UsesRender(t *testing.T) {
	calls := 0
	e := NewEngine(EngineConfig{Render: renderLeftRight(&calls, true)})
	e.StartSync("", "", Mix, 1000)
	const w, h = 64, 16
	a, b := makeSolidYUV420(w, h, 80), makeSolidYUV420(w, h, 160)
	got, gw, gh := e.BlendRawInto(nil, a, w, h, b, w, h, 0.5)
	require.Equal(t, 1, calls)
	require.Equal(t, [2]int{w, h}, [2]int{gw, gh})
	require.Equal(t, byte(80), got[0])
	require.Equal(t, byte(160), got[w-1], "the right half comes from the renderer, not a mix")
}

func TestBlendRawInto_RenderDeclinesFallsBackToBlend(t *testing.T) {
	calls := 0
	e := NewEngine(EngineConfig{Render: renderLeftRight(&calls, false)})
	e.StartSync("", "", Mix, 1000)
	const w, h = 64, 16
	got, _, _ := e.BlendRawInto(nil, makeSolidYUV420(w, h, 80), w, h, makeSolidYUV420(w, h, 160), w, h, 0.5)
	require.Equal(t, 1, calls)
	require.InDelta(t, 120, int(got[0]), 1, "a declined frame is mixed")
	require.InDelta(t, 120, int(got[w-1]), 1)
}

// IngestRawFrame drives the engine's own output path.
func TestIngestRawFrame_UsesRender(t *testing.T) {
	for _, f := range []video.Format{video.YUV420_8bit, video.YUV422_10bit} {
		calls := 0
		var out []byte
		var gotFormat video.Format
		e := NewEngine(EngineConfig{
			Render: func(dst, from, to []byte, w, h int, fm video.Format, pos float64) bool {
				calls++
				gotFormat = fm
				for i := range dst {
					dst[i] = 7
				}
				return true
			},
			Output: func(yuv []byte, w, h int, pts int64, kf bool) { out = append([]byte(nil), yuv...) },
		})
		e.SetColorFormat(f)
		require.NoError(t, e.Start("a", "b", Mix, 60000))
		const w, h = 32, 16
		e.IngestRawFrame("a", make([]byte, f.FrameSize(w, h)), w, h, 1)
		e.IngestRawFrame("b", make([]byte, f.FrameSize(w, h)), w, h, 2)
		require.Equal(t, 1, calls, "%v", f)
		require.Equal(t, f, gotFormat)
		require.Len(t, out, f.FrameSize(w, h))
		require.Equal(t, byte(7), out[0])
		e.Stop()
	}
}
