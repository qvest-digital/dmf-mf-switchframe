package control

import (
	"encoding/json"
	"net/http"

	"github.com/zsiec/switchframe/server/control/httperr"
)

// audioLevelRequest is the JSON body for the audio level endpoint.
type audioLevelRequest struct {
	Source string  `json:"source"`
	Level  float64 `json:"level"`
}

// audioMuteRequest is the JSON body for the audio mute endpoint.
type audioMuteRequest struct {
	Source string `json:"source"`
	Muted  bool   `json:"muted"`
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

// audioTrimRequest is the JSON body for the audio trim endpoint.
type audioTrimRequest struct {
	Source string  `json:"source"`
	Trim   float64 `json:"trim"`
}

// eqRequest is the JSON body for the EQ endpoint.
type eqRequest struct {
	Band      int     `json:"band"`
	Frequency float64 `json:"frequency"`
	Gain      float64 `json:"gain"`
	Q         float64 `json:"q"`
	Enabled   bool    `json:"enabled"`
}

// compressorRequest is the JSON body for the compressor endpoint.
type compressorRequest struct {
	Threshold  float64 `json:"threshold"`
	Ratio      float64 `json:"ratio"`
	Attack     float64 `json:"attack"`
	Release    float64 `json:"release"`
	MakeupGain float64 `json:"makeupGain"`
}

// compressorResponse is the JSON response for the compressor GET endpoint.
type compressorResponse struct {
	Threshold     float64 `json:"threshold"`
	Ratio         float64 `json:"ratio"`
	Attack        float64 `json:"attack"`
	Release       float64 `json:"release"`
	MakeupGain    float64 `json:"makeupGain"`
	GainReduction float64 `json:"gainReduction"`
}

// audioDelayRequest is the JSON body for the audio delay endpoint.
type audioDelayRequest struct {
	DelayMs int `json:"delayMs"`
}

// registerAudioRoutes registers audio-related API routes on the given mux.
func (a *API) registerAudioRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/audio/trim", a.handleAudioTrim)
	mux.HandleFunc("POST /api/audio/level", a.handleAudioLevel)
	mux.HandleFunc("POST /api/audio/mute", a.handleAudioMute)
	mux.HandleFunc("POST /api/audio/afv", a.handleAudioAFV)
	mux.HandleFunc("POST /api/audio/master", a.handleAudioMaster)
	mux.HandleFunc("PUT /api/audio/{source}/eq", a.handleSetEQ)
	mux.HandleFunc("GET /api/audio/{source}/eq", a.handleGetEQ)
	mux.HandleFunc("PUT /api/audio/{source}/compressor", a.handleSetCompressor)
	mux.HandleFunc("GET /api/audio/{source}/compressor", a.handleGetCompressor)
	mux.HandleFunc("PUT /api/audio/{source}/audio-delay", a.handleSetAudioDelay)
}

// handleAudioTrim sets the input trim for a source channel.
func (a *API) handleAudioTrim(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	if a.mixer == nil {
		httperr.Write(w, http.StatusNotImplemented, "audio mixer not configured")
		return
	}
	var req audioTrimRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Source == "" {
		httperr.Write(w, http.StatusBadRequest, "source required")
		return
	}
	if err := a.mixer.SetTrim(req.Source, req.Trim); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleAudioLevel sets the audio level for a source channel.
func (a *API) handleAudioLevel(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	if a.mixer == nil {
		httperr.Write(w, http.StatusNotImplemented, "audio mixer not configured")
		return
	}
	var req audioLevelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Source == "" {
		httperr.Write(w, http.StatusBadRequest, "source required")
		return
	}
	if err := a.mixer.SetLevel(req.Source, req.Level); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleAudioMute sets the mute state for a source channel.
func (a *API) handleAudioMute(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	if a.mixer == nil {
		httperr.Write(w, http.StatusNotImplemented, "audio mixer not configured")
		return
	}
	var req audioMuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Source == "" {
		httperr.Write(w, http.StatusBadRequest, "source required")
		return
	}
	if err := a.mixer.SetMuted(req.Source, req.Muted); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleAudioAFV sets the audio-follow-video state for a source channel.
func (a *API) handleAudioAFV(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	if a.mixer == nil {
		httperr.Write(w, http.StatusNotImplemented, "audio mixer not configured")
		return
	}
	var req audioAFVRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Source == "" {
		httperr.Write(w, http.StatusBadRequest, "source required")
		return
	}
	if err := a.mixer.SetAFV(req.Source, req.AFV); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleAudioMaster sets the master output level.
func (a *API) handleAudioMaster(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	if a.mixer == nil {
		httperr.Write(w, http.StatusNotImplemented, "audio mixer not configured")
		return
	}
	var req audioMasterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	a.mixer.SetMasterLevel(req.Level)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleSetEQ sets a single EQ band for a source channel.
func (a *API) handleSetEQ(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	if a.mixer == nil {
		httperr.Write(w, http.StatusNotImplemented, "audio mixer not configured")
		return
	}
	source := r.PathValue("source")
	if source == "" {
		httperr.Write(w, http.StatusBadRequest, "source required")
		return
	}
	var req eqRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := a.mixer.SetEQ(source, req.Band, req.Frequency, req.Gain, req.Q, req.Enabled); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleGetEQ returns the current EQ settings for a source channel.
func (a *API) handleGetEQ(w http.ResponseWriter, r *http.Request) {
	if a.mixer == nil {
		httperr.Write(w, http.StatusNotImplemented, "audio mixer not configured")
		return
	}
	source := r.PathValue("source")
	if source == "" {
		httperr.Write(w, http.StatusBadRequest, "source required")
		return
	}
	bands, err := a.mixer.GetEQ(source)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(bands)
}

// handleSetCompressor sets the compressor parameters for a source channel.
func (a *API) handleSetCompressor(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	if a.mixer == nil {
		httperr.Write(w, http.StatusNotImplemented, "audio mixer not configured")
		return
	}
	source := r.PathValue("source")
	if source == "" {
		httperr.Write(w, http.StatusBadRequest, "source required")
		return
	}
	var req compressorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := a.mixer.SetCompressor(source, req.Threshold, req.Ratio, req.Attack, req.Release, req.MakeupGain); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleGetCompressor returns the current compressor settings and gain reduction for a source channel.
func (a *API) handleGetCompressor(w http.ResponseWriter, r *http.Request) {
	if a.mixer == nil {
		httperr.Write(w, http.StatusNotImplemented, "audio mixer not configured")
		return
	}
	source := r.PathValue("source")
	if source == "" {
		httperr.Write(w, http.StatusBadRequest, "source required")
		return
	}
	state, err := a.mixer.GetCompressor(source)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(compressorResponse{
		Threshold:     state.Threshold,
		Ratio:         state.Ratio,
		Attack:        state.Attack,
		Release:       state.Release,
		MakeupGain:    state.MakeupGain,
		GainReduction: state.GainReduction,
	})
}

// handleSetAudioDelay sets the audio delay in milliseconds for a source channel.
func (a *API) handleSetAudioDelay(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	if a.mixer == nil {
		httperr.Write(w, http.StatusNotImplemented, "audio mixer not configured")
		return
	}
	source := r.PathValue("source")
	if source == "" {
		httperr.Write(w, http.StatusBadRequest, "source required")
		return
	}
	var req audioDelayRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := a.mixer.SetAudioDelay(source, req.DelayMs); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}
