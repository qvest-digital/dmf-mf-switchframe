package control

import (
	"encoding/json"
	"errors"
	"image"
	"net/http"
	"strings"

	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/htmlgfx"
)

// html5LoadRequest is the JSON body for loading an HTML5 graphic on a layer.
type html5LoadRequest struct {
	URL  string          `json:"url"`
	Data json.RawMessage `json:"data,omitempty"`
}

// html5UpdateDataRequest is the JSON body for updating HTML5 data on a layer.
type html5UpdateDataRequest struct {
	Data json.RawMessage `json:"data"`
}

// html5CustomRequest is the JSON body for a custom action on an HTML5 layer.
type html5CustomRequest struct {
	Action  string          `json:"action"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// registerHTML5GraphicsRoutes registers HTML5 graphics API routes on the given mux.
func (a *API) registerHTML5GraphicsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/graphics/html5/templates", a.handleHTML5Templates)
	mux.HandleFunc("POST /api/graphics/{id}/html5", a.handleHTML5Load)
	mux.HandleFunc("PUT /api/graphics/{id}/html5/data", a.handleHTML5UpdateData)
	mux.HandleFunc("POST /api/graphics/{id}/html5/play", a.handleHTML5Play)
	mux.HandleFunc("POST /api/graphics/{id}/html5/stop", a.handleHTML5Stop)
	mux.HandleFunc("POST /api/graphics/{id}/html5/next", a.handleHTML5Next)
	mux.HandleFunc("POST /api/graphics/{id}/html5/custom", a.handleHTML5Custom)
	mux.HandleFunc("DELETE /api/graphics/{id}/html5", a.handleHTML5Unload)
}

// handleHTML5Templates returns the list of available HTML5 templates.
func (a *API) handleHTML5Templates(w http.ResponseWriter, r *http.Request) {
	if a.htmlGfxMgr == nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]struct{}{})
		return
	}
	templates := a.htmlGfxMgr.DiscoverTemplates()
	if templates == nil {
		templates = []htmlgfx.TemplateInfo{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(templates)
}

// html5Available checks that the HTML5 manager is connected to a running sidecar.
func (a *API) html5Available(w http.ResponseWriter) bool {
	if err := a.html5Running(); err != nil {
		writeHTML5Error(w, err)
		return false
	}
	return true
}

// html5Error is an HTML5 operation refused with the status its route
// answers with, so a macro step and a request are refused alike.
type html5Error struct {
	status int
	msg    string
}

func (e *html5Error) Error() string { return e.msg }

func writeHTML5Error(w http.ResponseWriter, err error) {
	var he *html5Error
	if errors.As(err, &he) {
		httperr.Write(w, he.status, he.msg)
		return
	}
	httperr.Write(w, http.StatusInternalServerError, err.Error())
}

func (a *API) html5Running() error {
	if a.htmlGfxMgr == nil || !a.htmlGfxMgr.Started() {
		return &html5Error{http.StatusServiceUnavailable, "HTML5 graphics sidecar not running"}
	}
	return nil
}

// html5Browser returns the browser of the HTML5 graphic on the layer.
func (a *API) html5Browser(layerID int) (int, error) {
	if err := a.html5Running(); err != nil {
		return 0, err
	}
	browserID := a.htmlGfxMgr.BrowserForLayer(layerID)
	if browserID == -1 {
		return 0, &html5Error{http.StatusNotFound, "no HTML5 graphic loaded on this layer"}
	}
	return browserID, nil
}

// loadHTML5 allocates a browser for the layer, loads the URL, and sends the
// initial data if any.
func (a *API) loadHTML5(layerID int, url string, data json.RawMessage) error {
	if err := a.html5Running(); err != nil {
		return err
	}
	if url == "" {
		return &html5Error{http.StatusBadRequest, "url is required"}
	}
	if err := a.htmlGfxMgr.CheckURL(url); err != nil {
		return &html5Error{http.StatusBadRequest, err.Error()}
	}

	a.layerBindMu.Lock()
	browserID, err := a.htmlGfxMgr.AllocateBrowser(layerID)
	a.layerBindMu.Unlock()
	if err != nil {
		if strings.Contains(err.Error(), "pool exhausted") {
			return &html5Error{http.StatusConflict, err.Error()}
		}
		return err
	}

	// Set the layer rect to full-frame so the HTML5 overlay covers the entire
	// program output. The template renders at full resolution (1920x1080).
	status := a.compositor.Status()
	if status.ProgramWidth > 0 && status.ProgramHeight > 0 {
		_ = a.compositor.SetLayerRect(layerID, image.Rect(
			0, 0, status.ProgramWidth, status.ProgramHeight))
	}

	if err := a.htmlGfxMgr.LoadURL(browserID, url); err != nil {
		// Roll back allocation on load failure.
		a.htmlGfxMgr.FreeBrowser(browserID)
		return err
	}

	// Send initial data if provided. The URL is loaded, so a failed send
	// is reported without rolling it back.
	if len(data) > 0 {
		return a.htmlGfxMgr.Update(browserID, string(data))
	}
	return nil
}

// customHTML5 calls a custom action of the HTML5 graphic on the layer.
func (a *API) customHTML5(layerID int, action string, payload json.RawMessage) error {
	browserID, err := a.html5Browser(layerID)
	if err != nil {
		return err
	}
	if action == "" {
		return &html5Error{http.StatusBadRequest, "action is required"}
	}
	return a.htmlGfxMgr.CustomAction(browserID, action, string(payload))
}

// handleHTML5Load allocates a browser for the layer, loads the URL, and optionally sends initial data.
func (a *API) handleHTML5Load(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	if !a.html5Available(w) {
		return
	}

	layerID, err := parseLayerID(r)
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid layer id")
		return
	}

	var req html5LoadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	if err := a.loadHTML5(layerID, req.URL, req.Data); err != nil {
		writeHTML5Error(w, err)
		return
	}

	a.broadcast()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.compositor.Status())
}

// handleHTML5UpdateData sends updated data to the HTML5 graphic on a layer.
func (a *API) handleHTML5UpdateData(w http.ResponseWriter, r *http.Request) {
	a.handleHTML5Command(w, r, func(browserID int) error {
		var req html5UpdateDataRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return &html5Error{http.StatusBadRequest, "invalid json"}
		}
		return a.htmlGfxMgr.Update(browserID, string(req.Data))
	})
}

// handleHTML5Play sends a play command to the HTML5 graphic on a layer.
func (a *API) handleHTML5Play(w http.ResponseWriter, r *http.Request) {
	a.handleHTML5Command(w, r, func(browserID int) error { return a.htmlGfxMgr.Play(browserID) })
}

// handleHTML5Stop sends a stop command to the HTML5 graphic on a layer.
func (a *API) handleHTML5Stop(w http.ResponseWriter, r *http.Request) {
	a.handleHTML5Command(w, r, func(browserID int) error { return a.htmlGfxMgr.Stop(browserID) })
}

// handleHTML5Command runs fn on the browser of the HTML5 graphic on the
// request's layer and answers with the graphics state.
func (a *API) handleHTML5Command(w http.ResponseWriter, r *http.Request, fn func(browserID int) error) {
	a.setLastOperator(r)
	if !a.html5Available(w) {
		return
	}

	layerID, err := parseLayerID(r)
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid layer id")
		return
	}

	browserID, err := a.html5Browser(layerID)
	if err == nil {
		err = fn(browserID)
	}
	if err != nil {
		writeHTML5Error(w, err)
		return
	}

	a.broadcast()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.compositor.Status())
}

// handleHTML5Next sends a next command to the HTML5 graphic on a layer.
func (a *API) handleHTML5Next(w http.ResponseWriter, r *http.Request) {
	a.handleHTML5Command(w, r, func(browserID int) error { return a.htmlGfxMgr.Next(browserID) })
}

// handleHTML5Custom sends a custom action to the HTML5 graphic on a layer.
func (a *API) handleHTML5Custom(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	if !a.html5Available(w) {
		return
	}

	layerID, err := parseLayerID(r)
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid layer id")
		return
	}

	if _, err := a.html5Browser(layerID); err != nil {
		writeHTML5Error(w, err)
		return
	}

	var req html5CustomRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	if err := a.customHTML5(layerID, req.Action, req.Payload); err != nil {
		writeHTML5Error(w, err)
		return
	}

	a.broadcast()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.compositor.Status())
}

// handleHTML5Unload frees the browser associated with the layer.
func (a *API) handleHTML5Unload(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	if !a.html5Available(w) {
		return
	}

	layerID, err := parseLayerID(r)
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid layer id")
		return
	}

	browserID := a.htmlGfxMgr.BrowserForLayer(layerID)
	if browserID == -1 {
		httperr.Write(w, http.StatusNotFound, "no HTML5 graphic loaded on this layer")
		return
	}

	a.htmlGfxMgr.FreeBrowser(browserID)

	a.broadcast()
	w.WriteHeader(http.StatusNoContent)
}
