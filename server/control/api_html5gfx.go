package control

import (
	"encoding/json"
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

// handleHTML5Load allocates a browser for the layer, loads the URL, and optionally sends initial data.
func (a *API) handleHTML5Load(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	if a.htmlGfxMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "HTML5 graphics not available")
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

	if req.URL == "" {
		httperr.Write(w, http.StatusBadRequest, "url is required")
		return
	}

	browserID, err := a.htmlGfxMgr.AllocateBrowser(layerID)
	if err != nil {
		if strings.Contains(err.Error(), "pool exhausted") {
			httperr.Write(w, http.StatusConflict, err.Error())
			return
		}
		httperr.Write(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := a.htmlGfxMgr.LoadURL(browserID, req.URL); err != nil {
		// Roll back allocation on load failure.
		a.htmlGfxMgr.FreeBrowser(browserID)
		httperr.Write(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Send initial data if provided.
	if len(req.Data) > 0 {
		if err := a.htmlGfxMgr.Update(browserID, string(req.Data)); err != nil {
			// URL is loaded, data send failed — don't roll back, just report.
			httperr.Write(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	a.broadcast()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.compositor.Status())
}

// handleHTML5UpdateData sends updated data to the HTML5 graphic on a layer.
func (a *API) handleHTML5UpdateData(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	if a.htmlGfxMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "HTML5 graphics not available")
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

	var req html5UpdateDataRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	if err := a.htmlGfxMgr.Update(browserID, string(req.Data)); err != nil {
		httperr.Write(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.broadcast()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.compositor.Status())
}

// handleHTML5Play sends a play command to the HTML5 graphic on a layer.
func (a *API) handleHTML5Play(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	if a.htmlGfxMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "HTML5 graphics not available")
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

	if err := a.htmlGfxMgr.Play(browserID); err != nil {
		httperr.Write(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.broadcast()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.compositor.Status())
}

// handleHTML5Stop sends a stop command to the HTML5 graphic on a layer.
func (a *API) handleHTML5Stop(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	if a.htmlGfxMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "HTML5 graphics not available")
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

	if err := a.htmlGfxMgr.Stop(browserID); err != nil {
		httperr.Write(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.broadcast()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.compositor.Status())
}

// handleHTML5Next sends a next command to the HTML5 graphic on a layer.
func (a *API) handleHTML5Next(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	if a.htmlGfxMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "HTML5 graphics not available")
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

	if err := a.htmlGfxMgr.Next(browserID); err != nil {
		httperr.Write(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.broadcast()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.compositor.Status())
}

// handleHTML5Custom sends a custom action to the HTML5 graphic on a layer.
func (a *API) handleHTML5Custom(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	if a.htmlGfxMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "HTML5 graphics not available")
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

	var req html5CustomRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}

	if req.Action == "" {
		httperr.Write(w, http.StatusBadRequest, "action is required")
		return
	}

	if err := a.htmlGfxMgr.CustomAction(browserID, req.Action, string(req.Payload)); err != nil {
		httperr.Write(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.broadcast()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.compositor.Status())
}

// handleHTML5Unload frees the browser associated with the layer.
func (a *API) handleHTML5Unload(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	if a.htmlGfxMgr == nil {
		httperr.Write(w, http.StatusNotImplemented, "HTML5 graphics not available")
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
