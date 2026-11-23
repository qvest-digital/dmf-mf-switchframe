package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/zsiec/switchframe/server/dve"
	"github.com/zsiec/switchframe/server/htmlgfx"
	"github.com/zsiec/switchframe/server/macro"
)

// startupFile is what --startup-config names: the graphics, squeezeback and
// macros an engine comes up with, which otherwise exist only once someone
// creates them over the API after every start.
//
//	{
//	  "graphics": [
//	    {"layer": 1, "onAir": true, "play": true,
//	     "html5": {"url": "https://example.com/lower-third.html", "data": {"name": "Jane Doe"}}}
//	  ],
//	  "squeezeback": {"rect": {"x": 48, "y": 48, "w": 1344, "h": 756}},
//	  "macros": [
//	    {"name": "L-Frame on", "steps": [{"action": "squeezeback_on", "params": {}}]}
//	  ]
//	}
//
// Each part is applied through the API route that creates it, so it is
// checked as a request to that route is: the squeezeback as PUT
// /api/squeezeback, each macro as PUT /api/macros/{name}, each graphic as
// POST /api/graphics, POST /api/graphics/{id}/html5 and, as asked, POST
// /api/graphics/{id}/on and /html5/play.
type startupFile struct {
	Graphics    []startupGraphic   `json:"graphics,omitempty"`
	Squeezeback *dve.SqueezeConfig `json:"squeezeback,omitempty"`
	Macros      []macro.Macro      `json:"macros,omitempty"`
}

// startupGraphic is an HTML5 graphic on a graphics layer of its own. OnAir
// is not "on": YAML 1.1, which Helm and kubectl read, takes a bare on key
// for true.
type startupGraphic struct {
	Layer *int         `json:"layer"`
	HTML5 startupHTML5 `json:"html5"`
	OnAir bool         `json:"onAir,omitempty"`
	Play  bool         `json:"play,omitempty"`
}

// startupHTML5 takes the fields POST /api/graphics/{id}/html5 takes.
type startupHTML5 struct {
	URL  string          `json:"url"`
	Data json.RawMessage `json:"data,omitempty"`
}

// startupGraphicsWait bounds how long startup graphics wait for the HTML5
// sidecar to come up and for a graphic to play to be ready: the sidecar
// starts after the engine and a graphic loads its page over the network.
const startupGraphicsWait = 2 * time.Minute

// loadStartupFile reads the startup file and refuses what no route would
// take: unknown fields, a graphic without a layer or URL, and a layer or
// macro name used twice, where the second would replace the first.
func loadStartupFile(path string) (startupFile, error) {
	var f startupFile
	data, err := os.ReadFile(path)
	if err != nil {
		return f, fmt.Errorf("read startup config: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return f, fmt.Errorf("parse startup config %s: %w", path, err)
	}
	if dec.More() {
		return f, fmt.Errorf("parse startup config %s: more than one JSON value", path)
	}
	layers := map[int]bool{}
	for i, g := range f.Graphics {
		if g.Layer == nil {
			return f, fmt.Errorf("startup config: graphics[%d]: layer is required", i)
		}
		if *g.Layer < 0 {
			return f, fmt.Errorf("startup config: graphics[%d]: layer must not be negative, got %d", i, *g.Layer)
		}
		if layers[*g.Layer] {
			return f, fmt.Errorf("startup config: graphics[%d]: layer %d is used twice", i, *g.Layer)
		}
		layers[*g.Layer] = true
		if g.HTML5.URL == "" {
			return f, fmt.Errorf("startup config: graphics[%d]: html5.url is required", i)
		}
	}
	names := map[string]bool{}
	for i, m := range f.Macros {
		if m.Name == "" {
			return f, fmt.Errorf("startup config: macros[%d]: name is required", i)
		}
		if names[m.Name] {
			return f, fmt.Errorf("startup config: macros[%d]: name %q is used twice", i, m.Name)
		}
		names[m.Name] = true
	}
	return f, nil
}

// initStartupFile applies the file --startup-config names. A file that does
// not load, or a squeezeback, macro or graphic URL the API refuses, ends
// startup, so a booking never runs without what it declared. Graphics need
// the HTML5 sidecar, which comes up after the engine; they are loaded in the
// background once it runs, and a graphic that fails then is logged.
func (a *App) initStartupFile() error {
	if a.cfg.StartupConfig == "" {
		return nil
	}
	f, err := loadStartupFile(a.cfg.StartupConfig)
	if err != nil {
		return err
	}
	api := a.api.Mux()
	if err := applyStartupNow(api, f); err != nil {
		return fmt.Errorf("startup config: %w", err)
	}
	for i, g := range f.Graphics {
		if err := a.htmlGfxMgr.CheckURL(g.HTML5.URL); err != nil {
			return fmt.Errorf("startup config: graphics[%d]: %w", i, err)
		}
	}
	slog.Info("startup config applied",
		"squeezeback", f.Squeezeback != nil, "macros", len(f.Macros), "graphics", len(f.Graphics))
	if len(f.Graphics) > 0 {
		a.bgWG.Add(1)
		go func() {
			defer a.bgWG.Done()
			applyStartupGraphics(a.bgCtx, api, a.htmlGfxMgr, f.Graphics, startupGraphicsWait)
		}()
	}
	return nil
}

// applyStartupNow applies the squeezeback and the macros.
func applyStartupNow(api http.Handler, f startupFile) error {
	if f.Squeezeback != nil {
		if err := callAPI(api, http.MethodPut, "/api/squeezeback", f.Squeezeback, http.StatusOK); err != nil {
			return fmt.Errorf("squeezeback: %w", err)
		}
	}
	for _, m := range f.Macros {
		if err := callAPI(api, http.MethodPut, "/api/macros/"+url.PathEscape(m.Name), m, http.StatusOK); err != nil {
			return fmt.Errorf("macro %q: %w", m.Name, err)
		}
	}
	return nil
}

// startupSidecar is the HTML5 sidecar as startup graphics wait on it.
type startupSidecar interface {
	Started() bool
	BrowserStates() []htmlgfx.BrowserState
}

// applyStartupGraphics loads the graphics once the sidecar runs, waiting at
// most wait for it and for every graphic to play.
func applyStartupGraphics(ctx context.Context, api http.Handler, sidecar startupSidecar, graphics []startupGraphic, wait time.Duration) {
	deadline := time.Now().Add(wait)
	if !waitUntil(ctx, deadline, sidecar.Started) {
		if ctx.Err() == nil {
			slog.Error("startup config: HTML5 graphics sidecar not running, graphics not loaded",
				"waited", wait, "graphics", len(graphics))
		}
		return
	}
	for _, g := range graphics {
		if err := applyStartupGraphic(ctx, api, sidecar, g, deadline); err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("startup config: graphic not applied", "layer", *g.Layer, "url", g.HTML5.URL, "error", err)
			continue
		}
		slog.Info("startup config: graphic loaded", "layer", *g.Layer, "url", g.HTML5.URL, "onAir", g.OnAir, "play", g.Play)
	}
}

// applyStartupGraphic adds the graphic's layer, loads it, and turns it on
// and plays it as asked. On waits for the page's first frame; a play sent
// before the page has its template adapter is dropped there, so it waits
// until the page reports a format.
func applyStartupGraphic(ctx context.Context, api http.Handler, sidecar startupSidecar, g startupGraphic, deadline time.Time) error {
	id := *g.Layer
	layer := "/api/graphics/" + strconv.Itoa(id)
	// A layer a peer's state brought along is the same layer.
	if err := callAPI(api, http.MethodPost, "/api/graphics", map[string]int{"id": id}, http.StatusCreated, http.StatusConflict); err != nil {
		return fmt.Errorf("add layer: %w", err)
	}
	if err := callAPI(api, http.MethodPost, layer+"/html5", g.HTML5, http.StatusOK); err != nil {
		return fmt.Errorf("load: %w", err)
	}
	if g.OnAir {
		// A layer takes on once the page has painted its first frame into
		// it; until then the route refuses with 400.
		var err error
		waitUntil(ctx, deadline, func() bool {
			err = callAPI(api, http.MethodPost, layer+"/on", nil, http.StatusOK)
			return err == nil
		})
		if err != nil {
			return fmt.Errorf("on: %w", err)
		}
	}
	if !g.Play {
		return nil
	}
	var pageErr error
	ready := waitUntil(ctx, deadline, func() bool {
		for _, bs := range sidecar.BrowserStates() {
			if bs.LayerID != id || bs.Status == "empty" {
				continue
			}
			if bs.Status == "error" {
				pageErr = errors.New("page failed to load")
				return true
			}
			return bs.Format != "" && bs.Format != "unknown"
		}
		return false
	})
	if pageErr != nil {
		return pageErr
	}
	if !ready {
		return errors.New("page reported no template format to play")
	}
	if err := callAPI(api, http.MethodPost, layer+"/html5/play", nil, http.StatusOK); err != nil {
		return fmt.Errorf("play: %w", err)
	}
	return nil
}

// waitUntil polls cond until it holds, the deadline passes or ctx ends.
func waitUntil(ctx context.Context, deadline time.Time, cond func() bool) bool {
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for !cond() {
		if !time.Now().Before(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
		}
	}
	return true
}

// callAPI sends body as JSON to the API route and fails on any status but
// the ones given, with the API's error message.
func callAPI(api http.Handler, method, path string, body any, ok ...int) error {
	var b []byte
	if body != nil {
		var err error
		if b, err = json.Marshal(body); err != nil {
			return err
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	for _, code := range ok {
		if rec.Code == code {
			return nil
		}
	}
	return fmt.Errorf("%s %s: %d %s", method, path, rec.Code, bytes.TrimSpace(rec.Body.Bytes()))
}
