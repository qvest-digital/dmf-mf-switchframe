package control

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/dve"
)

// gfxUpdate is data sent to an HTML5 graphic and when.
type gfxUpdate struct {
	browser int
	data    string
	at      time.Time
}

// recordingGfx records the data sent to HTML5 graphics, from any goroutine.
type recordingGfx struct {
	*mockHTMLGfxManager
	mu      sync.Mutex
	updates []gfxUpdate
}

func (m *recordingGfx) Update(browserID int, data string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updates = append(m.updates, gfxUpdate{browserID, data, time.Now()})
	return nil
}

func (m *recordingGfx) sent() []gfxUpdate {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]gfxUpdate(nil), m.updates...)
}

// squeezeGfxAPI returns an API with a squeezeback configured with the
// graphics offset and an HTML5 graphic loaded on layer 3.
func squeezeGfxAPI(t *testing.T, offsetMs int) (*API, *dve.Squeeze, *recordingGfx) {
	t.Helper()
	api, sq := squeezeAPI(t)
	gfx := &recordingGfx{mockHTMLGfxManager: newMockHTMLGfxManager(2)}
	_, err := gfx.AllocateBrowser(3)
	require.NoError(t, err)
	api.SetHTMLGfxManager(gfx)
	cfg := fmt.Sprintf(`{"rect":{"x":480,"y":0,"w":1440,"h":810},"durationMs":0,"graphicsOffsetMs":%d}`, offsetMs)
	require.Equal(t, http.StatusOK, serve(t, api, "PUT", "/api/squeezeback", cfg).Code)
	return api, sq, gfx
}

const lFrameLayout = `{"graphics":[{"layerId":3,"data":{"layout":"lframe"}}]}`

// Without an offset the graphics change as the move starts, as a console
// sending the two calls back to back has them.
func TestSqueezeGraphics_NoOffsetSendsWithTheMove(t *testing.T) {
	api, sq, gfx := squeezeGfxAPI(t, 0)
	start := time.Now()
	require.Equal(t, http.StatusNoContent, serve(t, api, "POST", "/api/squeezeback/on", lFrameLayout).Code)
	require.True(t, sq.Snapshot().On)
	require.Eventually(t, func() bool { return len(gfx.sent()) == 1 }, 2*time.Second, time.Millisecond)
	sent := gfx.sent()
	assert.JSONEq(t, `{"layout":"lframe"}`, sent[0].data)
	assert.Less(t, sent[0].at.Sub(start), 100*time.Millisecond)
}

// A positive offset sends the graphics that long after the move starts.
func TestSqueezeGraphics_PositiveOffsetSendsAfterTheMove(t *testing.T) {
	api, sq, gfx := squeezeGfxAPI(t, 200)
	start := time.Now()
	require.Equal(t, http.StatusNoContent, serve(t, api, "POST", "/api/squeezeback/on", lFrameLayout).Code)
	require.True(t, sq.Snapshot().On, "the move starts at once")
	require.Empty(t, gfx.sent(), "the graphics wait for the offset")
	require.Eventually(t, func() bool { return len(gfx.sent()) == 1 }, 2*time.Second, 5*time.Millisecond)
	assert.GreaterOrEqual(t, gfx.sent()[0].at.Sub(start), 200*time.Millisecond)
}

// A negative offset, here one the move names over the configured one,
// sends the graphics at once and starts the move that long after.
func TestSqueezeGraphics_NegativeOffsetSendsBeforeTheMove(t *testing.T) {
	api, sq, gfx := squeezeGfxAPI(t, 1000)
	start := time.Now()
	body := `{"graphicsOffsetMs":-200,"graphics":[{"layerId":3,"data":{"layout":"lframe"}}]}`
	require.Equal(t, http.StatusNoContent, serve(t, api, "POST", "/api/squeezeback/on", body).Code)
	require.False(t, sq.Snapshot().On, "the move waits for the offset")
	require.Eventually(t, func() bool { return len(gfx.sent()) == 1 }, 2*time.Second, time.Millisecond)
	require.False(t, sq.Snapshot().On, "the graphics go first")
	require.Eventually(t, func() bool { return sq.Snapshot().On }, 2*time.Second, 5*time.Millisecond)
	assert.GreaterOrEqual(t, time.Since(start), 200*time.Millisecond)
}

// A move drops what an earlier one has still to do: an off sent while an
// on waits for its graphics' head start leaves the program full frame and
// the off's layout on the graphic.
func TestSqueezeGraphics_LaterMoveDropsPendingOne(t *testing.T) {
	api, sq, gfx := squeezeGfxAPI(t, -150)
	require.Equal(t, http.StatusNoContent, serve(t, api, "POST", "/api/squeezeback/on", lFrameLayout).Code)
	off := `{"graphicsOffsetMs":0,"graphics":[{"layerId":3,"data":{"layout":"full"}}]}`
	require.Equal(t, http.StatusNoContent, serve(t, api, "POST", "/api/squeezeback/off", off).Code)
	time.Sleep(300 * time.Millisecond)
	assert.False(t, sq.Snapshot().On, "the on that was waiting did not run")
	sent := gfx.sent()
	require.NotEmpty(t, sent)
	assert.JSONEq(t, `{"layout":"full"}`, sent[len(sent)-1].data, "the later move's layout is the one left")
}

// A sidecar that does not take the graphics holds up neither the move nor
// its caller, which for a timed move is the frame loop.
func TestSqueezeGraphics_SlowSidecarDoesNotHoldTheMove(t *testing.T) {
	api, sq, _ := squeezeGfxAPI(t, 0)
	stuck := &blockingGfx{recordingGfx: &recordingGfx{mockHTMLGfxManager: newMockHTMLGfxManager(2)}, release: make(chan struct{})}
	_, err := stuck.AllocateBrowser(3)
	require.NoError(t, err)
	api.SetHTMLGfxManager(stuck)
	defer close(stuck.release)
	done := make(chan error, 1)
	go func() { done <- api.MoveSqueeze(true, []byte(lFrameLayout)) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("MoveSqueeze waited for the graphics sidecar")
	}
	require.True(t, sq.Snapshot().On)
	require.NoError(t, api.MoveSqueeze(false, []byte(`{"graphics":[{"layerId":3,"data":{}}]}`)), "a second move waits neither")
}

// blockingGfx is a sidecar whose data sends hang until release closes.
type blockingGfx struct {
	*recordingGfx
	release chan struct{}
}

func (m *blockingGfx) Update(browserID int, data string) error {
	<-m.release
	return m.recordingGfx.Update(browserID, data)
}

// Graphics naming a layer without an HTML5 graphic, or no data, refuse the
// move before anything moves.
func TestSqueezeGraphics_RefusedBeforeMoving(t *testing.T) {
	api, sq, gfx := squeezeGfxAPI(t, 0)
	rec := serve(t, api, "POST", "/api/squeezeback/on", `{"graphics":[{"layerId":7,"data":{}}]}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	rec = serve(t, api, "POST", "/api/squeezeback/on", `{"graphics":[{"layerId":3}]}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	rec = serve(t, api, "POST", "/api/squeezeback/on", `{"graphics":[{"data":{}}]}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	rec = serve(t, api, "POST", "/api/squeezeback/on", `{"graphicsOffsetMs":9000,"graphics":[{"layerId":3,"data":{}}]}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.False(t, sq.Snapshot().On)
	assert.Empty(t, gfx.sent())
}

// The squeezeback's state reports the configured offset, and a config
// that leaves it out keeps it.
func TestSqueezeGraphics_OffsetIsConfiguration(t *testing.T) {
	api, sq, _ := squeezeGfxAPI(t, -250)
	require.Equal(t, http.StatusOK, serve(t, api, "PUT", "/api/squeezeback", lFrame).Code)
	assert.Equal(t, -250*time.Millisecond, sq.Snapshot().GraphicsOffset())
}

// A macro's squeezeback step moves as the REST call does: it drops an on
// still waiting for its graphics' head start.
func TestSqueezeGraphics_MacroMoveDropsPendingOne(t *testing.T) {
	api, sq, _ := squeezeGfxAPI(t, -150)
	require.Equal(t, http.StatusNoContent, serve(t, api, "POST", "/api/squeezeback/on", lFrameLayout).Code)
	target := &apiMacroTarget{switcher: api.switcher, squeeze: sq, html5: api}
	require.NoError(t, target.execSqueezebackMove(map[string]any{"durationMs": float64(0)}, false))
	time.Sleep(300 * time.Millisecond)
	assert.False(t, sq.Snapshot().On, "the on that was waiting did not run")
}

// Taking the peer's squeezeback drops a move still waiting on its
// graphics' head start, which would otherwise undo it.
func TestSqueezeGraphics_CancelDropsPendingMove(t *testing.T) {
	api, sq, _ := squeezeGfxAPI(t, -150)
	require.Equal(t, http.StatusNoContent, serve(t, api, "POST", "/api/squeezeback/on", lFrameLayout).Code)
	api.CancelSqueezeMoves()
	time.Sleep(300 * time.Millisecond)
	assert.False(t, sq.Snapshot().On)
}
