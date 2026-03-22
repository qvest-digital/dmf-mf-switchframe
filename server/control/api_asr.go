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
	mux.HandleFunc("GET /api/asr/models", a.handleASRModels)
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
		state.Translate = a.asrManager.ASRTranslate()
		state.ModelName = a.asrManager.ASRModelName()
		state.Speed = a.asrManager.ASRSpeed()
		state.ConfidenceFilter = a.asrManager.ASRConfidence()
		state.NoiseFloor = a.asrManager.ASRNoiseFloor()
		state.SpeakerDetection = a.asrManager.ASRSpeakerDetection()
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
		Active           *bool  `json:"active,omitempty"`
		Language         string `json:"language,omitempty"`
		Translate        *bool  `json:"translate,omitempty"`        // translate to English
		Model            string `json:"model,omitempty"`            // runtime model swap
		Speed            string `json:"speed,omitempty"`            // inference speed preset
		ConfidenceFilter string `json:"confidenceFilter,omitempty"` // confidence holdback preset
		NoiseFloor       string `json:"noiseFloor,omitempty"`       // VAD sensitivity preset
		SpeakerDetection string `json:"speakerDetection,omitempty"` // speaker change gap preset
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid request body")
		return
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
	if req.Language != "" {
		a.asrManager.SetASRLanguage(req.Language)
	}
	if req.Translate != nil {
		a.asrManager.SetASRTranslate(*req.Translate)
	}
	if req.Speed != "" {
		a.asrManager.SetASRSpeed(req.Speed)
	}
	if req.ConfidenceFilter != "" {
		a.asrManager.SetASRConfidence(req.ConfidenceFilter)
	}
	if req.NoiseFloor != "" {
		a.asrManager.SetASRNoiseFloor(req.NoiseFloor)
	}
	if req.SpeakerDetection != "" {
		a.asrManager.SetASRSpeakerDetection(req.SpeakerDetection)
	}

	state := internal.ASRState{
		Available:        true,
		Active:           a.asrManager.IsASRActive(),
		Language:         a.asrManager.ASRLanguage(),
		Translate:        a.asrManager.ASRTranslate(),
		ModelName:        a.asrManager.ASRModelName(),
		Speed:            a.asrManager.ASRSpeed(),
		ConfidenceFilter: a.asrManager.ASRConfidence(),
		NoiseFloor:       a.asrManager.ASRNoiseFloor(),
		SpeakerDetection: a.asrManager.ASRSpeakerDetection(),
	}

	a.broadcast()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(state)
}

// handleASRModels lists available whisper models.
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
