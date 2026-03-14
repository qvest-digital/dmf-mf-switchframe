package control

import (
	"encoding/json"
	"net/http"

	"github.com/zsiec/switchframe/server/control/httperr"
)

// labelRequest is the JSON body for the set-label command.
type labelRequest struct {
	Label string `json:"label"`
}

// registerSourceRoutes registers source-related API routes on the given mux.
func (a *API) registerSourceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sources", a.handleSources)
	mux.HandleFunc("POST /api/sources/{key}/label", a.handleSetLabel)
	mux.HandleFunc("POST /api/sources/{key}/delay", a.handleSetDelay)
	mux.HandleFunc("PUT /api/sources/{key}/position", a.handleSetPosition)
}

// handleSetLabel sets a human-readable label on a source.
func (a *API) handleSetLabel(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	key := r.PathValue("key")
	var req labelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := a.switcher.SetLabel(r.Context(), key, req.Label); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// delayRequest is the JSON body for the set-delay command.
type delayRequest struct {
	DelayMs int `json:"delayMs"`
}

// handleSetDelay sets the input delay for a source (0-500ms).
func (a *API) handleSetDelay(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	key := r.PathValue("key")
	var req delayRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := a.switcher.SetSourceDelay(key, req.DelayMs); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// positionRequest is the JSON body for the set-position command.
type positionRequest struct {
	Position int `json:"position"`
}

// handleSetPosition sets the display position for a source.
func (a *API) handleSetPosition(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	key := r.PathValue("key")
	var req positionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := a.switcher.SetSourcePosition(key, req.Position); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleSources returns the map of registered sources and their info.
func (a *API) handleSources(w http.ResponseWriter, r *http.Request) {
	state := a.enrichedState()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(state.Sources)
}
