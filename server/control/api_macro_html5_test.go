package control

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/macro"
	"github.com/zsiec/switchframe/server/switcher"
)

func runHTML5Steps(api *API, steps ...macro.Step) error {
	target := &apiMacroTarget{compositor: api.compositor, html5: api}
	return macro.Run(context.Background(), macro.Macro{Name: "html5", Steps: steps}, target, nil)
}

func layerStep(action macro.Action, layerID int, extra map[string]any) macro.Step {
	params := map[string]any{"layerId": float64(layerID)}
	for k, v := range extra {
		params[k] = v
	}
	return macro.Step{Action: action, Params: params}
}

// One button can then do what an operator does across two panels: move the
// squeezeback and switch the graphic around it to another layout. Every
// step reaches the sidecar as its /html5 route would send it.
func TestMacroHTML5_Steps(t *testing.T) {
	mgr := newMockHTMLGfxManager(4)
	api, comp := setupHTML5GfxTestAPI(t, mgr)
	layerID := addLayerViaAPI(t, api)

	require.NoError(t, runHTML5Steps(api,
		layerStep(macro.ActionHTML5Load, layerID, map[string]any{
			"url":  "https://example.com/g.html",
			"data": map[string]any{"layout": "full"},
		}),
		layerStep(macro.ActionHTML5Play, layerID, nil),
		layerStep(macro.ActionHTML5Data, layerID, map[string]any{"data": map[string]any{"layout": "lframe"}}),
		layerStep(macro.ActionHTML5Custom, layerID, map[string]any{"action": "highlight", "payload": map[string]any{"color": "#ff0"}}),
		layerStep(macro.ActionHTML5Custom, layerID, map[string]any{"action": "reset"}),
		layerStep(macro.ActionHTML5Stop, layerID, nil),
	))

	browserID := mgr.BrowserForLayer(layerID)
	require.NotEqual(t, -1, browserID)
	require.Equal(t, "https://example.com/g.html", mgr.urls[browserID])
	require.JSONEq(t, `{"layout":"lframe"}`, mgr.data[browserID], "the last data sent")
	require.True(t, mgr.played[browserID])
	require.True(t, mgr.stopped[browserID])
	require.Len(t, mgr.customs[browserID], 2)
	require.Equal(t, "highlight", mgr.customs[browserID][0].action)
	require.JSONEq(t, `{"color":"#ff0"}`, mgr.customs[browserID][0].payload)
	require.Equal(t, customCall{"reset", ""}, mgr.customs[browserID][1], "no payload, as the route sends none")

	_ = comp
}

func TestMacroHTML5_LoadSendsNoDataUnasked(t *testing.T) {
	mgr := newMockHTMLGfxManager(4)
	api, _ := setupHTML5GfxTestAPI(t, mgr)
	layerID := addLayerViaAPI(t, api)
	require.NoError(t, runHTML5Steps(api, layerStep(macro.ActionHTML5Load, layerID, map[string]any{"url": "https://example.com/g.html"})))
	_, sent := mgr.data[mgr.BrowserForLayer(layerID)]
	require.False(t, sent)
}

// The sidecar is an unsandboxed Chromium whose pixels go to program: a
// macro must not load what the route refuses.
func TestMacroHTML5_LoadRefusesWhatTheRouteRefuses(t *testing.T) {
	templates := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(templates, "g"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(templates, "g", "index.html"), nil, 0o644))
	secret := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(secret, []byte("x"), 0o600))

	for name, url := range map[string]string{
		"plain http":                 "http://cluster.internal/g.html",
		"file outside the templates": "file://" + secret,
		"https without a host":       "https:///g.html",
		"javascript":                 "javascript:alert(1)",
	} {
		mgr := newMockHTMLGfxManager(4)
		mgr.templatesDir = templates
		api, _ := setupHTML5GfxTestAPI(t, mgr)
		err := runHTML5Steps(api, layerStep(macro.ActionHTML5Load, 0, map[string]any{"url": url}))
		require.ErrorContains(t, err, "url not allowed", name)
		require.Empty(t, mgr.allocated, "%s: no browser allocated", name)
	}

	mgr := newMockHTMLGfxManager(4)
	mgr.templatesDir = templates
	api, _ := setupHTML5GfxTestAPI(t, mgr)
	require.NoError(t, runHTML5Steps(api, layerStep(macro.ActionHTML5Load, 0, map[string]any{"url": "file://" + filepath.Join(templates, "g", "index.html")})),
		"a file below the templates directory")
}

func TestMacroHTML5_LoadRollsBackAndReportsTheRoutesErrors(t *testing.T) {
	mgr := newMockHTMLGfxManager(1)
	api, _ := setupHTML5GfxTestAPI(t, mgr)
	load := func(layerID int) error {
		return runHTML5Steps(api, layerStep(macro.ActionHTML5Load, layerID, map[string]any{"url": "https://example.com/g.html"}))
	}
	require.NoError(t, load(0))
	require.ErrorContains(t, load(1), "pool exhausted")

	mgr = newMockHTMLGfxManager(4)
	mgr.loadErr = errors.New("sidecar gone")
	api, _ = setupHTML5GfxTestAPI(t, mgr)
	require.ErrorContains(t, runHTML5Steps(api, layerStep(macro.ActionHTML5Load, 0, map[string]any{"url": "https://example.com/g.html"})), "sidecar gone")
	require.Empty(t, mgr.allocated, "the browser is freed when the load fails")
}

// A step against a graphic that is not there fails the run, as the route
// answers 404 or 503, rather than doing nothing.
func TestMacroHTML5_RefusesWithoutAGraphic(t *testing.T) {
	steps := []macro.Step{
		layerStep(macro.ActionHTML5Data, 3, map[string]any{"data": map[string]any{}}),
		layerStep(macro.ActionHTML5Play, 3, nil),
		layerStep(macro.ActionHTML5Stop, 3, nil),
		layerStep(macro.ActionHTML5Custom, 3, map[string]any{"action": "x"}),
	}
	mgr := newMockHTMLGfxManager(4)
	api, _ := setupHTML5GfxTestAPI(t, mgr)
	noSidecar, _ := setupHTML5GfxTestAPI(t, nil)
	for _, step := range steps {
		require.ErrorContains(t, runHTML5Steps(api, step), "no HTML5 graphic loaded on this layer", string(step.Action))
		require.ErrorContains(t, runHTML5Steps(noSidecar, step), "HTML5 graphics sidecar not running", string(step.Action))
		target := &apiMacroTarget{}
		err := macro.Run(context.Background(), macro.Macro{Name: "x", Steps: []macro.Step{step}}, target, nil)
		require.ErrorContains(t, err, "HTML5 graphics sidecar not running", "a target without the API")
	}
	require.ErrorContains(t, runHTML5Steps(noSidecar, layerStep(macro.ActionHTML5Load, 0, map[string]any{"url": "https://example.com/g.html"})),
		"HTML5 graphics sidecar not running")
	require.Empty(t, mgr.data)
	require.Empty(t, mgr.played)
}

// Whether a URL may be loaded depends on the engine's templates directory,
// which macro validation cannot see: the API refuses it when the macro is
// saved, not first when an operator presses the button on air.
func TestMacroHTML5_SaveRefusesAURLTheRouteRefuses(t *testing.T) {
	programRelay := distribution.NewRelay()
	sw := switcher.NewTestSwitcher(t, programRelay)
	comp := graphics.NewCompositor()
	t.Cleanup(comp.Close)
	store, err := macro.NewStore(filepath.Join(t.TempDir(), "macros.json"))
	require.NoError(t, err)
	api := NewAPI(sw, WithCompositor(comp), WithMacroStore(store))
	api.SetHTMLGfxManager(newMockHTMLGfxManager(4))

	body := func(url string) string {
		return fmt.Sprintf(`{"steps":[{"action":"html5_load","params":{"layerId":1,"url":%q}}]}`, url)
	}
	rec := serve(t, api, http.MethodPut, "/api/macros/load", body("http://cluster.internal/g.html"))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "step 0: url not allowed")
	require.Empty(t, store.List())

	rec = serve(t, api, http.MethodPut, "/api/macros/load", body("https://example.com/g.html"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = serve(t, api, http.MethodPut, "/api/macros/bad", `{"steps":[{"action":"html5_play","params":{}}]}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.True(t, strings.Contains(rec.Body.String(), "html5_play requires 'layerId'"), rec.Body.String())
}
