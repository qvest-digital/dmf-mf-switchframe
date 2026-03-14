package control

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"net/http"
	"time"

	"github.com/zsiec/switchframe/server/caption"
	"github.com/zsiec/switchframe/server/clip"
	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/macro"
	"github.com/zsiec/switchframe/server/output"
	"github.com/zsiec/switchframe/server/preset"
	"github.com/zsiec/switchframe/server/replay"
	"github.com/zsiec/switchframe/server/scte35"
	"github.com/zsiec/switchframe/server/stinger"
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
		switcher:     a.switcher,
		mixer:        a.mixer,
		compositor:   a.compositor,
		keyer:        a.keyer,
		replayMgr:    a.replayMgr,
		presetStore:  a.presetStore,
		outputMgr:    a.outputMgr,
		stingerStore: a.stingerStore,
		scte35:       a.scte35,
		captionMgr:   a.captionMgr,
		clipMgr:      a.clipMgr,
	}

	onProgress := func(state macro.ExecutionState) {
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
		a.macroState = ms
		a.macroMu.Unlock()
		a.broadcast()
	}

	// Run in background goroutine so the HTTP handler returns immediately.
	go func() {
		macro.Run(ctx, m, target, onProgress)
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

// handleDismissMacro clears the macro execution state.
func (a *API) handleDismissMacro(w http.ResponseWriter, r *http.Request) {
	a.macroMu.Lock()
	a.macroState = nil
	a.macroMu.Unlock()
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
	switcher     *switcher.Switcher
	mixer        AudioMixerAPI
	compositor   *graphics.Compositor
	keyer        *graphics.KeyProcessor
	replayMgr    *replay.Manager
	presetStore  *preset.Store
	outputMgr    OutputManagerAPI
	stingerStore *stinger.Store
	scte35       SCTE35API
	captionMgr   CaptionManagerAPI
	clipMgr      *clip.Manager
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
		id := int(floatParam(params, "layerId", 0))
		return t.execGraphics(func(c *graphics.Compositor) error { return c.On(id) })
	case macro.ActionGraphicsOff:
		id := int(floatParam(params, "layerId", 0))
		return t.execGraphics(func(c *graphics.Compositor) error { return c.Off(id) })
	case macro.ActionGraphicsAutoOn:
		id := int(floatParam(params, "layerId", 0))
		dur := time.Duration(floatParam(params, "durationMs", 500)) * time.Millisecond
		return t.execGraphics(func(c *graphics.Compositor) error { return c.AutoOn(id, dur) })
	case macro.ActionGraphicsAutoOff:
		id := int(floatParam(params, "layerId", 0))
		dur := time.Duration(floatParam(params, "durationMs", 500)) * time.Millisecond
		return t.execGraphics(func(c *graphics.Compositor) error { return c.AutoOff(id, dur) })
	case macro.ActionGraphicsAddLayer:
		return t.execGraphics(func(c *graphics.Compositor) error {
			_, err := c.AddLayer()
			return err
		})
	case macro.ActionGraphicsRemoveLayer:
		id := int(floatParam(params, "layerId", 0))
		return t.execGraphics(func(c *graphics.Compositor) error { return c.RemoveLayer(id) })
	case macro.ActionGraphicsSetRect:
		id := int(floatParam(params, "layerId", 0))
		x := int(floatParam(params, "x", 0))
		y := int(floatParam(params, "y", 0))
		w := int(floatParam(params, "width", 0))
		h := int(floatParam(params, "height", 0))
		return t.execGraphics(func(c *graphics.Compositor) error {
			return c.SetLayerRect(id, image.Rect(x, y, x+w, y+h))
		})
	case macro.ActionGraphicsSetZOrder:
		id := int(floatParam(params, "layerId", 0))
		z := int(floatParam(params, "zOrder", 0))
		return t.execGraphics(func(c *graphics.Compositor) error { return c.SetLayerZOrder(id, z) })
	case macro.ActionGraphicsFlyIn:
		id := int(floatParam(params, "layerId", 0))
		dir, _ := params["direction"].(string)
		dur := int(floatParam(params, "durationMs", 500))
		return t.execGraphics(func(c *graphics.Compositor) error { return c.FlyIn(id, dir, dur) })
	case macro.ActionGraphicsFlyOut:
		id := int(floatParam(params, "layerId", 0))
		dir, _ := params["direction"].(string)
		dur := int(floatParam(params, "durationMs", 500))
		return t.execGraphics(func(c *graphics.Compositor) error { return c.FlyOut(id, dir, dur) })
	case macro.ActionGraphicsSlide:
		id := int(floatParam(params, "layerId", 0))
		x := int(floatParam(params, "x", 0))
		y := int(floatParam(params, "y", 0))
		w := int(floatParam(params, "width", 0))
		h := int(floatParam(params, "height", 0))
		dur := int(floatParam(params, "durationMs", 500))
		return t.execGraphics(func(c *graphics.Compositor) error {
			return c.SlideLayer(id, image.Rect(x, y, x+w, y+h), dur)
		})
	case macro.ActionGraphicsAnimate:
		id := int(floatParam(params, "layerId", 0))
		return t.execGraphicsAnimate(id, params)
	case macro.ActionGraphicsAnimateStop:
		id := int(floatParam(params, "layerId", 0))
		return t.execGraphics(func(c *graphics.Compositor) error { return c.StopAnimation(id) })
	case macro.ActionGraphicsUploadFrame:
		id := int(floatParam(params, "layerId", 0))
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
	if ct, ok := params["commandType"].(string); ok {
		switch ct {
		case "splice_insert":
			msg.CommandType = scte35.CommandSpliceInsert
		case "time_signal":
			msg.CommandType = scte35.CommandTimeSignal
		default:
			return 0, fmt.Errorf("invalid commandType %q", ct)
		}
	} else {
		msg.CommandType = scte35.CommandSpliceInsert
	}
	if v, ok := params["isOut"].(bool); ok {
		msg.IsOut = v
	}
	if v, ok := params["autoReturn"].(bool); ok {
		msg.AutoReturn = v
	}
	if v, ok := params["durationMs"].(float64); ok {
		d := time.Duration(int64(v)) * time.Millisecond
		msg.BreakDuration = &d
	}
	if v, ok := params["eventId"].(float64); ok {
		msg.EventID = uint32(v)
	}
	if v, ok := params["uniqueProgramId"].(float64); ok {
		msg.UniqueProgramID = uint16(v)
	}
	if v, ok := params["availNum"].(float64); ok {
		msg.AvailNum = uint8(v)
	}
	if v, ok := params["availsExpected"].(float64); ok {
		msg.AvailsExpected = uint8(v)
	}
	// Parse descriptors for time_signal commands.
	if descs, ok := params["descriptors"].([]any); ok {
		for _, raw := range descs {
			dm, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			desc := scte35.SegmentationDescriptor{}
			if v, ok := dm["segmentationType"].(float64); ok {
				desc.SegmentationType = uint8(v)
			}
			if v, ok := dm["segEventId"].(float64); ok {
				desc.SegEventID = uint32(v)
			}
			if v, ok := dm["upidType"].(float64); ok {
				desc.UPIDType = uint8(v)
			}
			if v, ok := dm["upid"].(string); ok {
				desc.UPID = []byte(v)
			}
			if v, ok := dm["durationMs"].(float64); ok && v > 0 {
				ticks := uint64(v) * 90
				desc.DurationTicks = &ticks
			}
			msg.Descriptors = append(msg.Descriptors, desc)
		}
	}
	msg.Source = "macro"
	if v, ok := params["preRollMs"].(float64); ok && v > 0 {
		return t.scte35.ScheduleCue(msg, int64(v))
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
	source, _ := params["source"].(string)
	muted := true
	if v, ok := params["muted"].(bool); ok {
		muted = v
	}
	return t.mixer.SetMuted(source, muted)
}

func (t *apiMacroTarget) execAudioAFV(params map[string]any) error {
	if t.mixer == nil {
		return nil
	}
	source, _ := params["source"].(string)
	afv := true
	if v, ok := params["afv"].(bool); ok {
		afv = v
	}
	return t.mixer.SetAFV(source, afv)
}

func (t *apiMacroTarget) execAudioTrim(params map[string]any) error {
	if t.mixer == nil {
		return nil
	}
	source, _ := params["source"].(string)
	trim := floatParam(params, "trim", 0)
	return t.mixer.SetTrim(source, trim)
}

func (t *apiMacroTarget) execAudioMaster(params map[string]any) error {
	if t.mixer == nil {
		return nil
	}
	level := floatParam(params, "level", 0)
	t.mixer.SetMasterLevel(level)
	return nil
}

func (t *apiMacroTarget) execAudioEQ(params map[string]any) error {
	if t.mixer == nil {
		return nil
	}
	source, _ := params["source"].(string)
	band := int(floatParam(params, "band", 0))
	freq := floatParam(params, "frequency", 1000)
	gain := floatParam(params, "gain", 0)
	q := floatParam(params, "q", 1.0)
	enabled := true
	if v, ok := params["enabled"].(bool); ok {
		enabled = v
	}
	return t.mixer.SetEQ(source, band, freq, gain, q, enabled)
}

func (t *apiMacroTarget) execAudioCompressor(params map[string]any) error {
	if t.mixer == nil {
		return nil
	}
	source, _ := params["source"].(string)
	threshold := floatParam(params, "threshold", -20)
	ratio := floatParam(params, "ratio", 4)
	attack := floatParam(params, "attack", 10)
	release := floatParam(params, "release", 100)
	makeupGain := floatParam(params, "makeupGain", 0)
	return t.mixer.SetCompressor(source, threshold, ratio, attack, release, makeupGain)
}

func (t *apiMacroTarget) execAudioDelay(params map[string]any) error {
	if t.mixer == nil {
		return nil
	}
	source, _ := params["source"].(string)
	delayMs := int(floatParam(params, "delayMs", 0))
	return t.mixer.SetAudioDelay(source, delayMs)
}

// --- Graphics helpers ---

func (t *apiMacroTarget) execGraphics(fn func(*graphics.Compositor) error) error {
	if t.compositor == nil {
		return nil
	}
	return fn(t.compositor)
}

func (t *apiMacroTarget) execGraphicsAnimate(id int, params map[string]any) error {
	if t.compositor == nil {
		return nil
	}
	mode, _ := params["mode"].(string)
	cfg := graphics.AnimationConfig{
		Mode:     mode,
		MinAlpha: floatParam(params, "minAlpha", 0),
		MaxAlpha: floatParam(params, "maxAlpha", 1),
		SpeedHz:  floatParam(params, "speedHz", 1),
	}
	if mode == "transition" {
		if x, ok := params["toX"].(float64); ok {
			y := floatParam(params, "toY", 0)
			w := floatParam(params, "toWidth", 0)
			h := floatParam(params, "toHeight", 0)
			cfg.ToRect = &graphics.RectState{X: int(x), Y: int(y), Width: int(w), Height: int(h)}
		}
		if a, ok := params["toAlpha"].(float64); ok {
			cfg.ToAlpha = &a
		}
		cfg.DurationMs = int(floatParam(params, "durationMs", 500))
		easing, _ := params["easing"].(string)
		cfg.Easing = easing
	}
	return t.compositor.Animate(id, cfg)
}

func (t *apiMacroTarget) execGraphicsUploadFrame(id int, params map[string]any) error {
	if t.compositor == nil {
		return nil
	}
	w := int(floatParam(params, "width", 0))
	h := int(floatParam(params, "height", 0))
	tmpl, _ := params["template"].(string)
	rgbaB64, _ := params["rgba"].(string)
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
	cfg := output.RecorderConfig{}
	if dir, ok := params["directory"].(string); ok {
		cfg.Dir = dir
	}
	return t.outputMgr.StartRecording(cfg)
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
	id, _ := params["id"].(string)
	if id == "" {
		// Also accept "name" for backward compat.
		id, _ = params["name"].(string)
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

func (rt *macroPresetRecallTarget) SetMasterLevel(level float64) {
	if rt.mixer == nil {
		return
	}
	rt.mixer.SetMasterLevel(level)
}

// --- Key helpers ---

func (t *apiMacroTarget) execKeySet(params map[string]any) error {
	if t.keyer == nil {
		return nil
	}
	source, _ := params["source"].(string)
	config := graphics.KeyConfig{}
	if configMap, ok := params["config"].(map[string]any); ok {
		data, err := json.Marshal(configMap)
		if err != nil {
			return fmt.Errorf("marshal key config: %w", err)
		}
		if err := json.Unmarshal(data, &config); err != nil {
			return fmt.Errorf("parse key config: %w", err)
		}
	}
	t.keyer.SetKey(source, config)
	return nil
}

func (t *apiMacroTarget) execKeyDelete(params map[string]any) error {
	if t.keyer == nil {
		return nil
	}
	source, _ := params["source"].(string)
	t.keyer.RemoveKey(source)
	return nil
}

// --- Source helpers ---

func (t *apiMacroTarget) execSourceLabel(ctx context.Context, params map[string]any) error {
	source, _ := params["source"].(string)
	label, _ := params["label"].(string)
	return t.switcher.SetLabel(ctx, source, label)
}

func (t *apiMacroTarget) execSourceDelay(params map[string]any) error {
	source, _ := params["source"].(string)
	delayMs := int(floatParam(params, "delayMs", 0))
	return t.switcher.SetSourceDelay(source, delayMs)
}

func (t *apiMacroTarget) execSourcePosition(params map[string]any) error {
	source, _ := params["source"].(string)
	pos := int(floatParam(params, "position", 1))
	return t.switcher.SetSourcePosition(source, pos)
}

// --- Replay helpers (mark-based) ---

func (t *apiMacroTarget) execReplayMarkIn(params map[string]any) error {
	if t.replayMgr == nil {
		return nil
	}
	source, _ := params["source"].(string)
	return t.replayMgr.MarkIn(source)
}

func (t *apiMacroTarget) execReplayMarkOut(params map[string]any) error {
	if t.replayMgr == nil {
		return nil
	}
	source, _ := params["source"].(string)
	return t.replayMgr.MarkOut(source)
}

func (t *apiMacroTarget) execReplayPlay(params map[string]any) error {
	if t.replayMgr == nil {
		return nil
	}
	source, _ := params["source"].(string)
	speed := floatParam(params, "speed", 0.5)
	loop := false
	if v, ok := params["loop"].(bool); ok {
		loop = v
	}
	return t.replayMgr.Play(source, speed, loop)
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
	source, _ := params["source"].(string)
	speed := floatParam(params, "speed", 0.5)
	if err := t.replayMgr.MarkIn(source); err != nil {
		return err
	}
	if err := t.replayMgr.MarkOut(source); err != nil {
		return err
	}
	return t.replayMgr.Play(source, speed, false)
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
	mode, _ := params["mode"].(string)
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
	text, _ := params["text"].(string)
	if text != "" {
		t.captionMgr.IngestText(text)
	}
	if newline, ok := params["newline"].(bool); ok && newline {
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
	playerID := int(floatParam(params, "playerId", 1))
	clipID, _ := params["clipId"].(string)
	if clipID == "" {
		return fmt.Errorf("clip_load requires 'clipId' param")
	}
	return t.clipMgr.Load(playerID, clipID)
}

func (t *apiMacroTarget) execClipPlay(params map[string]interface{}) error {
	if t.clipMgr == nil {
		return fmt.Errorf("clips not enabled")
	}
	playerID := int(floatParam(params, "playerId", 1))
	speed := floatParam(params, "speed", 1.0)
	loop := false
	if v, ok := params["loop"].(bool); ok {
		loop = v
	}
	return t.clipMgr.Play(playerID, speed, loop)
}

func (t *apiMacroTarget) execClipPause(params map[string]interface{}) error {
	if t.clipMgr == nil {
		return fmt.Errorf("clips not enabled")
	}
	playerID := int(floatParam(params, "playerId", 1))
	return t.clipMgr.Pause(playerID)
}

func (t *apiMacroTarget) execClipStop(params map[string]interface{}) error {
	if t.clipMgr == nil {
		return fmt.Errorf("clips not enabled")
	}
	playerID := int(floatParam(params, "playerId", 1))
	return t.clipMgr.Stop(playerID)
}

func (t *apiMacroTarget) execClipEject(params map[string]interface{}) error {
	if t.clipMgr == nil {
		return fmt.Errorf("clips not enabled")
	}
	playerID := int(floatParam(params, "playerId", 1))
	return t.clipMgr.Eject(playerID)
}

func (t *apiMacroTarget) execClipSeek(params map[string]interface{}) error {
	if t.clipMgr == nil {
		return fmt.Errorf("clips not enabled")
	}
	playerID := int(floatParam(params, "playerId", 1))
	position := floatParam(params, "position", 0)
	return t.clipMgr.Seek(playerID, position)
}

// --- Param helpers ---

func floatParam(params map[string]any, key string, defaultVal float64) float64 {
	if v, ok := params[key].(float64); ok {
		return v
	}
	return defaultVal
}
