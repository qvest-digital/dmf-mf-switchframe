package control

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/internal"
)

// registerASRRoutes registers ASR API routes on the given mux.
func (a *API) registerASRRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/asr/status", a.handleASRStatus)
	mux.HandleFunc("PUT /api/asr/config", a.handleASRConfig)
	mux.HandleFunc("GET /api/asr/models", a.handleASRModels)
	mux.HandleFunc("GET /api/asr/backend/config", a.handleASRBackendConfigGet)
	mux.HandleFunc("PUT /api/asr/backend/config", a.handleASRBackendConfigPut)
}

// handleASRStatus returns the current ASR system state.
//
// GET /api/asr/status
func (a *API) handleASRStatus(w http.ResponseWriter, r *http.Request) {
	state := internal.ASRState{Available: false}
	if a.asrManager != nil {
		state.Available = a.asrManager.IsASRAvailable()
		state.Active = a.asrManager.IsASRActive()
		state.Backend = a.asrManager.ActiveBackend()
		state.AvailableBackends = a.asrManager.AvailableBackends()
		state.ModelName = a.asrManager.ASRModelName()
		state.BackendConfig = a.asrManager.BackendConfig()
		state.ConfidenceFilter = a.asrManager.ASRConfidence()
		state.SpeakerDetection = a.asrManager.ASRSpeakerDetection()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(state)
}

// handleASRConfig updates top-level ASR configuration: active state, backend,
// model, confidence filter, and speaker detection.
//
// PUT /api/asr/config
func (a *API) handleASRConfig(w http.ResponseWriter, r *http.Request) {
	if a.asrManager == nil || !a.asrManager.IsASRAvailable() {
		httperr.Write(w, http.StatusNotImplemented, "ASR not available")
		return
	}

	var req struct {
		Active           *bool  `json:"active,omitempty"`
		Backend          string `json:"backend,omitempty"`
		Model            string `json:"model,omitempty"`
		ConfidenceFilter string `json:"confidenceFilter,omitempty"`
		SpeakerDetection string `json:"speakerDetection,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Backend switch must happen before model swap (new backend may have
	// different model inventory).
	if req.Backend != "" {
		if err := a.asrManager.SwitchBackend(req.Backend); err != nil {
			httperr.WriteErr(w, http.StatusBadRequest, err)
			return
		}
	}
	if req.Model != "" {
		if err := a.asrManager.SetASRModel(req.Model); err != nil {
			httperr.WriteErr(w, http.StatusBadRequest, err)
			return
		}
	}
	if req.Active != nil {
		if err := a.asrManager.SetASRActive(*req.Active); err != nil {
			httperr.Write(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if req.ConfidenceFilter != "" {
		a.asrManager.SetASRConfidence(req.ConfidenceFilter)
	}
	if req.SpeakerDetection != "" {
		a.asrManager.SetASRSpeakerDetection(req.SpeakerDetection)
	}

	state := internal.ASRState{
		Available:         true,
		Active:            a.asrManager.IsASRActive(),
		Backend:           a.asrManager.ActiveBackend(),
		AvailableBackends: a.asrManager.AvailableBackends(),
		ModelName:         a.asrManager.ASRModelName(),
		BackendConfig:     a.asrManager.BackendConfig(),
		ConfidenceFilter:  a.asrManager.ASRConfidence(),
		SpeakerDetection:  a.asrManager.ASRSpeakerDetection(),
	}

	a.broadcast()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(state)
}

// handleASRModels lists available models for the active backend.
//
// GET /api/asr/models
func (a *API) handleASRModels(w http.ResponseWriter, r *http.Request) {
	if a.asrManager == nil {
		httperr.Write(w, http.StatusNotImplemented, "ASR not available")
		return
	}
	models := a.asrManager.AvailableASRModels()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(models)
}

// handleASRBackendConfigGet returns the active backend's configuration as raw JSON.
//
// GET /api/asr/backend/config
func (a *API) handleASRBackendConfigGet(w http.ResponseWriter, r *http.Request) {
	if a.asrManager == nil || !a.asrManager.IsASRAvailable() {
		httperr.Write(w, http.StatusNotImplemented, "ASR not available")
		return
	}
	cfg := a.asrManager.BackendConfig()
	if cfg == nil {
		cfg = json.RawMessage(`{}`)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(cfg)
}

// handleASRBackendConfigPut applies raw JSON configuration to the active backend.
//
// PUT /api/asr/backend/config
func (a *API) handleASRBackendConfigPut(w http.ResponseWriter, r *http.Request) {
	if a.asrManager == nil || !a.asrManager.IsASRAvailable() {
		httperr.Write(w, http.StatusNotImplemented, "ASR not available")
		return
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	if !json.Valid(raw) {
		httperr.Write(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if err := a.asrManager.SetBackendConfig(json.RawMessage(raw)); err != nil {
		httperr.WriteErr(w, http.StatusBadRequest, err)
		return
	}

	a.broadcast()

	// Return updated config.
	cfg := a.asrManager.BackendConfig()
	if cfg == nil {
		cfg = json.RawMessage(`{}`)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(cfg)
}
