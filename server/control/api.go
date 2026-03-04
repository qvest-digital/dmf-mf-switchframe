// Package control provides the REST API for the Switchframe video switcher.
// It exposes HTTP endpoints for cut, preview, transition, state retrieval,
// and source listing. All commands are POST requests with JSON bodies;
// state queries are GET requests returning JSON.
package control

import (
	"encoding/json"
	"net/http"

	"github.com/zsiec/switchframe/server/switcher"
)

// switchRequest is the JSON body for cut and preview commands.
type switchRequest struct {
	Source string `json:"source"`
}

// API wraps a Switcher and exposes it over HTTP.
type API struct {
	switcher *switcher.Switcher
	mux      *http.ServeMux
}

// NewAPI creates an API that delegates to sw.
func NewAPI(sw *switcher.Switcher) *API {
	a := &API{switcher: sw, mux: http.NewServeMux()}
	a.registerRoutes()
	return a
}

// Mux returns the internal ServeMux with all routes registered.
func (a *API) Mux() *http.ServeMux { return a.mux }

// RegisterOnMux registers the API routes on an external ServeMux. This is
// used to mount the control API onto the main Prism HTTP/3 mux via the
// ExtraRoutes hook.
func (a *API) RegisterOnMux(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/switch/cut", a.handleCut)
	mux.HandleFunc("POST /api/switch/preview", a.handlePreview)
	mux.HandleFunc("POST /api/switch/transition", a.handleTransition)
	mux.HandleFunc("GET /api/switch/state", a.handleState)
	mux.HandleFunc("GET /api/sources", a.handleSources)
	mux.HandleFunc("POST /api/sources/{key}/label", a.handleSetLabel)
}

func (a *API) registerRoutes() { a.RegisterOnMux(a.mux) }

// handleCut performs a hard cut to the specified source.
func (a *API) handleCut(w http.ResponseWriter, r *http.Request) {
	var req switchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if req.Source == "" {
		http.Error(w, `{"error":"source required"}`, http.StatusBadRequest)
		return
	}
	if err := a.switcher.Cut(req.Source); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(a.switcher.State())
}

// handlePreview sets the preview source without affecting the program output.
func (a *API) handlePreview(w http.ResponseWriter, r *http.Request) {
	var req switchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if req.Source == "" {
		http.Error(w, `{"error":"source required"}`, http.StatusBadRequest)
		return
	}
	if err := a.switcher.SetPreview(req.Source); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(a.switcher.State())
}

// handleTransition is a placeholder for mix/wipe transitions (Phase 3+).
func (a *API) handleTransition(w http.ResponseWriter, r *http.Request) {
	http.Error(w, `{"error":"transitions not yet implemented"}`, http.StatusNotImplemented)
}

// handleState returns the current ControlRoomState as JSON.
func (a *API) handleState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(a.switcher.State())
}

// labelRequest is the JSON body for the set-label command.
type labelRequest struct {
	Label string `json:"label"`
}

// handleSetLabel sets a human-readable label on a source.
func (a *API) handleSetLabel(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	var req labelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if err := a.switcher.SetLabel(key, req.Label); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(a.switcher.State())
}

// handleSources returns the map of registered sources and their info.
func (a *API) handleSources(w http.ResponseWriter, r *http.Request) {
	state := a.switcher.State()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(state.Sources)
}
