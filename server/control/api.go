// Package control provides the REST API for the Switchframe video switcher.
// It exposes HTTP endpoints for cut, preview, transition, state retrieval,
// and source listing. All commands are POST requests with JSON bodies;
// state queries are GET requests returning JSON.
package control

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/output"
	"github.com/zsiec/switchframe/server/preset"
	"github.com/zsiec/switchframe/server/stinger"
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
	SetTrim(sourceKey string, trimDB float64) error
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
	ConfidenceThumbnail() []byte
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

// WithPresetStore attaches a preset store to the API.
func WithPresetStore(ps *preset.PresetStore) APIOption {
	return func(a *API) { a.presetStore = ps }
}

// WithCompositor attaches a graphics compositor to the API.
func WithCompositor(c *graphics.Compositor) APIOption {
	return func(a *API) { a.compositor = c }
}

// WithStingerStore attaches a stinger clip store to the API.
func WithStingerStore(s *stinger.StingerStore) APIOption {
	return func(a *API) { a.stingerStore = s }
}

// API wraps a Switcher and exposes it over HTTP.
type API struct {
	switcher     *switcher.Switcher
	mixer        AudioMixerAPI
	outputMgr    OutputManagerAPI
	debug        DebugAPI
	presetStore  *preset.PresetStore
	compositor   *graphics.Compositor
	stingerStore *stinger.StingerStore
	mux          *http.ServeMux
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
	mux.HandleFunc("POST /api/audio/trim", a.handleAudioTrim)
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
	mux.HandleFunc("GET /api/output/confidence", a.handleConfidence)
	if a.debug != nil {
		mux.HandleFunc("GET /api/debug/snapshot", a.debug.HandleSnapshot)
	}
	if a.presetStore != nil {
		mux.HandleFunc("GET /api/presets", a.handleListPresets)
		mux.HandleFunc("POST /api/presets", a.handleCreatePreset)
		mux.HandleFunc("GET /api/presets/{id}", a.handleGetPreset)
		mux.HandleFunc("PUT /api/presets/{id}", a.handleUpdatePreset)
		mux.HandleFunc("DELETE /api/presets/{id}", a.handleDeletePreset)
		mux.HandleFunc("POST /api/presets/{id}/recall", a.handleRecallPreset)
	}
	if a.stingerStore != nil {
		mux.HandleFunc("GET /api/stinger/list", a.handleStingerList)
		mux.HandleFunc("DELETE /api/stinger/{name}", a.handleStingerDelete)
		mux.HandleFunc("POST /api/stinger/{name}/cut-point", a.handleStingerCutPoint)
	}
	if a.compositor != nil {
		mux.HandleFunc("POST /api/graphics/on", a.handleGraphicsOn)
		mux.HandleFunc("POST /api/graphics/off", a.handleGraphicsOff)
		mux.HandleFunc("POST /api/graphics/auto-on", a.handleGraphicsAutoOn)
		mux.HandleFunc("POST /api/graphics/auto-off", a.handleGraphicsAutoOff)
		mux.HandleFunc("GET /api/graphics/status", a.handleGraphicsStatus)
		mux.HandleFunc("POST /api/graphics/frame", a.handleGraphicsFrame)
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
	Source        string `json:"source"`
	Type          string `json:"type"`
	DurationMs    int    `json:"durationMs"`
	WipeDirection string `json:"wipeDirection,omitempty"`
	StingerName   string `json:"stingerName,omitempty"`
}

// handleTransition starts a mix, dip, wipe, or stinger transition to the specified source.
func (a *API) handleTransition(w http.ResponseWriter, r *http.Request) {
	var req transitionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if req.Type != "mix" && req.Type != "dip" && req.Type != "wipe" && req.Type != "stinger" {
		http.Error(w, `{"error":"type must be 'mix', 'dip', 'wipe', or 'stinger'"}`, http.StatusBadRequest)
		return
	}
	if req.Type == "wipe" {
		wd := transition.WipeDirection(req.WipeDirection)
		if !transition.ValidWipeDirections[wd] {
			http.Error(w, `{"error":"wipeDirection must be one of: h-left, h-right, v-top, v-bottom, box-center-out, box-edges-in"}`, http.StatusBadRequest)
			return
		}
	}
	if req.Type == "stinger" {
		if a.stingerStore == nil {
			http.Error(w, `{"error":"stinger store not configured"}`, http.StatusNotImplemented)
			return
		}
		if req.StingerName == "" {
			http.Error(w, `{"error":"stingerName required for stinger transition"}`, http.StatusBadRequest)
			return
		}
	}
	if req.DurationMs < 100 || req.DurationMs > 5000 {
		http.Error(w, `{"error":"durationMs must be 100-5000"}`, http.StatusBadRequest)
		return
	}

	// Build transition options
	var opts []switcher.TransitionOption
	if req.Type == "stinger" {
		clip, ok := a.stingerStore.Get(req.StingerName)
		if !ok {
			http.Error(w, `{"error":"stinger clip not found"}`, http.StatusNotFound)
			return
		}
		sd := clipToStingerData(clip)
		opts = append(opts, switcher.WithStingerData(sd))
	}

	if err := a.switcher.StartTransition(r.Context(), req.Source, req.Type, req.DurationMs, req.WipeDirection, opts...); err != nil {
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

// clipToStingerData converts a stinger.StingerClip to transition.StingerData.
func clipToStingerData(clip *stinger.StingerClip) *transition.StingerData {
	frames := make([]transition.StingerFrameData, len(clip.Frames))
	for i, f := range clip.Frames {
		frames[i] = transition.StingerFrameData{
			YUV:   f.YUV,
			Alpha: f.Alpha,
		}
	}
	return &transition.StingerData{
		Frames:   frames,
		Width:    clip.Width,
		Height:   clip.Height,
		CutPoint: clip.CutPoint,
	}
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

// audioTrimRequest is the JSON body for the audio trim endpoint.
type audioTrimRequest struct {
	Source string  `json:"source"`
	Trim   float64 `json:"trim"`
}

// handleAudioTrim sets the input trim for a source channel.
func (a *API) handleAudioTrim(w http.ResponseWriter, r *http.Request) {
	if a.mixer == nil {
		http.Error(w, `{"error":"audio mixer not configured"}`, http.StatusNotImplemented)
		return
	}
	var req audioTrimRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if req.Source == "" {
		http.Error(w, `{"error":"source required"}`, http.StatusBadRequest)
		return
	}
	if err := a.mixer.SetTrim(req.Source, req.Trim); err != nil {
		w.Header().Set("Content-Type", "application/json")
		status := http.StatusNotFound
		if errors.Is(err, audio.ErrInvalidTrim) {
			status = http.StatusBadRequest
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(a.switcher.State())
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

// handleConfidence returns the latest JPEG confidence thumbnail from the
// program output. Returns 204 No Content if no thumbnail is available.
func (a *API) handleConfidence(w http.ResponseWriter, r *http.Request) {
	if a.outputMgr == nil {
		http.Error(w, "output not configured", http.StatusNotImplemented)
		return
	}
	jpg := a.outputMgr.ConfidenceThumbnail()
	if jpg == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(jpg)
}

// --- Preset API ---

// createPresetRequest is the JSON body for creating a preset.
type createPresetRequest struct {
	Name string `json:"name"`
}

// updatePresetRequest is the JSON body for updating a preset.
type updatePresetRequest struct {
	Name string `json:"name"`
}

// recallPresetResponse is the JSON response for recalling a preset.
type recallPresetResponse struct {
	Preset   preset.Preset `json:"preset"`
	Warnings []string      `json:"warnings,omitempty"`
}

// handleListPresets returns all presets.
func (a *API) handleListPresets(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(a.presetStore.List())
}

// handleCreatePreset creates a new preset from the current switcher state.
func (a *API) handleCreatePreset(w http.ResponseWriter, r *http.Request) {
	var req createPresetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, `{"error":"name required"}`, http.StatusBadRequest)
		return
	}

	state := a.switcher.State()
	snapshot := stateToSnapshot(state)

	p, err := a.presetStore.Create(req.Name, snapshot)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(p)
}

// handleGetPreset returns a single preset by ID.
func (a *API) handleGetPreset(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := a.presetStore.Get(id)
	if !ok {
		http.Error(w, `{"error":"preset not found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(p)
}

// handleUpdatePreset updates a preset's name.
func (a *API) handleUpdatePreset(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req updatePresetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}

	updates := preset.PresetUpdate{}
	if req.Name != "" {
		updates.Name = &req.Name
	}

	p, err := a.presetStore.Update(id, updates)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		status := http.StatusInternalServerError
		if errors.Is(err, preset.ErrNotFound) {
			status = http.StatusNotFound
		} else if errors.Is(err, preset.ErrEmptyName) {
			status = http.StatusBadRequest
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(p)
}

// handleDeletePreset deletes a preset by ID.
func (a *API) handleDeletePreset(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.presetStore.Delete(id); err != nil {
		w.Header().Set("Content-Type", "application/json")
		status := http.StatusInternalServerError
		if errors.Is(err, preset.ErrNotFound) {
			status = http.StatusNotFound
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNoContent)
}

// handleRecallPreset applies a preset to the switcher and mixer.
func (a *API) handleRecallPreset(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := a.presetStore.Get(id)
	if !ok {
		http.Error(w, `{"error":"preset not found"}`, http.StatusNotFound)
		return
	}

	target := &apiRecallTarget{
		switcher: a.switcher,
		mixer:    a.mixer,
	}

	warnings := preset.Recall(r.Context(), p, target)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(recallPresetResponse{
		Preset:   p,
		Warnings: warnings,
	})
}

// apiRecallTarget adapts the API's switcher and mixer to the preset.RecallTarget
// interface so Recall() can apply presets without knowing about concrete types.
type apiRecallTarget struct {
	switcher *switcher.Switcher
	mixer    AudioMixerAPI
}

func (t *apiRecallTarget) Cut(ctx context.Context, source string) error {
	return t.switcher.Cut(ctx, source)
}

func (t *apiRecallTarget) SetPreview(ctx context.Context, source string) error {
	return t.switcher.SetPreview(ctx, source)
}

func (t *apiRecallTarget) SetLevel(sourceKey string, levelDB float64) error {
	if t.mixer == nil {
		return nil
	}
	return t.mixer.SetLevel(sourceKey, levelDB)
}

func (t *apiRecallTarget) SetMuted(sourceKey string, muted bool) error {
	if t.mixer == nil {
		return nil
	}
	return t.mixer.SetMuted(sourceKey, muted)
}

func (t *apiRecallTarget) SetAFV(sourceKey string, afv bool) error {
	if t.mixer == nil {
		return nil
	}
	return t.mixer.SetAFV(sourceKey, afv)
}

func (t *apiRecallTarget) SetMasterLevel(level float64) {
	if t.mixer == nil {
		return
	}
	t.mixer.SetMasterLevel(level)
}

// --- Graphics Overlay API ---

// graphicsFrameRequest is the JSON body for the graphics frame upload endpoint.
type graphicsFrameRequest struct {
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Template string `json:"template"`
	RGBA     []byte `json:"rgba"` // base64-encoded in JSON
}

// handleGraphicsOn activates the overlay immediately (CUT ON).
func (a *API) handleGraphicsOn(w http.ResponseWriter, r *http.Request) {
	if err := a.compositor.On(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		status := http.StatusInternalServerError
		if errors.Is(err, graphics.ErrNoOverlay) {
			status = http.StatusBadRequest
		} else if errors.Is(err, graphics.ErrAlreadyActive) {
			status = http.StatusConflict
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(a.compositor.Status())
}

// handleGraphicsOff deactivates the overlay immediately (CUT OFF).
func (a *API) handleGraphicsOff(w http.ResponseWriter, r *http.Request) {
	if err := a.compositor.Off(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		status := http.StatusInternalServerError
		if errors.Is(err, graphics.ErrNotActive) {
			status = http.StatusConflict
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(a.compositor.Status())
}

// handleGraphicsAutoOn starts a 500ms fade-in transition (AUTO ON).
func (a *API) handleGraphicsAutoOn(w http.ResponseWriter, r *http.Request) {
	if err := a.compositor.AutoOn(500 * time.Millisecond); err != nil {
		w.Header().Set("Content-Type", "application/json")
		status := http.StatusInternalServerError
		if errors.Is(err, graphics.ErrNoOverlay) {
			status = http.StatusBadRequest
		} else if errors.Is(err, graphics.ErrFadeActive) {
			status = http.StatusConflict
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(a.compositor.Status())
}

// handleGraphicsAutoOff starts a 500ms fade-out transition (AUTO OFF).
func (a *API) handleGraphicsAutoOff(w http.ResponseWriter, r *http.Request) {
	if err := a.compositor.AutoOff(500 * time.Millisecond); err != nil {
		w.Header().Set("Content-Type", "application/json")
		status := http.StatusInternalServerError
		if errors.Is(err, graphics.ErrNotActive) || errors.Is(err, graphics.ErrFadeActive) {
			status = http.StatusConflict
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(a.compositor.Status())
}

// handleGraphicsStatus returns the current graphics overlay state.
func (a *API) handleGraphicsStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(a.compositor.Status())
}

// handleGraphicsFrame receives an RGBA overlay frame from the browser.
// The body is a JSON object with width, height, template name, and base64-encoded RGBA data.
func (a *API) handleGraphicsFrame(w http.ResponseWriter, r *http.Request) {
	// Limit body to 16MB to prevent abuse (1920*1080*4 = ~8MB).
	body := io.LimitReader(r.Body, 16*1024*1024)

	var req graphicsFrameRequest
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if req.Width <= 0 || req.Height <= 0 {
		http.Error(w, `{"error":"width and height must be positive"}`, http.StatusBadRequest)
		return
	}
	if req.Width > 3840 || req.Height > 2160 {
		http.Error(w, `{"error":"resolution exceeds 4K limit"}`, http.StatusBadRequest)
		return
	}
	expected := req.Width * req.Height * 4
	if len(req.RGBA) != expected {
		http.Error(w, `{"error":"rgba data size mismatch"}`, http.StatusBadRequest)
		return
	}

	if err := a.compositor.SetOverlay(req.RGBA, req.Width, req.Height, req.Template); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(a.compositor.Status())
}

// --- Stinger API ---

// handleStingerList returns all loaded stinger clip names.
func (a *API) handleStingerList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(a.stingerStore.List())
}

// handleStingerDelete removes a stinger clip by name.
func (a *API) handleStingerDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := a.stingerStore.Delete(name); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// stingerCutPointRequest is the JSON body for updating a stinger's cut point.
type stingerCutPointRequest struct {
	CutPoint float64 `json:"cutPoint"`
}

// handleStingerCutPoint updates the cut point for a stinger clip.
func (a *API) handleStingerCutPoint(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req stingerCutPointRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if err := a.stingerStore.SetCutPoint(name, req.CutPoint); err != nil {
		w.Header().Set("Content-Type", "application/json")
		status := http.StatusBadRequest
		if _, ok := a.stingerStore.Get(name); !ok {
			status = http.StatusNotFound
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// stateToSnapshot converts a ControlRoomState to a ControlRoomSnapshot
// for creating presets from the current state.
func stateToSnapshot(state internal.ControlRoomState) preset.ControlRoomSnapshot {
	channels := make(map[string]preset.AudioChannelSnapshot, len(state.AudioChannels))
	for k, ch := range state.AudioChannels {
		channels[k] = preset.AudioChannelSnapshot{
			Level: ch.Level,
			Muted: ch.Muted,
			AFV:   ch.AFV,
		}
	}
	return preset.ControlRoomSnapshot{
		ProgramSource:        state.ProgramSource,
		PreviewSource:        state.PreviewSource,
		TransitionType:       state.TransitionType,
		TransitionDurationMs: state.TransitionDurationMs,
		AudioChannels:        channels,
		MasterLevel:          state.MasterLevel,
	}
}
