package control

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/zsiec/switchframe/server/caption"
	"github.com/zsiec/switchframe/server/clip"
	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/dve"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/macro"
	"github.com/zsiec/switchframe/server/output"
	"github.com/zsiec/switchframe/server/preset"
	"github.com/zsiec/switchframe/server/replay"
	"github.com/zsiec/switchframe/server/scte35"
	"github.com/zsiec/switchframe/server/stinger"
	"github.com/zsiec/switchframe/server/stmap"
	"github.com/zsiec/switchframe/server/switcher"
)

// registerMacroRoutes registers macro-related API routes on the given mux.
func (a *API) registerMacroRoutes(mux *http.ServeMux) {
	if a.macroStore == nil {
		return
	}
	mux.HandleFunc("DELETE /api/macros/execution", a.handleDismissMacro)
	mux.HandleFunc("POST /api/macros/execution/cancel", a.handleCancelMacro)
	mux.HandleFunc("GET /api/macros", a.handleListMacros)
	mux.HandleFunc("GET /api/macros/{name}", a.handleGetMacro)
	mux.HandleFunc("PUT /api/macros/{name}", a.handleSaveMacro)
	mux.HandleFunc("DELETE /api/macros/{name}", a.handleDeleteMacro)
	mux.HandleFunc("POST /api/macros/{name}/run", a.handleRunMacro)
}

// handleListMacros returns all macros.
func (a *API) handleListMacros(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a.macroStore.List())
}

// handleGetMacro returns a single macro by name.
func (a *API) handleGetMacro(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	m, err := a.macroStore.Get(name)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(m)
}

// handleSaveMacro creates or updates a macro.
func (a *API) handleSaveMacro(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var m macro.Macro
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	m.Name = name
	if err := validateStringLen("name", name, MaxNameLen); err != nil {
		httperr.Write(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := a.macroStore.Save(m); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(m)
}

// handleDeleteMacro deletes a macro by name.
func (a *API) handleDeleteMacro(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := a.macroStore.Delete(name); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRunMacro triggers execution of a macro in a background goroutine
// and returns 202 Accepted immediately.
func (a *API) handleRunMacro(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	m, err := a.macroStore.Get(name)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}
	// runId names this run on every engine it is sent to, so that what a
	// step creates there, such as a replay clip, takes the same ID on each.
	var req struct {
		RunID string `json:"runId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		httperr.Write(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.RunID != "" && !clip.ValidID(req.RunID) {
		httperr.Write(w, http.StatusBadRequest, "runId must be 32 lowercase hex characters")
		return
	}

	// Concurrency guard: only one macro at a time.
	a.macroMu.Lock()
	if a.macroState != nil && a.macroState.Running {
		a.macroMu.Unlock()
		httperr.Write(w, http.StatusConflict, "macro already running")
		return
	}
	// Use context.Background() so the macro is not cancelled when the HTTP
	// response is sent and r.Context() is closed.
	ctx, cancel := context.WithCancel(context.Background())
	a.macroCancel = cancel
	a.macroGen++
	gen := a.macroGen
	a.macroState = &internal.MacroExecutionState{Running: true, MacroName: m.Name}
	a.macroMu.Unlock()

	target := &apiMacroTarget{
		switcher:       a.switcher,
		mixer:          a.mixer,
		compositor:     a.compositor,
		keyer:          a.keyer,
		replayMgr:      a.replayMgr,
		presetStore:    a.presetStore,
		outputMgr:      a.outputMgr,
		stingerStore:   a.stingerStore,
		scte35:         a.scte35,
		captionMgr:     a.captionMgr,
		clipMgr:        a.clipMgr,
		aiSegmentMgr:   a.aiSegmentMgr,
		colorGradeMgr:  a.colorGradeMgr,
		dveCompositor:  a.dveCompositor,
		stmapRegistry:  a.stmapRegistry,
		textAnimEngine: a.textAnimEngine,
		tickerEngine:   a.tickerEngine,
		runID:          req.RunID,
	}

	onProgress := a.macroProgressFn(gen)

	// Run in background goroutine so the HTTP handler returns immediately.
	go func() {
		_ = macro.Run(ctx, m, target, onProgress)
		cancel()

		// Mark completed — state stays for dismiss.
		a.macroMu.Lock()
		// Only clear cancel if this is still the same execution.
		if a.macroGen == gen {
			if a.macroState != nil {
				a.macroState.Running = false
			}
			a.macroCancel = nil
		}
		a.macroMu.Unlock()
		a.broadcast()
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "started", "name": m.Name})
}

// macroProgressFn returns the onProgress callback for the macro execution
// identified by gen. The returned callback publishes the running state and
// broadcasts it, but only while gen is still the current generation.
//
// The generation guard is essential: macro.Run fires a final onProgress
// synchronously on cancellation (and a "wait" step blocks until the context
// is cancelled). A dismissed-then-restarted macro therefore leaves the old
// run's goroutine still unwinding; without the guard its trailing callback
// would clobber the NEW macro's state and broadcast it to every browser.
func (a *API) macroProgressFn(gen uint64) macro.OnProgress {
	return func(state macro.ExecutionState) {
		ms := &internal.MacroExecutionState{
			Running:     state.Running,
			MacroName:   state.MacroName,
			CurrentStep: state.CurrentStep,
			Error:       state.Error,
		}
		ms.Steps = make([]internal.MacroStepState, len(state.Steps))
		for i, s := range state.Steps {
			ms.Steps[i] = internal.MacroStepState{
				Action:      string(s.Action),
				Summary:     s.Summary,
				Status:      string(s.Status),
				Error:       s.Error,
				WaitMs:      s.WaitMs,
				WaitStartMs: s.WaitStartMs,
			}
		}
		a.macroMu.Lock()
		stale := a.macroGen != gen
		if !stale {
			a.macroState = ms
		}
		a.macroMu.Unlock()
		if stale {
			return
		}
		a.broadcast()
	}
}

// handleDismissMacro clears the macro execution state and cancels any
// running macro. Without cancellation, a dismissed macro would continue
// executing steps silently in the background.
func (a *API) handleDismissMacro(w http.ResponseWriter, r *http.Request) {
	a.macroMu.Lock()
	cancel := a.macroCancel
	a.macroState = nil
	a.macroCancel = nil
	// Invalidate the current generation: macro.Run fires a final synchronous
	// onProgress (with Running still true) on the cancellation path, and
	// without a gen bump that trailing callback would pass the staleness
	// guard in macroProgressFn and resurrect the state we just cleared.
	a.macroGen++
	a.macroMu.Unlock()
	if cancel != nil {
		cancel()
	}
	a.broadcast()
	w.WriteHeader(http.StatusNoContent)
}

// handleCancelMacro cancels a running macro.
func (a *API) handleCancelMacro(w http.ResponseWriter, r *http.Request) {
	a.macroMu.Lock()
	cancel := a.macroCancel
	a.macroMu.Unlock()
	if cancel == nil {
		httperr.Write(w, http.StatusNotFound, "no macro running")
		return
	}
	cancel()
	w.WriteHeader(http.StatusNoContent)
}

// apiMacroTarget adapts the API's subsystems to the macro.Target
// interface so Run() can execute macro steps without knowing concrete types.
type apiMacroTarget struct {
	switcher       *switcher.Switcher
	mixer          AudioMixerAPI
	compositor     *graphics.Compositor
	keyer          *graphics.KeyProcessor
	replayMgr      *replay.Manager
	presetStore    *preset.Store
	outputMgr      OutputManagerAPI
	stingerStore   *stinger.Store
	scte35         SCTE35API
	captionMgr     CaptionManagerAPI
	clipMgr        *clip.Manager
	aiSegmentMgr   AISegmentManager
	colorGradeMgr  ColorGradeManager
	dveCompositor  *dve.Compositor
	stmapRegistry  *stmap.Registry
	textAnimEngine *graphics.TextAnimationEngine
	tickerEngine   *graphics.TickerEngine

	// runID and replays derive the ID of each replay clip the run exports.
	runID   string
	replays int
}

// nextReplayClipID returns the clip ID for the run's next replay: derived
// from the run ID and the replay's place in the run, so every engine running
// the same run names its export alike, or "" for a run without an ID.
func (t *apiMacroTarget) nextReplayClipID() string {
	if t.runID == "" {
		return ""
	}
	t.replays++
	sum := sha256.Sum256([]byte("switchframe-macro-replay\x00" + t.runID + "\x00" + strconv.Itoa(t.replays)))
	return hex.EncodeToString(sum[:16])
}

func (t *apiMacroTarget) Cut(ctx context.Context, source string) error {
	return t.switcher.Cut(ctx, source)
}

func (t *apiMacroTarget) SetPreview(ctx context.Context, source string) error {
	return t.switcher.SetPreview(ctx, source)
}

func (t *apiMacroTarget) StartTransition(ctx context.Context, source, transType string, durationMs int, wipeDirection, stingerName string) error {
	var opts []switcher.TransitionOption
	if transType == "stinger" && stingerName != "" && t.stingerStore != nil {
		clip, ok := t.stingerStore.Get(stingerName)
		if !ok {
			return fmt.Errorf("stinger clip %q not found", stingerName)
		}
		sd := clipToStingerData(clip)
		opts = append(opts, switcher.WithStingerData(sd))
	}
	return t.switcher.StartTransition(ctx, source, transType, durationMs, wipeDirection, opts...)
}

func (t *apiMacroTarget) SetLevel(ctx context.Context, source string, level float64) error {
	if t.mixer == nil {
		return nil
	}
	return t.mixer.SetLevel(source, level)
}

// Execute dispatches all new macro actions to the appropriate subsystem.
func (t *apiMacroTarget) Execute(ctx context.Context, action string, params map[string]any) error {
	switch macro.Action(action) {
	// Switching
	case macro.ActionFTB:
		return t.switcher.FadeToBlack(ctx)

	// Audio
	case macro.ActionAudioMute:
		return t.execAudioMute(params)
	case macro.ActionAudioAFV:
		return t.execAudioAFV(params)
	case macro.ActionAudioTrim:
		return t.execAudioTrim(params)
	case macro.ActionAudioMaster:
		return t.execAudioMaster(params)
	case macro.ActionAudioEQ:
		return t.execAudioEQ(params)
	case macro.ActionAudioCompressor:
		return t.execAudioCompressor(params)
	case macro.ActionAudioDelay:
		return t.execAudioDelay(params)

	// Graphics — layerId is required; defaults to 0 for backward compatibility
	// with macros created before multi-layer migration, but the compositor will
	// return ErrLayerNotFound if that layer doesn't exist.
	case macro.ActionGraphicsOn:
		return t.execGraphicsLayerOp(params, func(c *graphics.Compositor, id int) error { return c.On(id) })
	case macro.ActionGraphicsOff:
		return t.execGraphicsLayerOp(params, func(c *graphics.Compositor, id int) error { return c.Off(id) })
	case macro.ActionGraphicsAutoOn:
		id, err := intParam(params, "layerId", 0)
		if err != nil {
			return err
		}
		durMs, err := floatParam(params, "durationMs", 500)
		if err != nil {
			return err
		}
		dur := time.Duration(durMs) * time.Millisecond
		return t.execGraphics(func(c *graphics.Compositor) error { return c.AutoOn(id, dur) })
	case macro.ActionGraphicsAutoOff:
		id, err := intParam(params, "layerId", 0)
		if err != nil {
			return err
		}
		durMs, err := floatParam(params, "durationMs", 500)
		if err != nil {
			return err
		}
		dur := time.Duration(durMs) * time.Millisecond
		return t.execGraphics(func(c *graphics.Compositor) error { return c.AutoOff(id, dur) })
	case macro.ActionGraphicsAddLayer:
		return t.execGraphics(func(c *graphics.Compositor) error {
			_, err := c.AddLayer()
			return err
		})
	case macro.ActionGraphicsRemoveLayer:
		return t.execGraphicsLayerOp(params, func(c *graphics.Compositor, id int) error { return c.RemoveLayer(id) })
	case macro.ActionGraphicsSetRect:
		return t.execGraphicsSetRect(params)
	case macro.ActionGraphicsSetZOrder:
		id, err := intParam(params, "layerId", 0)
		if err != nil {
			return err
		}
		z, err := intParam(params, "zOrder", 0)
		if err != nil {
			return err
		}
		return t.execGraphics(func(c *graphics.Compositor) error { return c.SetLayerZOrder(id, z) })
	case macro.ActionGraphicsFlyIn:
		return t.execGraphicsFly(params, func(c *graphics.Compositor, id int, dir string, dur int) error { return c.FlyIn(id, dir, dur) })
	case macro.ActionGraphicsFlyOut:
		return t.execGraphicsFly(params, func(c *graphics.Compositor, id int, dir string, dur int) error { return c.FlyOut(id, dir, dur) })
	case macro.ActionGraphicsSlide:
		return t.execGraphicsSlide(params)
	case macro.ActionGraphicsAnimate:
		id, err := intParam(params, "layerId", 0)
		if err != nil {
			return err
		}
		return t.execGraphicsAnimate(id, params)
	case macro.ActionGraphicsAnimateStop:
		return t.execGraphicsLayerOp(params, func(c *graphics.Compositor, id int) error { return c.StopAnimation(id) })
	case macro.ActionGraphicsUploadFrame:
		id, err := intParam(params, "layerId", 0)
		if err != nil {
			return err
		}
		return t.execGraphicsUploadFrame(id, params)

	// Output
	case macro.ActionRecordingStart:
		return t.execRecordingStart(params)
	case macro.ActionRecordingStop:
		return t.execRecordingStop()

	// Presets
	case macro.ActionPresetRecall:
		return t.execPresetRecall(ctx, params)

	// Keys
	case macro.ActionKeySet:
		return t.execKeySet(params)
	case macro.ActionKeyDelete:
		return t.execKeyDelete(params)

	// AI Segmentation
	case macro.ActionAISegEnable:
		return t.execAISegEnable(params)
	case macro.ActionAISegDisable:
		return t.execAISegDisable(params)

	// Source
	case macro.ActionSourceLabel:
		return t.execSourceLabel(ctx, params)
	case macro.ActionSourceDelay:
		return t.execSourceDelay(params)
	case macro.ActionSourcePosition:
		return t.execSourcePosition(params)

	// Replay (mark-based)
	case macro.ActionReplayMarkIn:
		return t.execReplayMarkIn(params)
	case macro.ActionReplayMarkOut:
		return t.execReplayMarkOut(params)
	case macro.ActionReplayPlay:
		return t.execReplayPlay(params)
	case macro.ActionReplayStop:
		return t.execReplayStop()

	// Captions
	case macro.ActionCaptionMode:
		return t.execCaptionMode(params)
	case macro.ActionCaptionText:
		return t.execCaptionText(params)
	case macro.ActionCaptionClear:
		return t.execCaptionClear()

	// Replay (clip-based — from replay UX plan)
	case macro.ActionReplayQuickClip:
		return t.execReplayQuickClip(params)
	case macro.ActionReplayPlayLast:
		return t.execReplayPlayLast()
	case macro.ActionReplayPlayClip:
		return t.execReplayPlayClip(params)

	// Clips
	case macro.ActionClipLoad:
		return t.execClipLoad(params)
	case macro.ActionClipPlay:
		return t.execClipPlay(params)
	case macro.ActionClipPause:
		return t.execClipPause(params)
	case macro.ActionClipStop:
		return t.execClipStop(params)
	case macro.ActionClipEject:
		return t.execClipEject(params)
	case macro.ActionClipSeek:
		return t.execClipSeek(params)

	// Color Grade
	case macro.ActionColorGradeSet:
		return t.execColorGradeSet(params)
	case macro.ActionColorGradeOff:
		return t.execColorGradeOff()

	// Layout
	case macro.ActionLayoutPreset:
		return t.execLayoutPreset(params)
	case macro.ActionLayoutSlotOn:
		return t.execLayoutSlotOn(params)
	case macro.ActionLayoutSlotOff:
		return t.execLayoutSlotOff(params)
	case macro.ActionLayoutSlotSource:
		return t.execLayoutSlotSource(params)
	case macro.ActionLayoutClear:
		return t.execLayoutClear()

	// ST Map
	case macro.ActionSTMapAssignSource:
		return t.execSTMapAssignSource(params)
	case macro.ActionSTMapRemoveSource:
		return t.execSTMapRemoveSource(params)
	case macro.ActionSTMapAssignProgram:
		return t.execSTMapAssignProgram(params)
	case macro.ActionSTMapRemoveProgram:
		return t.execSTMapRemoveProgram()

	// Graphics (text/ticker/fly-on)
	case macro.ActionGraphicsFlyOn:
		return t.execGraphicsFly(params, func(c *graphics.Compositor, id int, dir string, dur int) error { return c.FlyOn(id, dir, dur) })
	case macro.ActionGraphicsTextAnimate:
		return t.execTextAnimate(params)
	case macro.ActionGraphicsTextAnimateStop:
		return t.execTextAnimateStop(params)
	case macro.ActionGraphicsTickerStart:
		return t.execTickerStart(params)
	case macro.ActionGraphicsTickerStop:
		return t.execTickerStop(params)

	default:
		return fmt.Errorf("unimplemented action %q", action)
	}
}

// SCTE-35 methods — wired to real injector.

func (t *apiMacroTarget) SCTE35Cue(_ context.Context, params map[string]any) (uint32, error) {
	if t.scte35 == nil {
		return 0, errors.New("scte35 not enabled")
	}
	msg := &scte35.CueMessage{}
	ct, err := stringParam(params, "commandType")
	if err != nil {
		return 0, err
	}
	switch ct {
	case "", "splice_insert":
		msg.CommandType = scte35.CommandSpliceInsert
	case "time_signal":
		msg.CommandType = scte35.CommandTimeSignal
	default:
		return 0, fmt.Errorf("invalid commandType %q", ct)
	}
	if msg.IsOut, err = boolParam(params, "isOut", false); err != nil {
		return 0, err
	}
	if msg.AutoReturn, err = boolParam(params, "autoReturn", false); err != nil {
		return 0, err
	}
	if _, present := params["durationMs"]; present {
		durMs, err := floatParam(params, "durationMs", 0)
		if err != nil {
			return 0, err
		}
		d := time.Duration(int64(durMs)) * time.Millisecond
		msg.BreakDuration = &d
	}
	eventID, err := floatParam(params, "eventId", 0)
	if err != nil {
		return 0, err
	}
	msg.EventID = uint32(eventID)
	uniqueProgramID, err := floatParam(params, "uniqueProgramId", 0)
	if err != nil {
		return 0, err
	}
	msg.UniqueProgramID = uint16(uniqueProgramID)
	availNum, err := floatParam(params, "availNum", 0)
	if err != nil {
		return 0, err
	}
	msg.AvailNum = uint8(availNum)
	availsExpected, err := floatParam(params, "availsExpected", 0)
	if err != nil {
		return 0, err
	}
	msg.AvailsExpected = uint8(availsExpected)
	// Parse descriptors for time_signal commands.
	if raw, present := params["descriptors"]; present {
		descs, ok := raw.([]any)
		if !ok {
			return 0, fmt.Errorf("param %q: expected array, got %T", "descriptors", raw)
		}
		for i, rawDesc := range descs {
			dm, ok := rawDesc.(map[string]any)
			if !ok {
				return 0, fmt.Errorf("descriptors[%d]: expected object, got %T", i, rawDesc)
			}
			desc := scte35.SegmentationDescriptor{}
			segType, err := floatParam(dm, "segmentationType", 0)
			if err != nil {
				return 0, fmt.Errorf("descriptors[%d]: %w", i, err)
			}
			desc.SegmentationType = uint8(segType)
			segEventID, err := floatParam(dm, "segEventId", 0)
			if err != nil {
				return 0, fmt.Errorf("descriptors[%d]: %w", i, err)
			}
			desc.SegEventID = uint32(segEventID)
			upidType, err := floatParam(dm, "upidType", 0)
			if err != nil {
				return 0, fmt.Errorf("descriptors[%d]: %w", i, err)
			}
			desc.UPIDType = uint8(upidType)
			upid, err := stringParam(dm, "upid")
			if err != nil {
				return 0, fmt.Errorf("descriptors[%d]: %w", i, err)
			}
			if upid != "" {
				desc.UPID = []byte(upid)
			}
			durMs, err := floatParam(dm, "durationMs", 0)
			if err != nil {
				return 0, fmt.Errorf("descriptors[%d]: %w", i, err)
			}
			if durMs > 0 {
				ticks := uint64(durMs) * 90
				desc.DurationTicks = &ticks
			}
			msg.Descriptors = append(msg.Descriptors, desc)
		}
	}
	msg.Source = "macro"
	preRollMs, err := floatParam(params, "preRollMs", 0)
	if err != nil {
		return 0, err
	}
	if preRollMs > 0 {
		return t.scte35.ScheduleCue(msg, int64(preRollMs))
	}
	return t.scte35.InjectCue(msg)
}

func (t *apiMacroTarget) SCTE35Return(_ context.Context, eventID uint32) error {
	if t.scte35 == nil {
		return errors.New("scte35 not enabled")
	}
	return t.scte35.ReturnToProgram(eventID)
}

func (t *apiMacroTarget) SCTE35Cancel(_ context.Context, eventID uint32) error {
	if t.scte35 == nil {
		return errors.New("scte35 not enabled")
	}
	return t.scte35.CancelEvent(eventID)
}

func (t *apiMacroTarget) SCTE35Hold(_ context.Context, eventID uint32) error {
	if t.scte35 == nil {
		return errors.New("scte35 not enabled")
	}
	return t.scte35.HoldBreak(eventID)
}

func (t *apiMacroTarget) SCTE35Extend(_ context.Context, eventID uint32, durationMs int64) error {
	if t.scte35 == nil {
		return errors.New("scte35 not enabled")
	}
	return t.scte35.ExtendBreak(eventID, durationMs)
}

// --- Audio helpers ---

func (t *apiMacroTarget) execAudioMute(params map[string]any) error {
	if t.mixer == nil {
		return nil
	}
	source, err := stringParam(params, "source")
	if err != nil {
		return err
	}
	muted, err := boolParam(params, "muted", true)
	if err != nil {
		return err
	}
	return t.mixer.SetMuted(source, muted)
}

func (t *apiMacroTarget) execAudioAFV(params map[string]any) error {
	if t.mixer == nil {
		return nil
	}
	source, err := stringParam(params, "source")
	if err != nil {
		return err
	}
	afv, err := boolParam(params, "afv", true)
	if err != nil {
		return err
	}
	return t.mixer.SetAFV(source, afv)
}

func (t *apiMacroTarget) execAudioTrim(params map[string]any) error {
	if t.mixer == nil {
		return nil
	}
	source, err := stringParam(params, "source")
	if err != nil {
		return err
	}
	trim, err := floatParam(params, "trim", 0)
	if err != nil {
		return err
	}
	return t.mixer.SetTrim(source, trim)
}

func (t *apiMacroTarget) execAudioMaster(params map[string]any) error {
	if t.mixer == nil {
		return nil
	}
	level, err := floatParam(params, "level", 0)
	if err != nil {
		return err
	}
	return t.mixer.SetMasterLevel(level)
}

func (t *apiMacroTarget) execAudioEQ(params map[string]any) error {
	if t.mixer == nil {
		return nil
	}
	source, err := stringParam(params, "source")
	if err != nil {
		return err
	}
	band, err := intParam(params, "band", 0)
	if err != nil {
		return err
	}
	freq, err := floatParam(params, "frequency", 1000)
	if err != nil {
		return err
	}
	gain, err := floatParam(params, "gain", 0)
	if err != nil {
		return err
	}
	q, err := floatParam(params, "q", 1.0)
	if err != nil {
		return err
	}
	enabled, err := boolParam(params, "enabled", true)
	if err != nil {
		return err
	}
	return t.mixer.SetEQ(source, band, freq, gain, q, enabled)
}

func (t *apiMacroTarget) execAudioCompressor(params map[string]any) error {
	if t.mixer == nil {
		return nil
	}
	source, err := stringParam(params, "source")
	if err != nil {
		return err
	}
	threshold, err := floatParam(params, "threshold", -20)
	if err != nil {
		return err
	}
	ratio, err := floatParam(params, "ratio", 4)
	if err != nil {
		return err
	}
	attack, err := floatParam(params, "attack", 10)
	if err != nil {
		return err
	}
	release, err := floatParam(params, "release", 100)
	if err != nil {
		return err
	}
	makeupGain, err := floatParam(params, "makeupGain", 0)
	if err != nil {
		return err
	}
	return t.mixer.SetCompressor(source, threshold, ratio, attack, release, makeupGain)
}

func (t *apiMacroTarget) execAudioDelay(params map[string]any) error {
	if t.mixer == nil {
		return nil
	}
	source, err := stringParam(params, "source")
	if err != nil {
		return err
	}
	delayMs, err := intParam(params, "delayMs", 0)
	if err != nil {
		return err
	}
	return t.mixer.SetAudioDelay(source, delayMs)
}

// --- Graphics helpers ---

func (t *apiMacroTarget) execGraphics(fn func(*graphics.Compositor) error) error {
	if t.compositor == nil {
		return nil
	}
	return fn(t.compositor)
}

func (t *apiMacroTarget) execGraphicsLayerOp(params map[string]any, op func(*graphics.Compositor, int) error) error {
	id, err := intParam(params, "layerId", 0)
	if err != nil {
		return err
	}
	return t.execGraphics(func(c *graphics.Compositor) error { return op(c, id) })
}

func (t *apiMacroTarget) execGraphicsFly(params map[string]any, op func(*graphics.Compositor, int, string, int) error) error {
	id, err := intParam(params, "layerId", 0)
	if err != nil {
		return err
	}
	dir, err := stringParam(params, "direction")
	if err != nil {
		return err
	}
	dur, err := intParam(params, "durationMs", 500)
	if err != nil {
		return err
	}
	return t.execGraphics(func(c *graphics.Compositor) error { return op(c, id, dir, dur) })
}

func (t *apiMacroTarget) execGraphicsSetRect(params map[string]any) error {
	id, err := intParam(params, "layerId", 0)
	if err != nil {
		return err
	}
	x, err := intParam(params, "x", 0)
	if err != nil {
		return err
	}
	y, err := intParam(params, "y", 0)
	if err != nil {
		return err
	}
	w, err := intParam(params, "width", 0)
	if err != nil {
		return err
	}
	h, err := intParam(params, "height", 0)
	if err != nil {
		return err
	}
	return t.execGraphics(func(c *graphics.Compositor) error {
		return c.SetLayerRect(id, image.Rect(x, y, x+w, y+h))
	})
}

func (t *apiMacroTarget) execGraphicsSlide(params map[string]any) error {
	id, err := intParam(params, "layerId", 0)
	if err != nil {
		return err
	}
	x, err := intParam(params, "x", 0)
	if err != nil {
		return err
	}
	y, err := intParam(params, "y", 0)
	if err != nil {
		return err
	}
	w, err := intParam(params, "width", 0)
	if err != nil {
		return err
	}
	h, err := intParam(params, "height", 0)
	if err != nil {
		return err
	}
	dur, err := intParam(params, "durationMs", 500)
	if err != nil {
		return err
	}
	return t.execGraphics(func(c *graphics.Compositor) error {
		return c.SlideLayer(id, image.Rect(x, y, x+w, y+h), dur)
	})
}

func (t *apiMacroTarget) execGraphicsAnimate(id int, params map[string]any) error {
	if t.compositor == nil {
		return nil
	}
	mode, err := stringParam(params, "mode")
	if err != nil {
		return err
	}
	minAlpha, err := floatParam(params, "minAlpha", 0)
	if err != nil {
		return err
	}
	maxAlpha, err := floatParam(params, "maxAlpha", 1)
	if err != nil {
		return err
	}
	speedHz, err := floatParam(params, "speedHz", 1)
	if err != nil {
		return err
	}
	cfg := graphics.AnimationConfig{
		Mode:     mode,
		MinAlpha: minAlpha,
		MaxAlpha: maxAlpha,
		SpeedHz:  speedHz,
	}
	if mode == "transition" {
		if _, present := params["toX"]; present {
			x, err := floatParam(params, "toX", 0)
			if err != nil {
				return err
			}
			y, err := floatParam(params, "toY", 0)
			if err != nil {
				return err
			}
			w, err := floatParam(params, "toWidth", 0)
			if err != nil {
				return err
			}
			h, err := floatParam(params, "toHeight", 0)
			if err != nil {
				return err
			}
			cfg.ToRect = &graphics.RectState{X: int(x), Y: int(y), Width: int(w), Height: int(h)}
		}
		if _, present := params["toAlpha"]; present {
			a, err := floatParam(params, "toAlpha", 0)
			if err != nil {
				return err
			}
			cfg.ToAlpha = &a
		}
		durMs, err := intParam(params, "durationMs", 500)
		if err != nil {
			return err
		}
		cfg.DurationMs = durMs
		easing, err := stringParam(params, "easing")
		if err != nil {
			return err
		}
		cfg.Easing = easing
	}
	return t.compositor.Animate(id, cfg)
}

func (t *apiMacroTarget) execGraphicsUploadFrame(id int, params map[string]any) error {
	if t.compositor == nil {
		return nil
	}
	w, err := intParam(params, "width", 0)
	if err != nil {
		return err
	}
	h, err := intParam(params, "height", 0)
	if err != nil {
		return err
	}
	tmpl, err := stringParam(params, "template")
	if err != nil {
		return err
	}
	rgbaB64, err := stringParam(params, "rgba")
	if err != nil {
		return err
	}
	if w <= 0 || h <= 0 {
		return errors.New("width and height must be positive")
	}
	rgba, err := base64.StdEncoding.DecodeString(rgbaB64)
	if err != nil {
		return fmt.Errorf("invalid base64 rgba data: %w", err)
	}
	if len(rgba) != w*h*4 {
		return fmt.Errorf("rgba size mismatch: expected %d, got %d", w*h*4, len(rgba))
	}
	return t.compositor.SetOverlay(id, rgba, w, h, tmpl)
}

// --- Output helpers ---

func (t *apiMacroTarget) execRecordingStart(params map[string]any) error {
	if t.outputMgr == nil {
		return nil
	}
	dir, err := stringParam(params, "directory")
	if err != nil {
		return err
	}
	return t.outputMgr.StartRecording(output.RecorderConfig{Dir: dir})
}

func (t *apiMacroTarget) execRecordingStop() error {
	if t.outputMgr == nil {
		return nil
	}
	return t.outputMgr.StopRecording()
}

// --- Preset helpers ---

func (t *apiMacroTarget) execPresetRecall(ctx context.Context, params map[string]any) error {
	if t.presetStore == nil {
		return nil
	}
	id, err := stringParam(params, "id")
	if err != nil {
		return err
	}
	if id == "" {
		// Also accept "name" for backward compat.
		id, err = stringParam(params, "name")
		if err != nil {
			return err
		}
	}
	if id == "" {
		return errors.New("preset_recall requires 'id' param")
	}
	p, ok := t.presetStore.Get(id)
	if !ok {
		return fmt.Errorf("preset %q not found", id)
	}
	recallTarget := &macroPresetRecallTarget{
		switcher: t.switcher,
		mixer:    t.mixer,
	}
	warnings := preset.Recall(ctx, p, recallTarget)
	_ = warnings
	return nil
}

// macroPresetRecallTarget adapts switcher+mixer to preset.RecallTarget.
type macroPresetRecallTarget struct {
	switcher *switcher.Switcher
	mixer    AudioMixerAPI
}

func (rt *macroPresetRecallTarget) Cut(ctx context.Context, source string) error {
	return rt.switcher.Cut(ctx, source)
}

func (rt *macroPresetRecallTarget) SetPreview(ctx context.Context, source string) error {
	return rt.switcher.SetPreview(ctx, source)
}

func (rt *macroPresetRecallTarget) SetLevel(sourceKey string, levelDB float64) error {
	if rt.mixer == nil {
		return nil
	}
	return rt.mixer.SetLevel(sourceKey, levelDB)
}

func (rt *macroPresetRecallTarget) SetMuted(sourceKey string, muted bool) error {
	if rt.mixer == nil {
		return nil
	}
	return rt.mixer.SetMuted(sourceKey, muted)
}

func (rt *macroPresetRecallTarget) SetAFV(sourceKey string, afv bool) error {
	if rt.mixer == nil {
		return nil
	}
	return rt.mixer.SetAFV(sourceKey, afv)
}

func (rt *macroPresetRecallTarget) SetMasterLevel(level float64) error {
	if rt.mixer == nil {
		return nil
	}
	return rt.mixer.SetMasterLevel(level)
}

// --- Key helpers ---

func (t *apiMacroTarget) execKeySet(params map[string]any) error {
	// Validate params before the nil-keyer early return so malformed client
	// macros surface an error even if the subsystem is disabled.
	source, err := stringParam(params, "source")
	if err != nil {
		return err
	}
	config := graphics.KeyConfig{}
	if raw, present := params["config"]; present {
		configMap, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("param %q: expected object, got %T", "config", raw)
		}
		data, err := json.Marshal(configMap)
		if err != nil {
			return fmt.Errorf("marshal key config: %w", err)
		}
		if err := json.Unmarshal(data, &config); err != nil {
			return fmt.Errorf("parse key config: %w", err)
		}
	}
	if t.keyer == nil {
		return nil
	}
	t.keyer.SetKey(source, config)
	return nil
}

func (t *apiMacroTarget) execKeyDelete(params map[string]any) error {
	source, err := stringParam(params, "source")
	if err != nil {
		return err
	}
	if t.keyer == nil {
		return nil
	}
	t.keyer.RemoveKey(source)
	return nil
}

// --- Source helpers ---

func (t *apiMacroTarget) execSourceLabel(ctx context.Context, params map[string]any) error {
	source, err := stringParam(params, "source")
	if err != nil {
		return err
	}
	label, err := stringParam(params, "label")
	if err != nil {
		return err
	}
	return t.switcher.SetLabel(ctx, source, label)
}

func (t *apiMacroTarget) execSourceDelay(params map[string]any) error {
	source, err := stringParam(params, "source")
	if err != nil {
		return err
	}
	delayMs, err := intParam(params, "delayMs", 0)
	if err != nil {
		return err
	}
	return t.switcher.SetSourceDelay(source, delayMs)
}

func (t *apiMacroTarget) execSourcePosition(params map[string]any) error {
	source, err := stringParam(params, "source")
	if err != nil {
		return err
	}
	pos, err := intParam(params, "position", 1)
	if err != nil {
		return err
	}
	return t.switcher.SetSourcePosition(source, pos)
}

// --- Replay helpers (mark-based) ---

func (t *apiMacroTarget) execReplayMarkIn(params map[string]any) error {
	if t.replayMgr == nil {
		return nil
	}
	source, err := stringParam(params, "source")
	if err != nil {
		return err
	}
	return t.replayMgr.MarkIn(source)
}

func (t *apiMacroTarget) execReplayMarkOut(params map[string]any) error {
	if t.replayMgr == nil {
		return nil
	}
	source, err := stringParam(params, "source")
	if err != nil {
		return err
	}
	return t.replayMgr.MarkOut(source)
}

func (t *apiMacroTarget) execReplayPlay(params map[string]any) error {
	if t.replayMgr == nil {
		return nil
	}
	source, err := stringParam(params, "source")
	if err != nil {
		return err
	}
	speed, err := floatParam(params, "speed", 0.5)
	if err != nil {
		return err
	}
	loop, err := boolParam(params, "loop", false)
	if err != nil {
		return err
	}
	return t.replayMgr.PlayAs(source, speed, loop, t.nextReplayClipID())
}

func (t *apiMacroTarget) execReplayStop() error {
	if t.replayMgr == nil {
		return nil
	}
	return t.replayMgr.Stop()
}

// --- Replay helpers (clip-based — stubs for replay UX plan) ---

func (t *apiMacroTarget) execReplayQuickClip(params map[string]any) error {
	if t.replayMgr == nil {
		return nil
	}
	// Will call replayMgr.QuickClip() once the replay UX plan lands.
	// For now, fall back to mark-based: MarkIn (now - duration) + MarkOut (now) + Play.
	source, err := stringParam(params, "source")
	if err != nil {
		return err
	}
	speed, err := floatParam(params, "speed", 0.5)
	if err != nil {
		return err
	}
	if err := t.replayMgr.MarkIn(source); err != nil {
		return err
	}
	if err := t.replayMgr.MarkOut(source); err != nil {
		return err
	}
	return t.replayMgr.PlayAs(source, speed, false, t.nextReplayClipID())
}

func (t *apiMacroTarget) execReplayPlayLast() error {
	if t.replayMgr == nil {
		return nil
	}
	// Will call replayMgr.PlayLast() once the replay UX plan lands.
	// For now, this is a no-op since there's no clip concept yet.
	return nil
}

func (t *apiMacroTarget) execReplayPlayClip(params map[string]any) error {
	if t.replayMgr == nil {
		return nil
	}
	// Will call replayMgr.PlayClip(clipId) once the replay UX plan lands.
	_ = params["clipId"]
	return nil
}

// --- Caption helpers ---

func (t *apiMacroTarget) execCaptionMode(params map[string]any) error {
	if t.captionMgr == nil {
		return errors.New("captions not enabled")
	}
	mode, err := stringParam(params, "mode")
	if err != nil {
		return err
	}
	m, ok := caption.ParseMode(mode)
	if !ok {
		return fmt.Errorf("invalid caption mode %q", mode)
	}
	t.captionMgr.SetMode(m)
	return nil
}

func (t *apiMacroTarget) execCaptionText(params map[string]any) error {
	if t.captionMgr == nil {
		return errors.New("captions not enabled")
	}
	text, err := stringParam(params, "text")
	if err != nil {
		return err
	}
	if text != "" {
		t.captionMgr.IngestText(text)
	}
	newline, err := boolParam(params, "newline", false)
	if err != nil {
		return err
	}
	if newline {
		t.captionMgr.IngestNewline()
	}
	return nil
}

func (t *apiMacroTarget) execCaptionClear() error {
	if t.captionMgr == nil {
		return errors.New("captions not enabled")
	}
	t.captionMgr.Clear()
	return nil
}

// --- Clip helpers ---

func (t *apiMacroTarget) execClipLoad(params map[string]interface{}) error {
	if t.clipMgr == nil {
		return fmt.Errorf("clips not enabled")
	}
	playerID, err := intParam(params, "player", 1)
	if err != nil {
		return err
	}
	clipID, err := stringParam(params, "clipId")
	if err != nil {
		return err
	}
	if clipID == "" {
		return fmt.Errorf("clip_load requires 'clipId' param")
	}
	return t.clipMgr.Load(playerID, clipID)
}

func (t *apiMacroTarget) execClipPlay(params map[string]interface{}) error {
	if t.clipMgr == nil {
		return fmt.Errorf("clips not enabled")
	}
	playerID, err := intParam(params, "player", 1)
	if err != nil {
		return err
	}
	speed, err := floatParam(params, "speed", 1.0)
	if err != nil {
		return err
	}
	loop, err := boolParam(params, "loop", false)
	if err != nil {
		return err
	}
	return t.clipMgr.Play(playerID, speed, loop)
}

func (t *apiMacroTarget) execClipPause(params map[string]interface{}) error {
	if t.clipMgr == nil {
		return fmt.Errorf("clips not enabled")
	}
	playerID, err := intParam(params, "player", 1)
	if err != nil {
		return err
	}
	return t.clipMgr.Pause(playerID)
}

func (t *apiMacroTarget) execClipStop(params map[string]interface{}) error {
	if t.clipMgr == nil {
		return fmt.Errorf("clips not enabled")
	}
	playerID, err := intParam(params, "player", 1)
	if err != nil {
		return err
	}
	return t.clipMgr.Stop(playerID)
}

func (t *apiMacroTarget) execClipEject(params map[string]interface{}) error {
	if t.clipMgr == nil {
		return fmt.Errorf("clips not enabled")
	}
	playerID, err := intParam(params, "player", 1)
	if err != nil {
		return err
	}
	return t.clipMgr.Eject(playerID)
}

func (t *apiMacroTarget) execClipSeek(params map[string]interface{}) error {
	if t.clipMgr == nil {
		return fmt.Errorf("clips not enabled")
	}
	playerID, err := intParam(params, "player", 1)
	if err != nil {
		return err
	}
	position, err := floatParam(params, "position", 0)
	if err != nil {
		return err
	}
	return t.clipMgr.Seek(playerID, position)
}

// --- AI Segmentation ---

func (t *apiMacroTarget) execAISegEnable(params map[string]any) error {
	if t.aiSegmentMgr == nil {
		return fmt.Errorf("ai segmentation not available")
	}
	source, err := stringParam(params, "source")
	if err != nil {
		return err
	}
	if source == "" {
		return fmt.Errorf("ai_segment_enable requires 'source' param")
	}
	sensitivityF, err := floatParam(params, "sensitivity", 0.7)
	if err != nil {
		return err
	}
	edgeSmoothF, err := floatParam(params, "edgeSmooth", 0.5)
	if err != nil {
		return err
	}
	background, err := stringParam(params, "background")
	if err != nil {
		return err
	}
	return t.aiSegmentMgr.EnableAISegment(source, float32(sensitivityF), float32(edgeSmoothF), background)
}

func (t *apiMacroTarget) execAISegDisable(params map[string]any) error {
	if t.aiSegmentMgr == nil {
		return fmt.Errorf("ai segmentation not available")
	}
	source, err := stringParam(params, "source")
	if err != nil {
		return err
	}
	if source == "" {
		return fmt.Errorf("ai_segment_disable requires 'source' param")
	}
	t.aiSegmentMgr.DisableAISegment(source)
	return nil
}

// --- Color Grade helpers ---

func (t *apiMacroTarget) execColorGradeSet(params map[string]any) error {
	if t.colorGradeMgr == nil {
		return nil
	}
	lookName, err := stringParam(params, "lookName")
	if err != nil {
		return err
	}
	if lookName == "" {
		return errors.New("colorgrade_set requires 'lookName' param")
	}
	intensityF, err := floatParam(params, "intensity", 1.0)
	if err != nil {
		return err
	}
	return t.colorGradeMgr.SetColorGrade(lookName, float32(intensityF))
}

func (t *apiMacroTarget) execColorGradeOff() error {
	if t.colorGradeMgr == nil {
		return nil
	}
	t.colorGradeMgr.DisableColorGrade()
	return nil
}

// --- Layout helpers ---

func (t *apiMacroTarget) execLayoutPreset(params map[string]any) error {
	if t.dveCompositor == nil {
		return nil
	}
	preset, err := stringParam(params, "preset")
	if err != nil {
		return err
	}
	if preset == "" {
		return errors.New("layout_preset requires 'preset' param")
	}
	w, h := t.dveCompositor.FrameSize()
	p := dve.ResolveBuiltinPreset(preset, w, h)
	if p == nil {
		return fmt.Errorf("layout preset %q not found", preset)
	}
	t.dveCompositor.SetLayout(dveLayoutFromPreset(p))
	return nil
}

func (t *apiMacroTarget) execLayoutSlotOn(params map[string]any) error {
	if t.dveCompositor == nil {
		return nil
	}
	slot, err := intParam(params, "slot", 0)
	if err != nil {
		return err
	}
	t.dveCompositor.SlotOn(slot)
	return nil
}

func (t *apiMacroTarget) execLayoutSlotOff(params map[string]any) error {
	if t.dveCompositor == nil {
		return nil
	}
	slot, err := intParam(params, "slot", 0)
	if err != nil {
		return err
	}
	t.dveCompositor.SlotOff(slot)
	return nil
}

func (t *apiMacroTarget) execLayoutSlotSource(params map[string]any) error {
	if t.dveCompositor == nil {
		return nil
	}
	slot, err := intParam(params, "slot", 0)
	if err != nil {
		return err
	}
	source, err := stringParam(params, "source")
	if err != nil {
		return err
	}
	t.dveCompositor.UpdateSlot(slot, func(s *dve.Slot) {
		s.SourceKey = source
	})
	return nil
}

func (t *apiMacroTarget) execLayoutClear() error {
	if t.dveCompositor == nil {
		return nil
	}
	t.dveCompositor.SetLayout(nil)
	return nil
}

// --- ST Map helpers ---

func (t *apiMacroTarget) execSTMapAssignSource(params map[string]any) error {
	if t.stmapRegistry == nil {
		return nil
	}
	source, err := stringParam(params, "source")
	if err != nil {
		return err
	}
	mapName, err := stringParam(params, "map")
	if err != nil {
		return err
	}
	return t.stmapRegistry.AssignSource(source, mapName)
}

func (t *apiMacroTarget) execSTMapRemoveSource(params map[string]any) error {
	if t.stmapRegistry == nil {
		return nil
	}
	source, err := stringParam(params, "source")
	if err != nil {
		return err
	}
	t.stmapRegistry.RemoveSource(source)
	return nil
}

func (t *apiMacroTarget) execSTMapAssignProgram(params map[string]any) error {
	if t.stmapRegistry == nil {
		return nil
	}
	mapName, err := stringParam(params, "map")
	if err != nil {
		return err
	}
	return t.stmapRegistry.AssignProgram(mapName)
}

func (t *apiMacroTarget) execSTMapRemoveProgram() error {
	if t.stmapRegistry == nil {
		return nil
	}
	t.stmapRegistry.RemoveProgram()
	return nil
}

// --- Text Animation / Ticker helpers ---

func (t *apiMacroTarget) execTextAnimate(params map[string]any) error {
	if t.textAnimEngine == nil {
		return nil
	}
	id, err := intParam(params, "layerId", 0)
	if err != nil {
		return err
	}
	mode, err := stringParam(params, "mode")
	if err != nil {
		return err
	}
	text, err := stringParam(params, "text")
	if err != nil {
		return err
	}
	fontSize, err := floatParam(params, "fontSize", 0)
	if err != nil {
		return err
	}
	bold, err := boolParam(params, "bold", false)
	if err != nil {
		return err
	}
	charsPerSec, err := floatParam(params, "charsPerSec", 0)
	if err != nil {
		return err
	}
	wordDelayMs, err := intParam(params, "wordDelayMs", 0)
	if err != nil {
		return err
	}
	fadeDurationMs, err := intParam(params, "fadeDurationMs", 0)
	if err != nil {
		return err
	}
	width, err := intParam(params, "width", 0)
	if err != nil {
		return err
	}
	height, err := intParam(params, "height", 0)
	if err != nil {
		return err
	}
	cfg := graphics.TextAnimationConfig{
		Mode:           mode,
		Text:           text,
		FontSize:       fontSize,
		Bold:           bold,
		CharsPerSec:    charsPerSec,
		WordDelayMs:    wordDelayMs,
		FadeDurationMs: fadeDurationMs,
		Width:          width,
		Height:         height,
	}
	return t.textAnimEngine.Start(id, cfg)
}

func (t *apiMacroTarget) execTextAnimateStop(params map[string]any) error {
	if t.textAnimEngine == nil {
		return nil
	}
	id, err := intParam(params, "layerId", 0)
	if err != nil {
		return err
	}
	return t.textAnimEngine.Stop(id)
}

func (t *apiMacroTarget) execTickerStart(params map[string]any) error {
	if t.tickerEngine == nil {
		return nil
	}
	id, err := intParam(params, "layerId", 0)
	if err != nil {
		return err
	}
	text, err := stringParam(params, "text")
	if err != nil {
		return err
	}
	speed, err := floatParam(params, "speed", 50)
	if err != nil {
		return err
	}
	fontSize, err := floatParam(params, "fontSize", 0)
	if err != nil {
		return err
	}
	bold, err := boolParam(params, "bold", false)
	if err != nil {
		return err
	}
	loop, err := boolParam(params, "loop", false)
	if err != nil {
		return err
	}
	height, err := intParam(params, "height", 0)
	if err != nil {
		return err
	}
	cfg := graphics.TickerConfig{
		Text:     text,
		Speed:    speed,
		FontSize: fontSize,
		Bold:     bold,
		Loop:     loop,
		Height:   height,
	}
	return t.tickerEngine.Start(id, cfg)
}

func (t *apiMacroTarget) execTickerStop(params map[string]any) error {
	if t.tickerEngine == nil {
		return nil
	}
	id, err := intParam(params, "layerId", 0)
	if err != nil {
		return err
	}
	return t.tickerEngine.Stop(id)
}

// --- Param helpers ---
//
// Delegate to macro.Param* so a mistyped JSON value fails loud at executeStep
// instead of silently falling back to the default. See macro/params.go.

func stringParam(params map[string]any, key string) (string, error) {
	return macro.ParamStringDefault(params, key, "")
}

func floatParam(params map[string]any, key string, defaultVal float64) (float64, error) {
	return macro.ParamFloatDefault(params, key, defaultVal)
}

func boolParam(params map[string]any, key string, defaultVal bool) (bool, error) {
	return macro.ParamBoolDefault(params, key, defaultVal)
}

// intParam is a convenience over floatParam for the common
// `int(floatParam(...))` pattern used for layer/slot/player IDs etc.
func intParam(params map[string]any, key string, defaultVal int) (int, error) {
	v, err := macro.ParamFloatDefault(params, key, float64(defaultVal))
	if err != nil {
		return 0, err
	}
	return int(v), nil
}
