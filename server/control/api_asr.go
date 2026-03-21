package control

import (
	"encoding/json"
	"net/http"

	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/internal"
)

// registerASRRoutes registers ASR API routes on the given mux.
func (a *API) registerASRRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/asr/status", a.handleASRStatus)
	mux.HandleFunc("PUT /api/asr/config", a.handleASRConfig)
}

// handleASRStatus returns the current ASR system state.
//
// GET /api/asr/status
func (a *API) handleASRStatus(w http.ResponseWriter, r *http.Request) {
	state := internal.ASRState{Available: false}
	if a.asrManager != nil {
		state.Available = a.asrManager.IsASRAvailable()
		state.Active = a.asrManager.IsASRActive()
		state.Language = a.asrManager.ASRLanguage()
		state.ModelName = a.asrManager.ASRModelName()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(state)
}

// handleASRConfig updates ASR configuration (active state, language).
//
// PUT /api/asr/config
func (a *API) handleASRConfig(w http.ResponseWriter, r *http.Request) {
	if a.asrManager == nil || !a.asrManager.IsASRAvailable() {
		httperr.Write(w, http.StatusNotImplemented, "ASR not available")
		return
	}

	var req struct {
		Active   *bool  `json:"active,omitempty"`
		Language string `json:"language,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Active != nil {
		if err := a.asrManager.SetASRActive(*req.Active); err != nil {
			httperr.Write(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if req.Language != "" {
		a.asrManager.SetASRLanguage(req.Language)
	}

	state := internal.ASRState{
		Available: true,
		Active:    a.asrManager.IsASRActive(),
		Language:  a.asrManager.ASRLanguage(),
		ModelName: a.asrManager.ASRModelName(),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(state)
}
