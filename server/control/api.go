package control

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/caption"
	"github.com/zsiec/switchframe/server/clip"
	"github.com/zsiec/switchframe/server/cmdqueue"
	"github.com/zsiec/switchframe/server/colorgrade"
	"github.com/zsiec/switchframe/server/metrics"
	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/dve"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/htmlgfx"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/macro"
	"github.com/zsiec/switchframe/server/operator"
	"github.com/zsiec/switchframe/server/output"
	"github.com/zsiec/switchframe/server/playout"
	"github.com/zsiec/switchframe/server/preset"
	"github.com/zsiec/switchframe/server/replay"
	"github.com/zsiec/switchframe/server/scte35"
	"github.com/zsiec/switchframe/server/stinger"
	"github.com/zsiec/switchframe/server/stmap"
	"github.com/zsiec/switchframe/server/switcher"
	"github.com/zsiec/switchframe/server/transition/wipemap"
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
	SetPhaseInvert(sourceKey string, invert bool) error
	SetBalance(sourceKey string, balance float64) error
	SetMasterLevel(level float64) error
	SetEQ(sourceKey string, band int, frequency, gain, q float64, enabled bool) error
	GetEQ(sourceKey string) ([3]audio.EQBandSettings, error)
	SetCompressor(sourceKey string, threshold, ratio, attack, release, makeupGain float64) error
	GetCompressor(sourceKey string) (audio.CompressorState, error)
	SetHPF(sourceKey string, frequency float64, enabled bool) error
	GetHPF(sourceKey string) (frequency float64, enabled bool)
	SetGate(sourceKey string, threshold, rangeDB, attack, release, hold float64, enabled bool) error
	GetGate(sourceKey string) (audio.GateState, error)
	SetAudioDelay(sourceKey string, delayMs int) error
	AudioDelayMs(sourceKey string) int
	SetLimiterThreshold(thresholdDB float64) error
	SetLimiterCeiling(ceilingDB float64) error
	LimiterSettings() (thresholdDB, ceilingDB, gainReduction float64)
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
	ReassignDestinationEngine(id, engine string) error
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
	UpdateByName(name string, rule scte35.Rule) error
	DeleteByName(name string) error
	Reorder(ids []string) error
	DefaultAction() scte35.RuleAction
	SetDefaultAction(action scte35.RuleAction) error
	Templates() []scte35.Rule
	CreateFromTemplate(templateName string) (scte35.Rule, error)
}

// HTMLGfxManager is the interface for HTML5 graphics browser pool operations.
type HTMLGfxManager interface {
	Started() bool
	AllocateBrowser(layerID int) (int, error)
	FreeBrowser(browserID int)
	BrowserForLayer(layerID int) int
	LoadURL(browserID int, url string) error
	Play(browserID int) error
	Stop(browserID int) error
	Next(browserID int) error
	Update(browserID int, data string) error
	CustomAction(browserID int, action, payload string) error
	DiscoverTemplates() []htmlgfx.TemplateInfo
}

// ColorGradeManager is the interface for color grading operations.
type ColorGradeManager interface {
	IsEnabled() bool
	SetColorGrade(lookName string, intensity float32) error
	DisableColorGrade()
	GetState() internal.ColorGradeState
	UploadLUT(name string, data io.Reader) error
	DeleteLUT(name string) error
	SetCorrector(state colorgrade.CorrectorState)
	GetCorrector() colorgrade.CorrectorState
	ResetCorrector()
	SaveCustomLook(name string) error
	LoadLookSettings(name string) *colorgrade.CorrectorState
}

// SourceCorrectorManager is the interface for per-source color corrector operations.
type SourceCorrectorManager interface {
	Get(key string) colorgrade.CorrectorState
	Set(key string, state colorgrade.CorrectorState)
	Reset(key string)
	HasCorrection(key string) bool
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

// WithPlayoutManager attaches a playout manager to the API.
func WithPlayoutManager(pm *playout.Manager) APIOption {
	return func(a *API) { a.playoutMgr = pm }
}

// WithPlayoutCache attaches a playout asset cache to the API.
func WithPlayoutCache(c *playout.Cache) APIOption {
	return func(a *API) { a.playoutCache = c }
}

// WithPlayoutPrefetcher attaches a playout prefetcher to the API so
// playlist loads can trigger asset pre-fetching.
func WithPlayoutPrefetcher(pf *playout.Prefetcher) APIOption {
	return func(a *API) { a.playoutPrefetcher = pf }
}

// WithPlayoutSamples attaches pre-generated sample clips to the API
// for the sample/template endpoints.
func WithPlayoutSamples(samples []playout.SampleClip) APIOption {
	return func(a *API) { a.playoutSamples = samples }
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

// WithDVECompositor attaches a DVE compositor to the API.
func WithDVECompositor(c *dve.Compositor) APIOption {
	return func(a *API) { a.dveCompositor = c }
}

// WithDVEPresetStore attaches a DVE preset store to the API.
func WithDVEPresetStore(s *dve.PresetStore) APIOption {
	return func(a *API) { a.dvePresetStore = s }
}

// WithWipeMapStore attaches a wipe pattern store to the API.
func WithWipeMapStore(s *wipemap.WipeMapStore) APIOption {
	return func(a *API) { a.wipeStore = s }
}

// SRTManager is the interface for SRT source management operations.
// The app layer implements this, wrapping srt.Caller and srt.StatsManager.
// This keeps the control package free of srt package imports.
type SRTManager interface {
	// CreatePull starts an outbound SRT pull connection. Returns the source key.
	CreatePull(ctx context.Context, address, streamID, label string, latencyMs int, inputBW int64, overheadBW int) (string, error)
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

// AISegmentManager is the interface for AI background segmentation operations.
// This is implemented by an adapter in the app layer that bridges the GPU
// SegmentationEngine with the per-source config store. All methods are
// safe to call when the underlying engine is nil (non-GPU builds).
type AISegmentManager interface {
	// EnableAISegment configures and starts AI segmentation for a source.
	// sensitivity: 0.0-1.0, edgeSmooth: 0.0-1.0, background: "" | "transparent" | "blur:N" | "color:RRGGBB"
	EnableAISegment(source string, sensitivity, edgeSmooth float32, background string) error
	// DisableAISegment stops AI segmentation for a source and frees GPU resources.
	DisableAISegment(source string)
	// GetAISegmentConfig returns the current AI segmentation config for a source.
	// Returns (zero, false) if no config is set for this source.
	GetAISegmentConfig(source string) (internal.AISegmentConfig, bool)
	// IsAISegmentAvailable returns true when a GPU segmentation engine is present
	// and the feature can be used.
	IsAISegmentAvailable() bool
}

// ASRManager is the interface for ASR operations.
type ASRManager interface {
	IsASRAvailable() bool
	IsASRActive() bool
	SetASRActive(active bool) error

	// Backend selection
	ActiveBackend() string
	AvailableBackends() []string
	SwitchBackend(name string) error

	// Per-backend model management
	ASRModelName() string
	SetASRModel(name string) error
	AvailableASRModels() []string

	// Backend-specific config (JSON pass-through)
	BackendConfig() json.RawMessage
	SetBackendConfig(raw json.RawMessage) error

	// Pacer settings (backend-agnostic)
	SetASRConfidence(preset string)
	ASRConfidence() string
	SetASRSpeakerDetection(preset string)
	ASRSpeakerDetection() string
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

// WithSequenceEngine attaches a sequence engine to the API.
func WithSequenceEngine(se *graphics.SequenceEngine) APIOption {
	return func(a *API) { a.sequenceEngine = se }
}

// WithSRTManager attaches an SRT source manager to the API.
func WithSRTManager(m SRTManager) APIOption {
	return func(a *API) { a.srtMgr = m }
}

// WithCommsManager attaches a comms manager to the API.
func WithCommsManager(cm CommsManagerAPI) APIOption {
	return func(a *API) { a.commsMgr = cm }
}

// WithAISegmentManager attaches an AI segmentation manager to the API.
func WithAISegmentManager(m AISegmentManager) APIOption {
	return func(a *API) { a.aiSegmentMgr = m }
}

// WithASRManager attaches an ASR manager to the API.
func WithASRManager(m ASRManager) APIOption {
	return func(a *API) { a.asrManager = m }
}

// WithCommandQueue attaches a command queue for timed execution of cut/preview commands.
func WithCommandQueue(q *cmdqueue.Queue) APIOption {
	return func(a *API) { a.cmdQueue = q }
}

// WithSeqTracker sets a callback invoked after successful timed command enqueue
// to track the highest enqueued command sequence number.
func WithSeqTracker(fn func(uint64)) APIOption {
	return func(a *API) { a.seqTracker = fn }
}

// WithLastPongSentFn provides the sync-health endpoint with a transport-level
// liveness indicator: the Unix-microsecond timestamp of the last pong response
// sent by the FastControl dispatcher.
func WithLastPongSentFn(fn func() int64) APIOption {
	return func(a *API) { a.lastPongSentFn = fn }
}

// WithSTMapRegistry attaches an ST map registry to the API.
func WithSTMapRegistry(r *stmap.Registry) APIOption {
	return func(a *API) { a.stmapRegistry = r }
}

// WithSTMapStore attaches an ST map file store for persistence.
func WithSTMapStore(s *stmap.Store) APIOption {
	return func(a *API) { a.stmapStore = s }
}

// WithPlayoutSaveDir sets the directory for saving/loading playlists.
func WithPlayoutSaveDir(dir string) APIOption {
	return func(a *API) { a.playoutSaveDir = dir }
}

// WithSTMapCacheDir sets the base cache directory for mmap-backed ST map LUTs.
// When set, animated ST maps use BuildProcessorsCached instead of BuildProcessors.
func WithSTMapCacheDir(dir string) APIOption {
	return func(a *API) { a.stmapCacheDir = dir }
}

// WithColorGradeManager attaches a color grade manager to the API.
func WithColorGradeManager(m ColorGradeManager) APIOption {
	return func(a *API) { a.colorGradeMgr = m }
}

// WithSourceCorrectorManager attaches a per-source color corrector manager to the API.
func WithSourceCorrectorManager(m SourceCorrectorManager) APIOption {
	return func(a *API) { a.sourceCorrectorMgr = m }
}

// SetHTMLGfxManager attaches an HTML5 graphics manager to the API.
// This is a setter rather than an option because the manager may be
// initialized after the API is constructed.
func (a *API) SetHTMLGfxManager(mgr HTMLGfxManager) { a.htmlGfxMgr = mgr }

// WithInviteTokens sets the invite token map (token -> role) for operator registration gating.
func WithInviteTokens(tokens map[string]string) APIOption {
	return func(a *API) { a.inviteTokens = tokens }
}

// WithSessionAPIToken stores the session API token so the register handler
// can bypass invite token requirements for session owners.
func WithSessionAPIToken(token string) APIOption {
	return func(a *API) { a.sessionAPIToken = token }
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

// WithAppMetrics attaches Prometheus metrics for snapshot instrumentation.
func WithAppMetrics(m *metrics.Metrics) APIOption {
	return func(a *API) { a.appMetrics = m }
}

// SetAppMetrics sets the Prometheus metrics after API construction.
func (a *API) SetAppMetrics(m *metrics.Metrics) {
	a.appMetrics = m
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
	playoutMgr         *playout.Manager
	playoutCache       *playout.Cache
	playoutPrefetcher  *playout.Prefetcher
	playoutSamples     []playout.SampleClip
	recordingDir       atomic.Pointer[string]
	operatorStore      *operator.Store
	sessionMgr         *operator.SessionManager
	scte35             SCTE35API
	scte35Rules        SCTE35RulesAPI
	dveCompositor      *dve.Compositor
	dvePresetStore     *dve.PresetStore
	wipeStore          *wipemap.WipeMapStore
	captionMgr         CaptionManagerAPI
	perf               PerfAPI
	textAnimEngine     *graphics.TextAnimationEngine
	tickerEngine       *graphics.TickerEngine
	sequenceEngine     *graphics.SequenceEngine
	srtMgr             SRTManager
	commsMgr           CommsManagerAPI
	aiSegmentMgr       AISegmentManager
	asrManager         ASRManager
	stmapRegistry      *stmap.Registry
	stmapStore         *stmap.Store
	playoutSaveDir     string // directory for saved playlists
	stmapCacheDir      string // base cache dir for mmap'd ST map LUTs (empty = disabled)
	colorGradeMgr      ColorGradeManager
	sourceCorrectorMgr SourceCorrectorManager
	htmlGfxMgr         HTMLGfxManager
	inviteTokens       map[string]string // token -> role; nil = no invite gating
	sessionAPIToken    string            // used to bypass invite token requirement for session owner
	allowedOutputPorts map[int]bool      // nil = unconstrained
	snapshotProvider   SnapshotProvider
	appMetrics         *metrics.Metrics
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
	asrConfigMu        sync.Mutex // serializes ASR config changes (prevents pile-up)
	cmdQueue           *cmdqueue.Queue
	seqTracker         func(uint64)
	lastPongSentFn     func() int64 // returns Unix µs of last fast-control pong
	startTime          time.Time
	reconcileLock      reconcileLockState
}

// enrichFunc is the type for the state enrichment callback.
type enrichFunc func(internal.ControlRoomState) internal.ControlRoomState

// broadcastFunc is the type for the state broadcast callback.
type broadcastFunc func()

// NewAPI creates an API that delegates to sw.
func NewAPI(sw *switcher.Switcher, opts ...APIOption) *API {
	a := &API{switcher: sw, mux: http.NewServeMux(), startTime: time.Now(), reconcileLock: newReconcileLockState()}
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
	mux.HandleFunc("POST /api/switch/cut", timedHandler(a.cmdQueue, "cut", a.seqTracker, a.handleCutInner))
	mux.HandleFunc("POST /api/switch/preview", timedHandler(a.cmdQueue, "preview", a.seqTracker, a.handlePreviewInner))
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
	a.registerHTML5GraphicsRoutes(mux)
	a.registerMacroRoutes(mux)
	a.registerKeyRoutes(mux)
	a.registerOperatorAPIRoutes(mux)
	a.registerReplayRoutes(mux)
	a.registerSCTE35Routes(mux)
	a.registerCaptionRoutes(mux)
	a.registerDVERoutes(mux)
	a.registerWipeRoutes(mux)
	a.registerClipRoutes(mux)
	a.registerPlayoutRoutes(mux)
	a.registerPlayoutPodRoutes(mux)
	a.registerCommsRoutes(mux)
	a.registerSTMapRoutes(mux)
	a.registerColorGradeRoutes(mux)
	a.registerAISegmentRoutes(mux)
	a.registerASRRoutes(mux)
	a.registerPeerRoutes(mux)
	mux.HandleFunc("GET /api/sync/health", a.handleSyncHealth)
	a.registerSyncRoutes(mux)
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

// handleCutInner performs a hard cut to the specified source.
// body is the pre-read request body provided by timedHandler.
func (a *API) handleCutInner(w http.ResponseWriter, r *http.Request, body []byte) {
	a.setLastOperator(r)
	var req switchRequest
	if err := json.Unmarshal(body, &req); err != nil {
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

// handlePreviewInner sets the preview source without affecting the program output.
// body is the pre-read request body provided by timedHandler.
func (a *API) handlePreviewInner(w http.ResponseWriter, r *http.Request, body []byte) {
	a.setLastOperator(r)
	var req switchRequest
	if err := json.Unmarshal(body, &req); err != nil {
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

// handleSyncHealth returns lightweight health information for redundancy monitoring.
// Polled at 1Hz by the browser in dual-engine mode for clock sync calibration
// and engine health detection.
func (a *API) handleSyncHealth(w http.ResponseWriter, r *http.Request) {
	state := a.enrichedState()
	resp := map[string]any{
		"clockSync":       clock.GetSyncState(),
		"lastCommandSeq":  state.LastCommandSeq,
		"lastExecutedSeq": state.LastExecutedSeq,
		"lastExecutedErr": state.LastExecutedErr,
		"lateCommands":    state.LateCommands,
		"uptimeMs":        time.Since(a.startTime).Milliseconds(),
	}
	if a.lastPongSentFn != nil {
		resp["lastPongSentUs"] = a.lastPongSentFn()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
