package control

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/caption"
	"github.com/zsiec/switchframe/server/clip"
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
	SetMasterLevel(level float64) error
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
	StartSRTOutput(config output.SRTConfig) error
	StopSRTOutput() error
	SRTOutputStatus() output.SRTStatus
	ConfidenceThumbnail() []byte
	AddDestination(config output.DestinationConfig) (string, error)
	RemoveDestination(id string) error
	StartDestination(id string) error
	StopDestination(id string) error
	ListDestinations() []output.DestinationStatus
	GetDestination(id string) (output.DestinationStatus, error)
	CBRStatus() *output.CBRPacerStatus
}

// DebugAPI is the interface for the debug snapshot endpoint.
type DebugAPI interface {
	HandleSnapshot(w http.ResponseWriter, r *http.Request)
}

// PerfAPI is the interface for performance monitoring endpoints.
type PerfAPI interface {
	HandlePerf(w http.ResponseWriter, r *http.Request)
	HandleSaveBaseline(w http.ResponseWriter, r *http.Request)
	HandleDeleteBaseline(w http.ResponseWriter, r *http.Request)
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
func WithPresetStore(ps *preset.Store) APIOption {
	return func(a *API) { a.presetStore = ps }
}

// WithCompositor attaches a graphics compositor to the API.
func WithCompositor(c *graphics.Compositor) APIOption {
	return func(a *API) { a.compositor = c }
}

// WithStingerStore attaches a stinger clip store to the API.
func WithStingerStore(s *stinger.Store) APIOption {
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

// WithClipManager attaches a clip manager to the API.
func WithClipManager(cm *clip.Manager) APIOption {
	return func(a *API) { a.clipMgr = cm }
}

// WithClipStore attaches a clip store to the API.
func WithClipStore(cs *clip.Store) APIOption {
	return func(a *API) { a.clipStore = cs }
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

// SRTManager is the interface for SRT source management operations.
// The app layer implements this, wrapping srt.Caller and srt.StatsManager.
// This keeps the control package free of srt package imports.
type SRTManager interface {
	// CreatePull starts an outbound SRT pull connection. Returns the source key.
	CreatePull(ctx context.Context, address, streamID, label string, latencyMs int) (string, error)
	// StopPull cancels an active pull and removes it from the store.
	// Returns ErrNotSRTSource if the key is not an SRT pull source.
	StopPull(key string) error
	// GetStats returns SRT connection stats for the given source key.
	// The second return value is false if the source is not found.
	GetStats(key string) (interface{}, bool)
	// UpdateLatency changes the SRT latency for an active source.
	// Returns ErrNotSRTSource if the key is not an SRT source.
	UpdateLatency(key string, latencyMs int) error
}

// CaptionManagerAPI is the interface for caption management operations.
type CaptionManagerAPI interface {
	SetMode(mode caption.Mode)
	Mode() caption.Mode
	IngestText(text string)
	IngestNewline()
	Clear()
	State() caption.State
}

// CommsManagerAPI is the interface for operator voice comms operations.
type CommsManagerAPI interface {
	Join(operatorID, name string) error
	Leave(operatorID string)
	SetMuted(operatorID string, muted bool) error
	State() *internal.CommsState
}

// WithCaptionManager attaches a caption manager to the API.
func WithCaptionManager(cm CaptionManagerAPI) APIOption {
	return func(a *API) { a.captionMgr = cm }
}

// WithPerfSampler attaches a performance sampler to the API.
func WithPerfSampler(p PerfAPI) APIOption {
	return func(a *API) { a.perf = p }
}

// WithTextAnimEngine attaches a text animation engine to the API.
func WithTextAnimEngine(tae *graphics.TextAnimationEngine) APIOption {
	return func(a *API) { a.textAnimEngine = tae }
}

// WithTickerEngine attaches a ticker engine to the API.
func WithTickerEngine(te *graphics.TickerEngine) APIOption {
	return func(a *API) { a.tickerEngine = te }
}

// WithSRTManager attaches an SRT source manager to the API.
func WithSRTManager(m SRTManager) APIOption {
	return func(a *API) { a.srtMgr = m }
}

// WithCommsManager attaches a comms manager to the API.
func WithCommsManager(cm CommsManagerAPI) APIOption {
	return func(a *API) { a.commsMgr = cm }
}

// WithAllowedOutputPorts constrains SRT listener output to the given ports.
func WithAllowedOutputPorts(ports []int) APIOption {
	return func(a *API) {
		if len(ports) > 0 {
			a.allowedOutputPorts = make(map[int]bool, len(ports))
			for _, p := range ports {
				a.allowedOutputPorts[p] = true
			}
		}
	}
}

// WithRecordingDir sets the directory where recordings are stored.
// Used by handleClipRecordings to list available recordings for import.
func WithRecordingDir(dir string) APIOption {
	return func(a *API) { a.recordingDir.Store(&dir) }
}

// API wraps a Switcher and exposes it over HTTP.
type API struct {
	switcher           *switcher.Switcher
	mixer              AudioMixerAPI
	outputMgr          OutputManagerAPI
	debug              DebugAPI
	presetStore        *preset.Store
	compositor         *graphics.Compositor
	stingerStore       *stinger.Store
	macroStore         *macro.Store
	keyer              *graphics.KeyProcessor
	keyBridge          *graphics.KeyProcessorBridge
	replayMgr          *replay.Manager
	clipMgr            *clip.Manager
	clipStore          *clip.Store
	recordingDir       atomic.Pointer[string]
	operatorStore      *operator.Store
	sessionMgr         *operator.SessionManager
	scte35             SCTE35API
	scte35Rules        SCTE35RulesAPI
	layoutCompositor   *layout.Compositor
	layoutStore        *layout.Store
	captionMgr         CaptionManagerAPI
	perf               PerfAPI
	textAnimEngine     *graphics.TextAnimationEngine
	tickerEngine       *graphics.TickerEngine
	srtMgr             SRTManager
	commsMgr           CommsManagerAPI
	allowedOutputPorts map[int]bool // nil = unconstrained
	mux                *http.ServeMux
	enrichFn           atomic.Pointer[enrichFunc]
	lastOperator       atomic.Pointer[string]
	macroMu            sync.Mutex
	macroState         *internal.MacroExecutionState
	macroCancel        context.CancelFunc
	macroGen           uint64 // incremented each time a macro starts
	broadcastFn        atomic.Pointer[broadcastFunc]
	uploadMu           sync.Mutex
	uploadProgress     *internal.ClipUploadProgress
	uploadStartTime    time.Time
}

// enrichFunc is the type for the state enrichment callback.
type enrichFunc func(internal.ControlRoomState) internal.ControlRoomState

// broadcastFunc is the type for the state broadcast callback.
type broadcastFunc func()

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
	ef := enrichFunc(fn)
	a.enrichFn.Store(&ef)
}

// SetBroadcastFunc sets the function used to trigger a state broadcast.
func (a *API) SetBroadcastFunc(fn func()) {
	bf := broadcastFunc(fn)
	a.broadcastFn.Store(&bf)
}

// broadcast calls the broadcast function if one has been set.
func (a *API) broadcast() {
	if fn := a.broadcastFn.Load(); fn != nil {
		(*fn)()
	}
}

// MacroState returns a deep copy of the current macro execution state, if any.
// A copy is returned to prevent data races: the onProgress callback writes
// fields under macroMu, and callers (e.g. enrichState -> json.Marshal) read
// fields without the lock.
func (a *API) MacroState() *internal.MacroExecutionState {
	a.macroMu.Lock()
	defer a.macroMu.Unlock()
	if a.macroState == nil {
		return nil
	}
	cp := *a.macroState
	if a.macroState.Steps != nil {
		cp.Steps = make([]internal.MacroStepState, len(a.macroState.Steps))
		copy(cp.Steps, a.macroState.Steps)
	}
	return &cp
}

// UploadProgress returns the current clip upload progress, or nil if no upload
// is in progress. Returns a copy to avoid races with the upload goroutine.
func (a *API) UploadProgress() *internal.ClipUploadProgress {
	a.uploadMu.Lock()
	defer a.uploadMu.Unlock()
	if a.uploadProgress == nil {
		return nil
	}
	cp := *a.uploadProgress
	return &cp
}

// enrichedState returns the current switcher state, enriched with output,
// graphics, operator, and replay information if an enrich function is set.
func (a *API) enrichedState() internal.ControlRoomState {
	s := a.switcher.State()
	if fn := a.enrichFn.Load(); fn != nil {
		s = (*fn)(s)
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
	// Core routes (state, cut, preview) stay here
	mux.HandleFunc("POST /api/switch/cut", a.handleCut)
	mux.HandleFunc("POST /api/switch/preview", a.handlePreview)
	mux.HandleFunc("GET /api/switch/state", a.handleState)

	// Delegate to per-file route registration methods
	a.registerTransitionRoutes(mux)
	a.registerFormatRoutes(mux)
	a.registerEncoderRoutes(mux)
	a.registerSourceRoutes(mux)
	a.registerAudioRoutes(mux)
	a.registerOutputRoutes(mux)
	a.registerDebugRoutes(mux)
	a.registerPresetRoutes(mux)
	a.registerGraphicsRoutes(mux)
	a.registerMacroRoutes(mux)
	a.registerKeyRoutes(mux)
	a.registerOperatorAPIRoutes(mux)
	a.registerReplayRoutes(mux)
	a.registerSCTE35Routes(mux)
	a.registerCaptionRoutes(mux)
	a.registerLayoutRoutes(mux)
	a.registerClipRoutes(mux)
	a.registerCommsRoutes(mux)
}

func (a *API) registerRoutes() { a.RegisterOnMux(a.mux) }

// registerDebugRoutes registers debug and performance monitoring routes on the given mux.
func (a *API) registerDebugRoutes(mux *http.ServeMux) {
	if a.debug != nil {
		mux.HandleFunc("GET /api/debug/snapshot", a.debug.HandleSnapshot)
	}
	if a.perf != nil {
		mux.HandleFunc("GET /api/perf", a.perf.HandlePerf)
		mux.HandleFunc("POST /api/perf/baseline", a.perf.HandleSaveBaseline)
		mux.HandleFunc("DELETE /api/perf/baseline/{name}", a.perf.HandleDeleteBaseline)
	}
}

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
