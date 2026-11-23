package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/control"
	"github.com/zsiec/switchframe/server/dve"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/htmlgfx"
	"github.com/zsiec/switchframe/server/macro"
	"github.com/zsiec/switchframe/server/switcher"
)

// startupExample is the file the reference documents: an OGraf graphic
// loaded through the ograf-host template and played, an L-frame configured
// and off, and the two macros that move between full frame and L-frame.
const startupExample = `{
  "graphics": [
    {
      "layer": 1,
      "html5": {
        "url": "file:///opt/switchframe-cef/demo-templates/ograf-host/index.html?manifest=https://dmf-ograf.s3.eu-central-1.amazonaws.com/wsj-markets/1.0.0/manifest.ograf.json",
        "data": {"layout": "full"}
      },
      "onAir": true,
      "play": true
    }
  ],
  "squeezeback": {
    "rect": {"x": 48, "y": 48, "w": 1344, "h": 756},
    "durationMs": 500,
    "easing": "ease-in-out"
  },
  "macros": [
    {
      "name": "L-Frame on",
      "steps": [
        {"action": "squeezeback_on", "params": {}},
        {"action": "graphics_html5_data", "params": {"layerId": 1, "data": {"layout": "lframe"}}}
      ]
    },
    {
      "name": "L-Frame off",
      "steps": [
        {"action": "squeezeback_off", "params": {}},
        {"action": "graphics_html5_data", "params": {"layerId": 1, "data": {"layout": "full"}}}
      ]
    }
  ]
}`

func writeStartupFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "startup.json")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func startupTestAPI(t *testing.T) (*control.API, *graphics.Compositor, *dve.Squeeze, *macro.Store) {
	t.Helper()
	comp := graphics.NewCompositor()
	t.Cleanup(comp.Close)
	sq := dve.NewSqueeze(1920, 1080)
	store, err := macro.NewStore(filepath.Join(t.TempDir(), "macros.json"))
	require.NoError(t, err)
	api := control.NewAPI(switcher.NewTestSwitcher(t, distribution.NewRelay()),
		control.WithCompositor(comp), control.WithSqueeze(sq), control.WithMacroStore(store))
	return api, comp, sq, store
}

func TestLoadStartupFile_Example(t *testing.T) {
	f, err := loadStartupFile(writeStartupFile(t, startupExample))
	require.NoError(t, err)
	require.Len(t, f.Graphics, 1)
	require.Equal(t, 1, *f.Graphics[0].Layer)
	require.True(t, f.Graphics[0].OnAir)
	require.True(t, f.Graphics[0].Play)
	require.JSONEq(t, `{"layout":"full"}`, string(f.Graphics[0].HTML5.Data))
	require.Equal(t, dve.Rect{X: 48, Y: 48, W: 1344, H: 756}, f.Squeezeback.Rect)
	require.Len(t, f.Macros, 2)
}

// A typo in a booking's file must stop the engine rather than leave out the
// part it names: a misspelt field would otherwise be dropped in silence.
func TestLoadStartupFile_Refuses(t *testing.T) {
	for name, tc := range map[string]struct{ file, want string }{
		"unknown field": {`{"graphic": []}`, `unknown field "graphic"`},
		"unknown graphic field": {`{"graphics": [{"layer": 1, "html5": {"url": "https://x/"}, "autoplay": true}]}`,
			`unknown field "autoplay"`},
		"unknown html5 field":       {`{"graphics": [{"layer": 1, "html5": {"url": "https://x/", "date": {}}}]}`, `unknown field "date"`},
		"unknown squeezeback field": {`{"squeezeback": {"rect": {"x": 0, "y": 0, "w": 960, "h": 540}, "bg": ""}}`, `unknown field "bg"`},
		"unknown rect field":        {`{"squeezeback": {"rect": {"x": 0, "y": 0, "w": 960, "h": 540, "z": 1}}}`, `unknown field "z"`},
		"unknown macro field":       {`{"macros": [{"name": "m", "steps": [], "hotkey": "F1"}]}`, `unknown field "hotkey"`},
		"unknown step field":        {`{"macros": [{"name": "m", "steps": [{"action": "ftb", "param": {}}]}]}`, `unknown field "param"`},
		"no layer":                  {`{"graphics": [{"html5": {"url": "https://x/"}}]}`, "graphics[0]: layer is required"},
		"negative layer":            {`{"graphics": [{"layer": -1, "html5": {"url": "https://x/"}}]}`, "graphics[0]: layer must not be negative"},
		"layer twice": {`{"graphics": [{"layer": 1, "html5": {"url": "https://x/"}}, {"layer": 1, "html5": {"url": "https://y/"}}]}`,
			"graphics[1]: layer 1 is used twice"},
		"no url":        {`{"graphics": [{"layer": 1, "html5": {}}]}`, "graphics[0]: html5.url is required"},
		"no name":       {`{"macros": [{"steps": [{"action": "ftb"}]}]}`, "macros[0]: name is required"},
		"name twice":    {`{"macros": [{"name": "m", "steps": [{"action": "ftb"}]}, {"name": "m", "steps": [{"action": "cut"}]}]}`, `macros[1]: name "m" is used twice`},
		"two values":    {`{} {}`, "more than one JSON value"},
		"not an object": {`[]`, "cannot unmarshal"},
	} {
		_, err := loadStartupFile(writeStartupFile(t, tc.file))
		require.ErrorContains(t, err, tc.want, name)
	}
	_, err := loadStartupFile(filepath.Join(t.TempDir(), "missing.json"))
	require.ErrorContains(t, err, "read startup config")
}

func TestApplyStartupNow_SqueezebackAndMacros(t *testing.T) {
	f, err := loadStartupFile(writeStartupFile(t, startupExample))
	require.NoError(t, err)
	api, _, sq, store := startupTestAPI(t)

	require.NoError(t, applyStartupNow(api.Mux(), f))

	st := sq.Snapshot()
	require.True(t, st.Configured)
	require.False(t, st.On, "a booking comes up full frame")
	require.Equal(t, dve.Rect{X: 48, Y: 48, W: 1344, H: 756}, st.Rect)
	require.Equal(t, "", st.Background, "black")

	var names []string
	for _, m := range store.List() {
		names = append(names, m.Name)
	}
	require.Equal(t, []string{"L-Frame on", "L-Frame off"}, names)
	on, err := store.Get("L-Frame on")
	require.NoError(t, err)
	require.Equal(t, macro.ActionGraphicsHTML5Data, on.Steps[1].Action)
	require.Equal(t, map[string]any{"layout": "lframe"}, on.Steps[1].Params["data"])
}

// The file is checked as the API checks a request, so what the API refuses
// the file refuses, with the API's reason.
func TestApplyStartupNow_RefusesWhatTheAPIRefuses(t *testing.T) {
	for name, tc := range map[string]struct {
		f    startupFile
		want string
	}{
		"odd rect": {startupFile{Squeezeback: &dve.SqueezeConfig{Rect: dve.Rect{X: 1, W: 960, H: 540}}},
			"squeezeback: PUT /api/squeezeback: 400"},
		"rect outside the frame": {startupFile{Squeezeback: &dve.SqueezeConfig{Rect: dve.Rect{X: 1000, W: 960, H: 540}}},
			"outside the 1920x1080 frame"},
		"unknown background": {startupFile{Squeezeback: &dve.SqueezeConfig{Rect: dve.Rect{W: 960, H: 540}, Background: "nope"}},
			"unknown background source: nope"},
		"unknown action": {startupFile{Macros: []macro.Macro{{Name: "m", Steps: []macro.Step{{Action: "jump"}}}}},
			`unknown action \"jump\"`},
		"html5 data without data": {startupFile{Macros: []macro.Macro{{Name: "m", Steps: []macro.Step{{Action: macro.ActionGraphicsHTML5Data, Params: map[string]any{"layerId": float64(1)}}}}}},
			"requires 'data'"},
	} {
		api, _, _, _ := startupTestAPI(t)
		require.ErrorContains(t, applyStartupNow(api.Mux(), tc.f), tc.want, name)
	}
}

// fakeSidecar is the HTML5 sidecar as the API and startup graphics see it.
// It starts, and pages report their format and first frame, when the test
// says so, as the real one does some time after the engine is up.
type fakeSidecar struct {
	comp *graphics.Compositor

	mu       sync.Mutex
	started  bool
	browsers []*htmlgfx.BrowserState
	calls    []string
}

var _ control.HTMLGfxManager = (*fakeSidecar)(nil)

func (f *fakeSidecar) record(call string) {
	f.calls = append(f.calls, call)
}

func (f *fakeSidecar) Started() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started
}

func (f *fakeSidecar) AllocateBrowser(layerID int) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.browsers = append(f.browsers, &htmlgfx.BrowserState{ID: len(f.browsers), LayerID: layerID, Status: "allocated"})
	f.record("allocate")
	return len(f.browsers) - 1, nil
}

func (f *fakeSidecar) FreeBrowser(browserID int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	*f.browsers[browserID] = htmlgfx.NewBrowserState(browserID)
}

func (f *fakeSidecar) BrowserForLayer(layerID int) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, bs := range f.browsers {
		if bs.LayerID == layerID {
			return bs.ID
		}
	}
	return -1
}

func (f *fakeSidecar) CheckURL(url string) error { return htmlgfx.CheckURL(url, "") }

func (f *fakeSidecar) LoadURL(browserID int, url string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.browsers[browserID].URL = url
	f.browsers[browserID].Status = "loading"
	f.record("load")
	return nil
}

func (f *fakeSidecar) Play(browserID int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("play:" + f.browsers[browserID].Format)
	return nil
}

func (f *fakeSidecar) Update(browserID int, data string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("data:" + data)
	return nil
}

func (f *fakeSidecar) Stop(int) error                            { return nil }
func (f *fakeSidecar) Next(int) error                            { return nil }
func (f *fakeSidecar) CustomAction(int, string, string) error    { return nil }
func (f *fakeSidecar) DiscoverTemplates() []htmlgfx.TemplateInfo { return nil }

func (f *fakeSidecar) BrowserStates() []htmlgfx.BrowserState {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]htmlgfx.BrowserState, len(f.browsers))
	for i, bs := range f.browsers {
		out[i] = *bs
	}
	return out
}

func (f *fakeSidecar) start() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = true
}

// paint delivers the page's first frame into its layer, as the sidecar's
// frame socket does once the page renders.
func (f *fakeSidecar) paint(t *testing.T, layerID int) {
	t.Helper()
	require.NoError(t, f.comp.SetOverlay(layerID, make([]byte, 2*2*4), 2, 2, "html5"))
}

// ready is the page reporting its template format.
func (f *fakeSidecar) ready(layerID int, format string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, bs := range f.browsers {
		if bs.LayerID == layerID {
			bs.Format = format
			bs.Status = "ready"
		}
	}
}

func (f *fakeSidecar) callLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func startupGraphicsTest(t *testing.T) (*control.API, *graphics.Compositor, *fakeSidecar) {
	t.Helper()
	api, comp, _, _ := startupTestAPI(t)
	fake := &fakeSidecar{comp: comp}
	api.SetHTMLGfxManager(fake)
	return api, comp, fake
}

func layerActive(comp *graphics.Compositor, id int) bool {
	for _, l := range comp.Status().Layers {
		if l.ID == id {
			return l.Active
		}
	}
	return false
}

func intPtr(v int) *int { return &v }

// The sidecar comes up after the engine and a page loads after that: the
// graphic is loaded once the sidecar runs, put on air once it has painted,
// and played only once its template can take a play, which it would drop
// before.
func TestApplyStartupGraphics_SidecarReadyLate(t *testing.T) {
	api, comp, fake := startupGraphicsTest(t)
	g := startupGraphic{
		Layer: intPtr(1),
		HTML5: startupHTML5{URL: "https://example.com/g.html", Data: json.RawMessage(`{"layout":"full"}`)},
		OnAir: true,
		Play:  true,
	}
	done := make(chan struct{})
	go func() {
		applyStartupGraphics(context.Background(), api.Mux(), fake, []startupGraphic{g}, 10*time.Second)
		close(done)
	}()

	time.Sleep(300 * time.Millisecond)
	require.Empty(t, fake.callLog(), "nothing is sent before the sidecar runs")
	require.Empty(t, comp.Status().Layers)

	fake.start()
	require.Eventually(t, func() bool { return len(fake.callLog()) == 3 }, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, []string{"allocate", "load", `data:{"layout":"full"}`}, fake.callLog())

	time.Sleep(300 * time.Millisecond)
	require.False(t, layerActive(comp, 1), "no frame, nothing to put on air")
	fake.paint(t, 1)
	require.Eventually(t, func() bool { return layerActive(comp, 1) }, 5*time.Second, 10*time.Millisecond)

	time.Sleep(300 * time.Millisecond)
	require.NotContains(t, fake.callLog(), "play:", "no play before the template reports itself")
	fake.ready(1, "ograf")

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("startup graphics did not finish")
	}
	require.Equal(t, []string{"allocate", "load", `data:{"layout":"full"}`, "play:ograf"}, fake.callLog())
}

func TestApplyStartupGraphics_LoadOnly(t *testing.T) {
	api, comp, fake := startupGraphicsTest(t)
	fake.start()
	g := startupGraphic{Layer: intPtr(4), HTML5: startupHTML5{URL: "https://example.com/g.html"}}
	applyStartupGraphics(context.Background(), api.Mux(), fake, []startupGraphic{g}, 10*time.Second)
	require.Equal(t, []string{"allocate", "load"}, fake.callLog())
	require.Len(t, comp.Status().Layers, 1)
	require.Equal(t, 4, comp.Status().Layers[0].ID, "the layer the file names")
	require.False(t, layerActive(comp, 4))
}

// An engine of a pair that comes back takes the peer's graphics with its
// state; a second browser on the layer would composite the graphic twice.
func TestApplyStartupGraphics_LeavesAPeersGraphic(t *testing.T) {
	api, _, fake := startupGraphicsTest(t)
	fake.start()
	require.Equal(t, http.StatusCreated, serve(api, http.MethodPost, "/api/graphics", `{"id":1}`))
	require.Equal(t, http.StatusOK, serve(api, http.MethodPost, "/api/graphics/1/html5", `{"url":"https://example.com/peer.html"}`))
	g := startupGraphic{Layer: intPtr(1), HTML5: startupHTML5{URL: "https://example.com/g.html"}, Play: true}
	applyStartupGraphics(context.Background(), api.Mux(), fake, []startupGraphic{g}, time.Second)
	require.Equal(t, []string{"allocate", "load"}, fake.callLog(), "only the peer's load")
}

func serve(api *control.API, method, path, body string) int {
	rec := httptest.NewRecorder()
	api.Mux().ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec.Code
}

// Waiting is bounded: a sidecar that never runs, or a page that never gets
// ready, gives up rather than holding a goroutine for the engine's life.
func TestApplyStartupGraphics_GivesUp(t *testing.T) {
	t.Run("sidecar never runs", func(t *testing.T) {
		api, comp, fake := startupGraphicsTest(t)
		g := startupGraphic{Layer: intPtr(1), HTML5: startupHTML5{URL: "https://example.com/g.html"}, Play: true}
		applyStartupGraphics(context.Background(), api.Mux(), fake, []startupGraphic{g}, 300*time.Millisecond)
		require.Empty(t, fake.callLog())
		require.Empty(t, comp.Status().Layers)
	})
	t.Run("page never ready", func(t *testing.T) {
		api, _, fake := startupGraphicsTest(t)
		fake.start()
		g := startupGraphic{Layer: intPtr(1), HTML5: startupHTML5{URL: "https://example.com/g.html"}, Play: true}
		applyStartupGraphics(context.Background(), api.Mux(), fake, []startupGraphic{g}, 300*time.Millisecond)
		require.Equal(t, []string{"allocate", "load"}, fake.callLog(), "no play into a page that cannot take it")
	})
	t.Run("page failed", func(t *testing.T) {
		api, _, fake := startupGraphicsTest(t)
		fake.start()
		g1 := startupGraphic{Layer: intPtr(1), HTML5: startupHTML5{URL: "https://example.com/broken.html"}, Play: true}
		g2 := startupGraphic{Layer: intPtr(2), HTML5: startupHTML5{URL: "https://example.com/g.html"}}
		go func() {
			// The sidecar reports a failed page after the load.
			for {
				fake.mu.Lock()
				if len(fake.browsers) > 0 && fake.browsers[0].Status == "loading" {
					fake.browsers[0].Status = "error"
					fake.mu.Unlock()
					return
				}
				fake.mu.Unlock()
				time.Sleep(10 * time.Millisecond)
			}
		}()
		start := time.Now()
		applyStartupGraphics(context.Background(), api.Mux(), fake, []startupGraphic{g1, g2}, 10*time.Second)
		require.Less(t, time.Since(start), 5*time.Second, "a failed page ends its wait")
		require.Equal(t, []string{"allocate", "load", "allocate", "load"}, fake.callLog(), "the next graphic still loads")
	})
	t.Run("engine shuts down", func(t *testing.T) {
		api, _, fake := startupGraphicsTest(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		start := time.Now()
		applyStartupGraphics(ctx, api.Mux(), fake, []startupGraphic{{Layer: intPtr(1)}}, 10*time.Second)
		require.Less(t, time.Since(start), time.Second)
	})
}

func TestInitStartupFile(t *testing.T) {
	newApp := func(t *testing.T, path string) (*App, *dve.Squeeze) {
		api, comp, sq, _ := startupTestAPI(t)
		ctx, cancel := context.WithCancel(context.Background())
		a := &App{
			cfg:        AppConfig{StartupConfig: path},
			api:        api,
			htmlGfxMgr: htmlgfx.NewManager(comp, htmlgfx.DefaultConfig()),
			bgCtx:      ctx,
			bgCancel:   cancel,
		}
		t.Cleanup(func() {
			cancel()
			a.bgWG.Wait()
		})
		return a, sq
	}

	t.Run("no flag changes nothing", func(t *testing.T) {
		a, sq := newApp(t, "")
		require.NoError(t, a.initStartupFile())
		require.False(t, sq.Snapshot().Configured)
	})
	t.Run("invalid file ends startup", func(t *testing.T) {
		a, _ := newApp(t, writeStartupFile(t, `{"squeezeback": {"rect": {"x": 0, "y": 0, "w": 960, "h": 540}}, "macro": []}`))
		require.ErrorContains(t, a.initStartupFile(), `unknown field "macro"`)
	})
	t.Run("refused graphic URL ends startup", func(t *testing.T) {
		a, _ := newApp(t, writeStartupFile(t, `{"graphics": [{"layer": 1, "html5": {"url": "http://cluster.internal/g.html"}}]}`))
		require.ErrorContains(t, a.initStartupFile(), "graphics[0]")
	})
	t.Run("refused squeezeback ends startup", func(t *testing.T) {
		a, _ := newApp(t, writeStartupFile(t, `{"squeezeback": {"rect": {"x": 0, "y": 0, "w": 961, "h": 540}}}`))
		require.ErrorContains(t, a.initStartupFile(), "startup config: squeezeback")
	})
	t.Run("applies and leaves graphics to the background", func(t *testing.T) {
		a, sq := newApp(t, writeStartupFile(t, `{
		  "graphics": [{"layer": 1, "html5": {"url": "https://example.com/g.html"}, "play": true}],
		  "squeezeback": {"rect": {"x": 48, "y": 48, "w": 1344, "h": 756}}
		}`))
		start := time.Now()
		require.NoError(t, a.initStartupFile())
		require.Less(t, time.Since(start), time.Second, "start does not wait for the sidecar")
		require.True(t, sq.Snapshot().Configured)
	})
}

// The macros the file declares run as the API runs them: L-Frame on moves
// the squeezeback on and sends the graphic its L-frame layout.
func TestStartupMacrosRun(t *testing.T) {
	f, err := loadStartupFile(writeStartupFile(t, startupExample))
	require.NoError(t, err)
	api, comp, sq, _ := startupTestAPI(t)
	fake := &fakeSidecar{comp: comp, started: true}
	api.SetHTMLGfxManager(fake)
	require.NoError(t, applyStartupNow(api.Mux(), f))
	applyStartupGraphics(context.Background(), api.Mux(), fake,
		[]startupGraphic{{Layer: intPtr(1), HTML5: startupHTML5{URL: "https://example.com/g.html"}}}, time.Second)

	run := func(name string) {
		t.Helper()
		rec := httptest.NewRecorder()
		api.Mux().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/macros/"+name+"/run", nil))
		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
		require.Eventually(t, func() bool {
			st := api.MacroState()
			return st != nil && !st.Running
		}, 5*time.Second, 10*time.Millisecond)
		require.Empty(t, api.MacroState().Error)
	}
	run("L-Frame%20on")
	require.True(t, sq.Snapshot().On)
	require.Equal(t, `data:{"layout":"lframe"}`, fake.callLog()[len(fake.callLog())-1])
	run("L-Frame%20off")
	require.False(t, sq.Snapshot().On)
	require.Equal(t, `data:{"layout":"full"}`, fake.callLog()[len(fake.callLog())-1])
}
