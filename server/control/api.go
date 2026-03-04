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

// AudioMixerAPI is the interface for audio mixer operations used by the REST API.
type AudioMixerAPI interface {
	SetLevel(sourceKey string, levelDB float64) error
	SetMuted(sourceKey string, muted bool) error
	SetAFV(sourceKey string, afv bool) error
	SetMasterLevel(level float64)
}

// APIOption configures optional API dependencies.
type APIOption func(*API)

// WithMixer attaches an audio mixer to the API.
func WithMixer(m AudioMixerAPI) APIOption {
	return func(a *API) { a.mixer = m }
}

// API wraps a Switcher and exposes it over HTTP.
type API struct {
	switcher *switcher.Switcher
	mixer    AudioMixerAPI
	mux      *http.ServeMux
}

// NewAPI creates an API that delegates to sw.
func NewAPI(sw *switcher.Switcher, opts ...APIOption) *API {
	a := &API{switcher: sw, mux: http.NewServeMux()}
	for _, opt := range opts {
		opt(a)
	}
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
	mux.HandleFunc("POST /api/audio/level", a.handleAudioLevel)
	mux.HandleFunc("POST /api/audio/mute", a.handleAudioMute)
	mux.HandleFunc("POST /api/audio/afv", a.handleAudioAFV)
	mux.HandleFunc("POST /api/audio/master", a.handleAudioMaster)
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
	if err := a.switcher.Cut(r.Context(), req.Source); err != nil {
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
	if err := a.switcher.SetPreview(r.Context(), req.Source); err != nil {
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
	if err := a.switcher.SetLabel(r.Context(), key, req.Label); err != nil {
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

// --- Audio API ---

// audioLevelRequest is the JSON body for the audio level endpoint.
type audioLevelRequest struct {
	Source string  `json:"source"`
	Level  float64 `json:"level"`
}

// audioMuteRequest is the JSON body for the audio mute endpoint.
type audioMuteRequest struct {
	Source string `json:"source"`
	Muted bool   `json:"muted"`
}

// audioAFVRequest is the JSON body for the audio AFV endpoint.
type audioAFVRequest struct {
	Source string `json:"source"`
	AFV    bool   `json:"afv"`
}

// audioMasterRequest is the JSON body for the audio master level endpoint.
type audioMasterRequest struct {
	Level float64 `json:"level"`
}

// handleAudioLevel sets the audio level for a source channel.
func (a *API) handleAudioLevel(w http.ResponseWriter, r *http.Request) {
	if a.mixer == nil {
		http.Error(w, `{"error":"audio mixer not configured"}`, http.StatusNotImplemented)
		return
	}
	var req audioLevelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if req.Source == "" {
		http.Error(w, `{"error":"source required"}`, http.StatusBadRequest)
		return
	}
	if err := a.mixer.SetLevel(req.Source, req.Level); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(a.switcher.State())
}

// handleAudioMute sets the mute state for a source channel.
func (a *API) handleAudioMute(w http.ResponseWriter, r *http.Request) {
	if a.mixer == nil {
		http.Error(w, `{"error":"audio mixer not configured"}`, http.StatusNotImplemented)
		return
	}
	var req audioMuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if req.Source == "" {
		http.Error(w, `{"error":"source required"}`, http.StatusBadRequest)
		return
	}
	if err := a.mixer.SetMuted(req.Source, req.Muted); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(a.switcher.State())
}

// handleAudioAFV sets the audio-follow-video state for a source channel.
func (a *API) handleAudioAFV(w http.ResponseWriter, r *http.Request) {
	if a.mixer == nil {
		http.Error(w, `{"error":"audio mixer not configured"}`, http.StatusNotImplemented)
		return
	}
	var req audioAFVRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if req.Source == "" {
		http.Error(w, `{"error":"source required"}`, http.StatusBadRequest)
		return
	}
	if err := a.mixer.SetAFV(req.Source, req.AFV); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(a.switcher.State())
}

// handleAudioMaster sets the master output level.
func (a *API) handleAudioMaster(w http.ResponseWriter, r *http.Request) {
	if a.mixer == nil {
		http.Error(w, `{"error":"audio mixer not configured"}`, http.StatusNotImplemented)
		return
	}
	var req audioMasterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	a.mixer.SetMasterLevel(req.Level)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(a.switcher.State())
}
