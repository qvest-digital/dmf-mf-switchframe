package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/htmlgfx"
	"github.com/zsiec/switchframe/server/playout"
)

// fakeHTML5 records data updates for the browsers bound to its layers.
type fakeHTML5 struct {
	browserForLayer map[int]int
	updates         []html5Update
}

type html5Update struct {
	browserID int
	data      string
}

func (f *fakeHTML5) BrowserForLayer(layerID int) int {
	if id, ok := f.browserForLayer[layerID]; ok {
		return id
	}
	return -1
}

func (f *fakeHTML5) Update(browserID int, data string) error {
	f.updates = append(f.updates, html5Update{browserID, data})
	return nil
}

// A playlist item's GFX Update events re-label an HTML5 graphic while it is
// on air: each event's data reaches the layer's browser unchanged and at its
// offset, the way an operator's PUT /api/graphics/{id}/html5/data would.
func TestPlayoutSecondary_GraphicsUpdateSendsDataToHTML5Layer(t *testing.T) {
	html5 := &fakeHTML5{browserForLayer: map[int]int{6: 1}}
	sched := playout.NewSecondaryEventScheduler(&playoutSecondaryHandler{html5: html5})

	sched.BeginItem([]playout.SecondaryEvent{
		{Type: "graphics_update", Layer: 6, Template: `{"subtitle": "B"}`, OffsetMs: 5000, Anchor: "start"},
		{Type: "graphics_update", Layer: 6, Template: `{"subtitle": "A"}`, OffsetMs: 0, Anchor: "start"},
	}, 0)

	sched.Tick(0)
	require.Equal(t, []html5Update{{1, `{"subtitle": "A"}`}}, html5.updates)

	sched.Tick(5000)
	require.Equal(t, []html5Update{{1, `{"subtitle": "A"}`}, {1, `{"subtitle": "B"}`}}, html5.updates)
}

// A template-based layer has no browser; the event must not reach another
// layer's graphic.
func TestPlayoutSecondary_GraphicsUpdateLeavesTemplateLayerAlone(t *testing.T) {
	html5 := &fakeHTML5{browserForLayer: map[int]int{6: 1}}
	h := &playoutSecondaryHandler{html5: html5}

	require.NoError(t, h.OnGraphicsUpdate(2, `{"name": "x"}`))
	require.Empty(t, html5.updates)
}

// The sidecar parses the data as JSON, so text that is not JSON would fail
// inside the browser where nobody sees it. Rejecting it here puts the
// failure in the engine log.
func TestPlayoutSecondary_GraphicsUpdateRejectsDataThatIsNotJSON(t *testing.T) {
	html5 := &fakeHTML5{browserForLayer: map[int]int{6: 1}}
	h := &playoutSecondaryHandler{html5: html5}

	require.Error(t, h.OnGraphicsUpdate(6, `subtitle: B`))
	require.Error(t, h.OnGraphicsUpdate(6, ``))
	require.Empty(t, html5.updates)
}

func TestNewPlayoutSecondaryHandler_WiresHTML5Manager(t *testing.T) {
	mgr := &htmlgfx.Manager{}
	h := (&App{htmlGfxMgr: mgr}).newPlayoutSecondaryHandler()
	require.Same(t, mgr, h.html5)

	// Without a manager the field must be a nil interface, not a typed nil
	// that would pass the nil check and panic on first use.
	require.Nil(t, (&App{}).newPlayoutSecondaryHandler().html5)
	require.NoError(t, (&App{}).newPlayoutSecondaryHandler().OnGraphicsUpdate(6, `{}`))
}
