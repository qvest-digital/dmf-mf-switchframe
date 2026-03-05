// Package control provides the REST API for the Switchframe video switcher.
// It exposes HTTP endpoints for cut, preview, transition, state retrieval,
// and source listing. All commands are POST requests with JSON bodies;
// state queries are GET requests returning JSON.
package control

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/zsiec/switchframe/server/output"
	"github.com/zsiec/switchframe/server/switcher"
	"github.com/zsiec/switchframe/server/transition"
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

// OutputManagerAPI is the interface for recording and SRT output operations
// used by the REST API.
type OutputManagerAPI interface {
	StartRecording(config output.RecorderConfig) error
	StopRecording() error
	RecordingStatus() output.RecordingStatus
	StartSRTOutput(config output.SRTOutputConfig) error
	StopSRTOutput() error
	SRTOutputStatus() output.SRTOutputStatus
}

// DebugAPI is the interface for the debug snapshot endpoint.
type DebugAPI interface {
	HandleSnapshot(w http.ResponseWriter, r *http.Request)
}

// APIOption configures optional API dependencies.
type APIOption func(*API)

// WithMixer attaches an audio mixer to the API.
func WithMixer(m AudioMixerAPI) APIOption {
	return func(a *API) { a.mixer = m }
}

// WithOutputManager attaches a recording/SRT output manager to the API.
func WithOutputManager(m OutputManagerAPI) APIOption {
	return func(a *API) { a.outputMgr = m }
}

// WithDebugCollector attaches a debug snapshot handler to the API.
func WithDebugCollector(d DebugAPI) APIOption {
	return func(a *API) { a.debug = d }
}

// API wraps a Switcher and exposes it over HTTP.
type API struct {
	switcher  *switcher.Switcher
	mixer     AudioMixerAPI
	outputMgr OutputManagerAPI
	debug     DebugAPI
	mux       *http.ServeMux
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
	mux.HandleFunc("POST /api/switch/transition/position", a.handleTransitionPosition)
	mux.HandleFunc("POST /api/switch/ftb", a.handleFTB)
	mux.HandleFunc("GET /api/switch/state", a.handleState)
	mux.HandleFunc("GET /api/sources", a.handleSources)
	mux.HandleFunc("POST /api/sources/{key}/label", a.handleSetLabel)
	mux.HandleFunc("POST /api/sources/{key}/delay", a.handleSetDelay)
	mux.HandleFunc("POST /api/audio/level", a.handleAudioLevel)
	mux.HandleFunc("POST /api/audio/mute", a.handleAudioMute)
	mux.HandleFunc("POST /api/audio/afv", a.handleAudioAFV)
	mux.HandleFunc("POST /api/audio/master", a.handleAudioMaster)
	mux.HandleFunc("POST /api/recording/start", a.handleRecordingStart)
	mux.HandleFunc("POST /api/recording/stop", a.handleRecordingStop)
	mux.HandleFunc("GET /api/recording/status", a.handleRecordingStatus)
	mux.HandleFunc("POST /api/output/srt/start", a.handleSRTStart)
	mux.HandleFunc("POST /api/output/srt/stop", a.handleSRTStop)
	mux.HandleFunc("GET /api/output/srt/status", a.handleSRTStatus)
	if a.debug != nil {
		mux.HandleFunc("GET /api/debug/snapshot", a.debug.HandleSnapshot)
	}
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
		status := http.StatusInternalServerError
		if errors.Is(err, switcher.ErrSourceNotFound) {
			status = http.StatusNotFound
		}
		w.WriteHeader(status)
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
		status := http.StatusInternalServerError
		if errors.Is(err, switcher.ErrSourceNotFound) {
			status = http.StatusNotFound
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(a.switcher.State())
}

// transitionRequest is the JSON body for transition commands.
type transitionRequest struct {
	Source     string `json:"source"`
	Type       string `json:"type"`
	DurationMs int    `json:"durationMs"`
}

// handleTransition starts a mix or dip transition to the specified source.
func (a *API) handleTransition(w http.ResponseWriter, r *http.Request) {
	var req transitionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if req.Type != "mix" && req.Type != "dip" {
		http.Error(w, `{"error":"type must be 'mix' or 'dip'"}`, http.StatusBadRequest)
		return
	}
	if req.DurationMs < 100 || req.DurationMs > 5000 {
		http.Error(w, `{"error":"durationMs must be 100-5000"}`, http.StatusBadRequest)
		return
	}
	if err := a.switcher.StartTransition(r.Context(), req.Source, req.Type, req.DurationMs); err != nil {
		w.Header().Set("Content-Type", "application/json")
		status := http.StatusInternalServerError
		if errors.Is(err, switcher.ErrSourceNotFound) {
			status = http.StatusNotFound
		} else if errors.Is(err, transition.ErrTransitionActive) || errors.Is(err, transition.ErrFTBActive) {
			status = http.StatusConflict
		} else if errors.Is(err, switcher.ErrAlreadyOnProgram) {
			status = http.StatusBadRequest
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(a.switcher.State())
}

// transitionPositionRequest is the JSON body for the transition position endpoint.
type transitionPositionRequest struct {
	Position float64 `json:"position"`
}

// handleTransitionPosition sets the T-bar position during an active transition.
func (a *API) handleTransitionPosition(w http.ResponseWriter, r *http.Request) {
	var req transitionPositionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if req.Position < 0 || req.Position > 1 {
		http.Error(w, `{"error":"position must be 0-1"}`, http.StatusBadRequest)
		return
	}
	if err := a.switcher.SetTransitionPosition(r.Context(), req.Position); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(a.switcher.State())
}

// handleFTB starts or toggles a Fade to Black transition.
func (a *API) handleFTB(w http.ResponseWriter, r *http.Request) {
	if err := a.switcher.FadeToBlack(r.Context()); err != nil {
		w.Header().Set("Content-Type", "application/json")
		status := http.StatusInternalServerError
		if errors.Is(err, transition.ErrTransitionActive) || errors.Is(err, transition.ErrFTBActive) {
			status = http.StatusConflict
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(a.switcher.State())
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

// delayRequest is the JSON body for the set-delay command.
type delayRequest struct {
	DelayMs int `json:"delayMs"`
}

// handleSetDelay sets the input delay for a source (0-500ms).
func (a *API) handleSetDelay(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	var req delayRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if err := a.switcher.SetSourceDelay(key, req.DelayMs); err != nil {
		w.Header().Set("Content-Type", "application/json")
		status := http.StatusInternalServerError
		if errors.Is(err, switcher.ErrSourceNotFound) {
			status = http.StatusNotFound
		} else if errors.Is(err, switcher.ErrInvalidDelay) {
			status = http.StatusBadRequest
		}
		w.WriteHeader(status)
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

// --- Recording & SRT Output API ---

// recordingStartRequest is the JSON body for the recording start endpoint.
type recordingStartRequest struct {
	OutputDir      string `json:"outputDir"`
	RotateAfterMins int   `json:"rotateAfterMins,omitempty"` // optional, minutes
	MaxFileSizeMB   int   `json:"maxFileSizeMB,omitempty"`   // optional, megabytes
}

// handleRecordingStart begins recording program output to a file.
func (a *API) handleRecordingStart(w http.ResponseWriter, r *http.Request) {
	if a.outputMgr == nil {
		http.Error(w, `{"error":"output manager not configured"}`, http.StatusNotImplemented)
		return
	}
	var req recordingStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}

	// Default output directory to OS temp dir if not specified.
	if req.OutputDir == "" {
		req.OutputDir = filepath.Join(os.TempDir(), "switchframe-recordings")
	}

	// Validate output directory: must be absolute and cleaned path
	outDir := filepath.Clean(req.OutputDir)
	if !filepath.IsAbs(outDir) {
		http.Error(w, `{"error":"outputDir must be an absolute path"}`, http.StatusBadRequest)
		return
	}

	config := output.RecorderConfig{
		Dir:         outDir,
		RotateAfter: time.Hour, // default: rotate every hour
	}
	if req.RotateAfterMins > 0 {
		config.RotateAfter = time.Duration(req.RotateAfterMins) * time.Minute
	}
	if req.MaxFileSizeMB > 0 {
		config.MaxFileSize = int64(req.MaxFileSizeMB) * 1024 * 1024
	}

	if err := a.outputMgr.StartRecording(config); err != nil {
		w.Header().Set("Content-Type", "application/json")
		status := http.StatusInternalServerError
		if errors.Is(err, output.ErrRecorderActive) {
			status = http.StatusConflict
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(a.outputMgr.RecordingStatus())
}

// handleRecordingStop stops the active recording.
func (a *API) handleRecordingStop(w http.ResponseWriter, r *http.Request) {
	if a.outputMgr == nil {
		http.Error(w, `{"error":"output manager not configured"}`, http.StatusNotImplemented)
		return
	}
	if err := a.outputMgr.StopRecording(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		status := http.StatusInternalServerError
		if errors.Is(err, output.ErrRecorderNotActive) {
			status = http.StatusConflict
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(a.outputMgr.RecordingStatus())
}

// handleRecordingStatus returns the current recording status.
func (a *API) handleRecordingStatus(w http.ResponseWriter, r *http.Request) {
	if a.outputMgr == nil {
		http.Error(w, `{"error":"output manager not configured"}`, http.StatusNotImplemented)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(a.outputMgr.RecordingStatus())
}

// handleSRTStart begins SRT output with the given configuration.
func (a *API) handleSRTStart(w http.ResponseWriter, r *http.Request) {
	if a.outputMgr == nil {
		http.Error(w, `{"error":"output manager not configured"}`, http.StatusNotImplemented)
		return
	}
	var config output.SRTOutputConfig
	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if config.Mode != "caller" && config.Mode != "listener" {
		http.Error(w, `{"error":"mode must be 'caller' or 'listener'"}`, http.StatusBadRequest)
		return
	}
	if config.Port <= 0 {
		http.Error(w, `{"error":"port is required"}`, http.StatusBadRequest)
		return
	}
	if config.Mode == "caller" && config.Address == "" {
		http.Error(w, `{"error":"address is required for caller mode"}`, http.StatusBadRequest)
		return
	}
	if err := a.outputMgr.StartSRTOutput(config); err != nil {
		w.Header().Set("Content-Type", "application/json")
		status := http.StatusInternalServerError
		if errors.Is(err, output.ErrSRTActive) {
			status = http.StatusConflict
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(a.outputMgr.SRTOutputStatus())
}

// handleSRTStop stops the active SRT output.
func (a *API) handleSRTStop(w http.ResponseWriter, r *http.Request) {
	if a.outputMgr == nil {
		http.Error(w, `{"error":"output manager not configured"}`, http.StatusNotImplemented)
		return
	}
	if err := a.outputMgr.StopSRTOutput(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		status := http.StatusInternalServerError
		if errors.Is(err, output.ErrSRTNotActive) {
			status = http.StatusConflict
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(a.outputMgr.SRTOutputStatus())
}

// handleSRTStatus returns the current SRT output status.
func (a *API) handleSRTStatus(w http.ResponseWriter, r *http.Request) {
	if a.outputMgr == nil {
		http.Error(w, `{"error":"output manager not configured"}`, http.StatusNotImplemented)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(a.outputMgr.SRTOutputStatus())
}
