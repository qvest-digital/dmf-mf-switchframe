package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/zsiec/switchframe/server/control/httperr"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/macro"
	"github.com/zsiec/switchframe/server/output"
	"github.com/zsiec/switchframe/server/preset"
	"github.com/zsiec/switchframe/server/replay"
	"github.com/zsiec/switchframe/server/scte35"
	"github.com/zsiec/switchframe/server/stinger"
	"github.com/zsiec/switchframe/server/switcher"
)

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

// handleRunMacro triggers execution of a macro.
func (a *API) handleRunMacro(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	m, err := a.macroStore.Get(name)
	if err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

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
	}

	if err := macro.Run(r.Context(), m, target); err != nil {
		httperr.WriteErr(w, errorStatus(err), err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// apiMacroTarget adapts the API's subsystems to the macro.MacroTarget
// interface so Run() can execute macro steps without knowing concrete types.
type apiMacroTarget struct {
	switcher     *switcher.Switcher
	mixer        AudioMixerAPI
	compositor   *graphics.Compositor
	keyer        *graphics.KeyProcessor
	replayMgr    *replay.Manager
	presetStore  *preset.PresetStore
	outputMgr    OutputManagerAPI
	stingerStore *stinger.StingerStore
	scte35       SCTE35API
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
func (t *apiMacroTarget) Execute(ctx context.Context, action string, params map[string]interface{}) error {
	switch macro.MacroAction(action) {
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

	// Graphics
	case macro.ActionGraphicsOn:
		return t.execGraphics(func(c *graphics.Compositor) error { return c.On() })
	case macro.ActionGraphicsOff:
		return t.execGraphics(func(c *graphics.Compositor) error { return c.Off() })
	case macro.ActionGraphicsAutoOn:
		dur := time.Duration(floatParam(params, "durationMs", 500)) * time.Millisecond
		return t.execGraphics(func(c *graphics.Compositor) error { return c.AutoOn(dur) })
	case macro.ActionGraphicsAutoOff:
		dur := time.Duration(floatParam(params, "durationMs", 500)) * time.Millisecond
		return t.execGraphics(func(c *graphics.Compositor) error { return c.AutoOff(dur) })

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

	// Replay (clip-based — from replay UX plan)
	case macro.ActionReplayQuickClip:
		return t.execReplayQuickClip(params)
	case macro.ActionReplayPlayLast:
		return t.execReplayPlayLast()
	case macro.ActionReplayPlayClip:
		return t.execReplayPlayClip(params)

	default:
		return fmt.Errorf("unimplemented action %q", action)
	}
}

// SCTE-35 methods — wired to real injector.

func (t *apiMacroTarget) SCTE35Cue(_ context.Context, params map[string]interface{}) (uint32, error) {
	if t.scte35 == nil {
		return 0, fmt.Errorf("scte35 not enabled")
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
	return t.scte35.InjectCue(msg)
}

func (t *apiMacroTarget) SCTE35Return(_ context.Context, eventID uint32) error {
	if t.scte35 == nil {
		return fmt.Errorf("scte35 not enabled")
	}
	return t.scte35.ReturnToProgram(eventID)
}

func (t *apiMacroTarget) SCTE35Cancel(_ context.Context, eventID uint32) error {
	if t.scte35 == nil {
		return fmt.Errorf("scte35 not enabled")
	}
	return t.scte35.CancelEvent(eventID)
}

func (t *apiMacroTarget) SCTE35Hold(_ context.Context, eventID uint32) error {
	if t.scte35 == nil {
		return fmt.Errorf("scte35 not enabled")
	}
	return t.scte35.HoldBreak(eventID)
}

func (t *apiMacroTarget) SCTE35Extend(_ context.Context, eventID uint32, durationMs int64) error {
	if t.scte35 == nil {
		return fmt.Errorf("scte35 not enabled")
	}
	return t.scte35.ExtendBreak(eventID, durationMs)
}

// --- Audio helpers ---

func (t *apiMacroTarget) execAudioMute(params map[string]interface{}) error {
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

func (t *apiMacroTarget) execAudioAFV(params map[string]interface{}) error {
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

func (t *apiMacroTarget) execAudioTrim(params map[string]interface{}) error {
	if t.mixer == nil {
		return nil
	}
	source, _ := params["source"].(string)
	trim := floatParam(params, "trim", 0)
	return t.mixer.SetTrim(source, trim)
}

func (t *apiMacroTarget) execAudioMaster(params map[string]interface{}) error {
	if t.mixer == nil {
		return nil
	}
	level := floatParam(params, "level", 0)
	t.mixer.SetMasterLevel(level)
	return nil
}

func (t *apiMacroTarget) execAudioEQ(params map[string]interface{}) error {
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

func (t *apiMacroTarget) execAudioCompressor(params map[string]interface{}) error {
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

func (t *apiMacroTarget) execAudioDelay(params map[string]interface{}) error {
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

// --- Output helpers ---

func (t *apiMacroTarget) execRecordingStart(params map[string]interface{}) error {
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

func (t *apiMacroTarget) execPresetRecall(ctx context.Context, params map[string]interface{}) error {
	if t.presetStore == nil {
		return nil
	}
	id, _ := params["id"].(string)
	if id == "" {
		// Also accept "name" for backward compat.
		id, _ = params["name"].(string)
	}
	if id == "" {
		return fmt.Errorf("preset_recall requires 'id' param")
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

func (t *apiMacroTarget) execKeySet(params map[string]interface{}) error {
	if t.keyer == nil {
		return nil
	}
	source, _ := params["source"].(string)
	config := graphics.KeyConfig{}
	if configMap, ok := params["config"].(map[string]interface{}); ok {
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

func (t *apiMacroTarget) execKeyDelete(params map[string]interface{}) error {
	if t.keyer == nil {
		return nil
	}
	source, _ := params["source"].(string)
	t.keyer.RemoveKey(source)
	return nil
}

// --- Source helpers ---

func (t *apiMacroTarget) execSourceLabel(ctx context.Context, params map[string]interface{}) error {
	source, _ := params["source"].(string)
	label, _ := params["label"].(string)
	return t.switcher.SetLabel(ctx, source, label)
}

func (t *apiMacroTarget) execSourceDelay(params map[string]interface{}) error {
	source, _ := params["source"].(string)
	delayMs := int(floatParam(params, "delayMs", 0))
	return t.switcher.SetSourceDelay(source, delayMs)
}

func (t *apiMacroTarget) execSourcePosition(params map[string]interface{}) error {
	source, _ := params["source"].(string)
	pos := int(floatParam(params, "position", 1))
	return t.switcher.SetSourcePosition(source, pos)
}

// --- Replay helpers (mark-based) ---

func (t *apiMacroTarget) execReplayMarkIn(params map[string]interface{}) error {
	if t.replayMgr == nil {
		return nil
	}
	source, _ := params["source"].(string)
	return t.replayMgr.MarkIn(source)
}

func (t *apiMacroTarget) execReplayMarkOut(params map[string]interface{}) error {
	if t.replayMgr == nil {
		return nil
	}
	source, _ := params["source"].(string)
	return t.replayMgr.MarkOut(source)
}

func (t *apiMacroTarget) execReplayPlay(params map[string]interface{}) error {
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

func (t *apiMacroTarget) execReplayQuickClip(params map[string]interface{}) error {
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

func (t *apiMacroTarget) execReplayPlayClip(params map[string]interface{}) error {
	if t.replayMgr == nil {
		return nil
	}
	// Will call replayMgr.PlayClip(clipId) once the replay UX plan lands.
	_ = params["clipId"]
	return nil
}

// --- Param helpers ---

func floatParam(params map[string]interface{}, key string, defaultVal float64) float64 {
	if v, ok := params[key].(float64); ok {
		return v
	}
	return defaultVal
}
