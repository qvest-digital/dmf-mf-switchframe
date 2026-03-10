package control

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/layout"
	"github.com/zsiec/switchframe/server/macro"
	"github.com/zsiec/switchframe/server/operator"
	"github.com/zsiec/switchframe/server/output"
	"github.com/zsiec/switchframe/server/preset"
	"github.com/zsiec/switchframe/server/replay"
	"github.com/zsiec/switchframe/server/scte35"
	"github.com/zsiec/switchframe/server/stinger"
	"github.com/zsiec/switchframe/server/switcher"
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
	SetEQ(sourceKey string, band int, frequency, gain, q float64, enabled bool) error
	GetEQ(sourceKey string) ([3]audio.EQBandSettings, error)
	SetCompressor(sourceKey string, threshold, ratio, attack, release, makeupGain float64) error
	GetCompressor(sourceKey string) (audio.CompressorState, error)
	SetAudioDelay(sourceKey string, delayMs int) error
	AudioDelayMs(sourceKey string) int
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
	AddDestination(config output.DestinationConfig) (string, error)
	RemoveDestination(id string) error
	StartDestination(id string) error
	StopDestination(id string) error
	ListDestinations() []output.DestinationStatus
	GetDestination(id string) (output.DestinationStatus, error)
}

// DebugAPI is the interface for the debug snapshot endpoint.
type DebugAPI interface {
	HandleSnapshot(w http.ResponseWriter, r *http.Request)
}

// SCTE35API is the interface for SCTE-35 splice event operations.
type SCTE35API interface {
	InjectCue(msg *scte35.CueMessage) (uint32, error)
	ScheduleCue(msg *scte35.CueMessage, preRollMs int64) (uint32, error)
	ReturnToProgram(eventID uint32) error
	CancelEvent(eventID uint32) error
	CancelSegmentationEvent(segEventID uint32, source string) error
	HoldBreak(eventID uint32) error
	ExtendBreak(eventID uint32, newDurationMs int64) error
	ActiveEventIDs() []uint32
	State() scte35.InjectorState
	EventLog() []scte35.EventLogEntry
}

// SCTE35RulesAPI is the interface for SCTE-35 rule management operations.
type SCTE35RulesAPI interface {
	List() []scte35.Rule
	Create(rule scte35.Rule) (scte35.Rule, error)
	Update(id string, rule scte35.Rule) error
	Delete(id string) error
	Reorder(ids []string) error
	DefaultAction() scte35.RuleAction
	SetDefaultAction(action scte35.RuleAction) error
	Templates() []scte35.Rule
	CreateFromTemplate(templateName string) (scte35.Rule, error)
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

// WithMacroStore attaches a macro store to the API.
func WithMacroStore(s *macro.Store) APIOption {
	return func(a *API) { a.macroStore = s }
}

// WithKeyer attaches a key processor to the API.
func WithKeyer(kp *graphics.KeyProcessor) APIOption {
	return func(a *API) { a.keyer = kp }
}

// WithKeyBridge attaches a key processor bridge for fill cleanup on key removal.
func WithKeyBridge(kb *graphics.KeyProcessorBridge) APIOption {
	return func(a *API) { a.keyBridge = kb }
}

// WithReplayManager attaches a replay manager to the API.
func WithReplayManager(rm *replay.Manager) APIOption {
	return func(a *API) { a.replayMgr = rm }
}

// WithSCTE35 attaches an SCTE-35 injector and rules store to the API.
func WithSCTE35(s SCTE35API, r SCTE35RulesAPI) APIOption {
	return func(a *API) { a.scte35 = s; a.scte35Rules = r }
}

// WithOperatorStore attaches an operator store to the API.
func WithOperatorStore(s *operator.Store) APIOption {
	return func(a *API) { a.operatorStore = s }
}

// WithSessionManager attaches a session manager to the API.
func WithSessionManager(sm *operator.SessionManager) APIOption {
	return func(a *API) { a.sessionMgr = sm }
}

// WithLayoutCompositor attaches a layout compositor to the API.
func WithLayoutCompositor(lc *layout.Compositor) APIOption {
	return func(a *API) { a.layoutCompositor = lc }
}

// WithLayoutStore attaches a layout preset store to the API.
func WithLayoutStore(ls *layout.Store) APIOption {
	return func(a *API) { a.layoutStore = ls }
}

// API wraps a Switcher and exposes it over HTTP.
type API struct {
	switcher      *switcher.Switcher
	mixer         AudioMixerAPI
	outputMgr     OutputManagerAPI
	debug         DebugAPI
	presetStore   *preset.PresetStore
	compositor    *graphics.Compositor
	stingerStore  *stinger.StingerStore
	macroStore    *macro.Store
	keyer         *graphics.KeyProcessor
	keyBridge     *graphics.KeyProcessorBridge
	replayMgr     *replay.Manager
	operatorStore *operator.Store
	sessionMgr    *operator.SessionManager
	scte35           SCTE35API
	scte35Rules      SCTE35RulesAPI
	layoutCompositor *layout.Compositor
	layoutStore      *layout.Store
	mux           *http.ServeMux
	enrichFn      func(internal.ControlRoomState) internal.ControlRoomState
	lastOperator  atomic.Pointer[string]
	macroMu       sync.Mutex
	macroState    *internal.MacroExecutionState
	macroCancel   context.CancelFunc
	broadcastFn   func()
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

// SetEnrichFunc sets the function used to enrich switcher state with output,
// graphics, operator, and replay information before returning it to API clients.
func (a *API) SetEnrichFunc(fn func(internal.ControlRoomState) internal.ControlRoomState) {
	a.enrichFn = fn
}

// SetBroadcastFunc sets the function used to trigger a state broadcast.
func (a *API) SetBroadcastFunc(fn func()) {
	a.broadcastFn = fn
}

// MacroState returns the current macro execution state, if any.
func (a *API) MacroState() *internal.MacroExecutionState {
	a.macroMu.Lock()
	defer a.macroMu.Unlock()
	return a.macroState
}

// enrichedState returns the current switcher state, enriched with output,
// graphics, operator, and replay information if an enrich function is set.
func (a *API) enrichedState() internal.ControlRoomState {
	s := a.switcher.State()
	if a.enrichFn != nil {
		s = a.enrichFn(s)
	}
	return s
}

// setLastOperator extracts the bearer token from the request and stores
// the corresponding operator name as the last operator who made a change.
func (a *API) setLastOperator(r *http.Request) {
	if a.operatorStore == nil {
		return
	}
	token := operator.ExtractBearerToken(r)
	if op, err := a.operatorStore.GetByToken(token); err == nil {
		a.lastOperator.Store(&op.Name)
	}
}

// LastOperator returns the name of the last operator who made a state change,
// or nil if no operator has been recorded.
func (a *API) LastOperator() *string { return a.lastOperator.Load() }

// SetLastOperator sets the last operator name directly. Used by non-handler
// callbacks (output, compositor, replay, session) to clear the operator
// since those changes aren't triggered by a user action.
func (a *API) SetLastOperator(name *string) { a.lastOperator.Store(name) }

// Mux returns the internal ServeMux with all routes registered.
func (a *API) Mux() *http.ServeMux { return a.mux }

// RegisterOnMux registers the API routes on an external ServeMux. This is
// used to mount the control API onto the main Prism HTTP/3 mux via the
// ExtraRoutes hook. Routes are registered at both /api/ and /api/v1/
// prefixes for forward-compatible API versioning.
func (a *API) RegisterOnMux(mux *http.ServeMux) {
	a.registerAPIRoutes(mux)

	v1Mux := http.NewServeMux()
	a.registerAPIRoutes(v1Mux)
	mux.HandleFunc("/api/v1/", func(w http.ResponseWriter, r *http.Request) {
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/api/" + r.URL.Path[len("/api/v1/"):]
		if r.URL.RawPath != "" {
			r2.URL.RawPath = "/api/" + r.URL.RawPath[len("/api/v1/"):]
		}
		v1Mux.ServeHTTP(w, r2)
	})
}

// registerAPIRoutes registers all API route handlers on the given mux.
func (a *API) registerAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/switch/cut", a.handleCut)
	mux.HandleFunc("POST /api/switch/preview", a.handlePreview)
	mux.HandleFunc("POST /api/switch/transition", a.handleTransition)
	mux.HandleFunc("POST /api/switch/transition/position", a.handleTransitionPosition)
	mux.HandleFunc("POST /api/switch/ftb", a.handleFTB)
	mux.HandleFunc("GET /api/switch/state", a.handleState)
	mux.HandleFunc("GET /api/format", a.handleGetFormat)
	mux.HandleFunc("PUT /api/format", a.handleSetFormat)
	mux.HandleFunc("GET /api/sources", a.handleSources)
	mux.HandleFunc("POST /api/sources/{key}/label", a.handleSetLabel)
	mux.HandleFunc("POST /api/sources/{key}/delay", a.handleSetDelay)
	mux.HandleFunc("PUT /api/sources/{key}/position", a.handleSetPosition)
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
	mux.HandleFunc("POST /api/recording/start", a.handleRecordingStart)
	mux.HandleFunc("POST /api/recording/stop", a.handleRecordingStop)
	mux.HandleFunc("GET /api/recording/status", a.handleRecordingStatus)
	mux.HandleFunc("POST /api/output/srt/start", a.handleSRTStart)
	mux.HandleFunc("POST /api/output/srt/stop", a.handleSRTStop)
	mux.HandleFunc("GET /api/output/srt/status", a.handleSRTStatus)
	mux.HandleFunc("GET /api/output/confidence", a.handleConfidence)
	mux.HandleFunc("POST /api/output/destinations", a.handleAddDestination)
	mux.HandleFunc("GET /api/output/destinations", a.handleListDestinations)
	mux.HandleFunc("GET /api/output/destinations/{id}", a.handleGetDestination)
	mux.HandleFunc("DELETE /api/output/destinations/{id}", a.handleRemoveDestination)
	mux.HandleFunc("POST /api/output/destinations/{id}/start", a.handleStartDestination)
	mux.HandleFunc("POST /api/output/destinations/{id}/stop", a.handleStopDestination)
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
		mux.HandleFunc("POST /api/stinger/{name}/upload", a.handleStingerUpload)
	}
	if a.compositor != nil {
		mux.HandleFunc("POST /api/graphics", a.handleGraphicsAddLayer)
		mux.HandleFunc("GET /api/graphics", a.handleGraphicsStatus)
		mux.HandleFunc("DELETE /api/graphics/{id}", a.handleGraphicsRemoveLayer)
		mux.HandleFunc("POST /api/graphics/{id}/frame", a.handleGraphicsFrame)
		mux.HandleFunc("POST /api/graphics/{id}/on", a.handleGraphicsOn)
		mux.HandleFunc("POST /api/graphics/{id}/off", a.handleGraphicsOff)
		mux.HandleFunc("POST /api/graphics/{id}/auto-on", a.handleGraphicsAutoOn)
		mux.HandleFunc("POST /api/graphics/{id}/auto-off", a.handleGraphicsAutoOff)
		mux.HandleFunc("POST /api/graphics/{id}/animate", a.handleGraphicsAnimate)
		mux.HandleFunc("POST /api/graphics/{id}/animate/stop", a.handleGraphicsAnimateStop)
		mux.HandleFunc("PUT /api/graphics/{id}/rect", a.handleGraphicsLayerRect)
		mux.HandleFunc("PUT /api/graphics/{id}/zorder", a.handleGraphicsLayerZOrder)
		mux.HandleFunc("POST /api/graphics/{id}/fly-in", a.handleGraphicsFlyIn)
		mux.HandleFunc("POST /api/graphics/{id}/fly-out", a.handleGraphicsFlyOut)
		mux.HandleFunc("POST /api/graphics/{id}/slide", a.handleGraphicsSlide)
	}
	if a.macroStore != nil {
		mux.HandleFunc("DELETE /api/macros/execution", a.handleDismissMacro)
		mux.HandleFunc("POST /api/macros/execution/cancel", a.handleCancelMacro)
		mux.HandleFunc("GET /api/macros", a.handleListMacros)
		mux.HandleFunc("GET /api/macros/{name}", a.handleGetMacro)
		mux.HandleFunc("PUT /api/macros/{name}", a.handleSaveMacro)
		mux.HandleFunc("DELETE /api/macros/{name}", a.handleDeleteMacro)
		mux.HandleFunc("POST /api/macros/{name}/run", a.handleRunMacro)
	}
	if a.keyer != nil {
		mux.HandleFunc("PUT /api/sources/{source}/key", a.handleSetSourceKey)
		mux.HandleFunc("GET /api/sources/{source}/key", a.handleGetSourceKey)
		mux.HandleFunc("DELETE /api/sources/{source}/key", a.handleDeleteSourceKey)
	}
	if a.operatorStore != nil && a.sessionMgr != nil {
		a.registerOperatorRoutes(mux)
	}
	if a.replayMgr != nil {
		mux.HandleFunc("POST /api/replay/mark-in", a.handleReplayMarkIn)
		mux.HandleFunc("POST /api/replay/mark-out", a.handleReplayMarkOut)
		mux.HandleFunc("POST /api/replay/play", a.handleReplayPlay)
		mux.HandleFunc("POST /api/replay/stop", a.handleReplayStop)
		mux.HandleFunc("GET /api/replay/status", a.handleReplayStatus)
		mux.HandleFunc("GET /api/replay/sources", a.handleReplaySources)
	}
	if a.scte35 != nil {
		mux.HandleFunc("POST /api/scte35/cue", a.handleSCTE35Cue)
		mux.HandleFunc("POST /api/scte35/return", a.handleSCTE35Return)
		mux.HandleFunc("POST /api/scte35/return/{eventId}", a.handleSCTE35ReturnEvent)
		mux.HandleFunc("POST /api/scte35/cancel/{eventId}", a.handleSCTE35Cancel)
		mux.HandleFunc("POST /api/scte35/cancel-segmentation/{segEventId}", a.handleSCTE35CancelSegmentation)
		mux.HandleFunc("POST /api/scte35/hold/{eventId}", a.handleSCTE35Hold)
		mux.HandleFunc("POST /api/scte35/extend/{eventId}", a.handleSCTE35Extend)
		mux.HandleFunc("GET /api/scte35/status", a.handleSCTE35Status)
		mux.HandleFunc("GET /api/scte35/log", a.handleSCTE35Log)
		mux.HandleFunc("GET /api/scte35/active", a.handleSCTE35Active)
	}
	if a.scte35Rules != nil {
		// Register specific named routes before wildcard {id} routes to ensure
		// Go's ServeMux picks them correctly.
		mux.HandleFunc("PUT /api/scte35/rules/default", a.handleSCTE35SetDefault)
		mux.HandleFunc("POST /api/scte35/rules/reorder", a.handleSCTE35ReorderRules)
		mux.HandleFunc("GET /api/scte35/rules/templates", a.handleSCTE35Templates)
		mux.HandleFunc("POST /api/scte35/rules/from-template", a.handleSCTE35FromTemplate)
		mux.HandleFunc("GET /api/scte35/rules", a.handleSCTE35ListRules)
		mux.HandleFunc("POST /api/scte35/rules", a.handleSCTE35CreateRule)
		mux.HandleFunc("PUT /api/scte35/rules/{id}", a.handleSCTE35UpdateRule)
		mux.HandleFunc("DELETE /api/scte35/rules/{id}", a.handleSCTE35DeleteRule)
	}
	if a.layoutCompositor != nil {
		mux.HandleFunc("GET /api/layout", a.handleGetLayout)
		mux.HandleFunc("PUT /api/layout", a.handleSetLayout)
		mux.HandleFunc("DELETE /api/layout", a.handleDeleteLayout)
		mux.HandleFunc("PUT /api/layout/slots/{id}", a.handleSlotUpdate)
		mux.HandleFunc("POST /api/layout/slots/{id}/on", a.handleSlotOn)
		mux.HandleFunc("POST /api/layout/slots/{id}/off", a.handleSlotOff)
		mux.HandleFunc("PUT /api/layout/slots/{id}/source", a.handleSlotSource)
		mux.HandleFunc("GET /api/layout/presets", a.handleListLayoutPresets)
		mux.HandleFunc("POST /api/layout/presets", a.handleSaveLayoutPreset)
		mux.HandleFunc("DELETE /api/layout/presets/{name}", a.handleDeleteLayoutPreset)
	}
}

func (a *API) registerRoutes() { a.RegisterOnMux(a.mux) }

// handleCut performs a hard cut to the specified source.
func (a *API) handleCut(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	var req switchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Source == "" {
		httperr.Write(w, http.StatusBadRequest, "source required")
		return
	}
	if err := a.switcher.Cut(r.Context(), req.Source); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handlePreview sets the preview source without affecting the program output.
func (a *API) handlePreview(w http.ResponseWriter, r *http.Request) {
	a.setLastOperator(r)
	var req switchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Source == "" {
		httperr.Write(w, http.StatusBadRequest, "source required")
		return
	}
	if err := a.switcher.SetPreview(r.Context(), req.Source); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}

// handleState returns the current ControlRoomState as JSON.
func (a *API) handleState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.enrichedState())
}
