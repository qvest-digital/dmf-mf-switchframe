package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"

	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/cmdqueue"
	"github.com/zsiec/switchframe/server/dve"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/stinger"
	"github.com/zsiec/switchframe/server/switcher"
	"github.com/zsiec/switchframe/server/transition"
	"github.com/zsiec/switchframe/server/transition/wipemap"
)

// initCommandQueue creates the PTP-timed command queue, wires a dispatcher
// with all state-mutating actions, and connects the queue to the switcher's
// frame-loop drain. When a state directory is available, a WAL is attached
// for crash recovery.
func (a *App) initCommandQueue() error {
	var opts []cmdqueue.Option

	// Default staleness threshold is 2s. When active-active peer engine
	// configuration is added (future), increase to 10s to accommodate
	// cross-region command propagation latency.
	maxAge := cmdqueue.DefaultMaxAge // 2s
	opts = append(opts, cmdqueue.WithMaxAge(maxAge))

	// Cap queue size to prevent unbounded growth from misbehaving clients.
	opts = append(opts, cmdqueue.WithMaxSize(10_000))

	// Wire Prometheus metrics callbacks when metrics are available.
	if a.appMetrics != nil {
		opts = append(opts, cmdqueue.WithMetrics(&cmdqueue.MetricsCallbacks{
			OnDrain: func(count int) {
				a.appMetrics.CmdQueueDrainedTotal.Add(float64(count))
			},
			OnLate: func() {
				a.appMetrics.CmdQueueLateTotal.Inc()
			},
			OnStale: func() {
				a.appMetrics.CmdQueueStaleTotal.Inc()
			},
			OnDup: func() {
				a.appMetrics.CmdQueueDuplicateTotal.Inc()
			},
			OnClockJump: func() {
				a.appMetrics.CmdQueueClockJumpsTotal.Inc()
			},
		}))
	}

	// Attach WAL for crash recovery if we have a state directory.
	if a.cfg.StateDir != "" {
		walPath := filepath.Join(a.cfg.StateDir, "cmdqueue.wal")
		wal, err := cmdqueue.NewWAL(walPath)
		if err != nil {
			slog.Warn("command queue WAL init failed, proceeding without WAL", "error", err, "path", walPath)
		} else {
			a.cmdQueueWAL = wal
			opts = append(opts, cmdqueue.WithWAL(wal))
			slog.Info("command queue WAL initialized", "path", walPath)
		}
	}

	a.cmdQueue = cmdqueue.New(clock.Now, opts...)

	// Build dispatcher with all state-mutating actions matching the
	// timedHandler/timedHandlerWithPath registrations in control/*.go.
	dispatcher := cmdqueue.NewDispatcher()
	a.registerCoreActions(dispatcher)
	a.registerTransitionActions(dispatcher)
	a.registerAudioActions(dispatcher)
	a.registerGraphicsActions(dispatcher)
	a.registerKeyActions(dispatcher)
	a.registerDVEActions(dispatcher)
	a.registerSourceActions(dispatcher)
	a.registerFormatActions(dispatcher)
	a.registerEncoderActions(dispatcher)
	a.cmdDispatcher = dispatcher

	// Wire into the switcher's frame-loop drain so queued commands execute
	// with frame-level accuracy (~33ms at 30fps).
	a.sw.SetCommandQueue(a.cmdQueue, a.cmdDispatcher)

	// Broadcast state after each dispatched command so browsers see changes
	// from PTP-timed commands immediately. Also update the depth gauge.
	a.sw.SetOnCommandExecuted(func() {
		if a.appMetrics != nil {
			a.appMetrics.CmdQueueDepth.Set(float64(a.cmdQueue.Pending()))
		}
		a.broadcastState(nil)
	})

	slog.Info("command queue initialized",
		"max_age_us", maxAge,
		"wal", a.cmdQueueWAL != nil)

	return nil
}

// registerCoreActions registers cut and preview dispatch actions.
func (a *App) registerCoreActions(d *cmdqueue.Dispatcher) {
	d.Register("cut", func(payload json.RawMessage) error {
		var req struct {
			Source string `json:"source"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return err
		}
		return a.sw.Cut(context.Background(), req.Source)
	})
	d.Register("preview", func(payload json.RawMessage) error {
		var req struct {
			Source string `json:"source"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return err
		}
		return a.sw.SetPreview(context.Background(), req.Source)
	})
}

// registerTransitionActions registers transition, transition_position, and ftb dispatch actions.
func (a *App) registerTransitionActions(d *cmdqueue.Dispatcher) {
	d.Register("transition", func(payload json.RawMessage) error {
		var req struct {
			Source        string                   `json:"source"`
			Type          string                   `json:"type"`
			DurationMs    int                      `json:"durationMs"`
			WipeDirection string                   `json:"wipeDirection,omitempty"`
			WipeConfig    *wipemap.WipeConfig      `json:"wipeConfig,omitempty"`
			StingerName   string                   `json:"stingerName,omitempty"`
			Easing        *transition.EasingConfig `json:"easing,omitempty"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return err
		}

		var opts []switcher.TransitionOption
		if req.Easing != nil {
			et := transition.EasingType(req.Easing.Type)
			var ec *transition.EasingCurve
			if et == transition.EasingCustom {
				var err error
				ec, err = transition.NewCustomEasingCurve(req.Easing.X1, req.Easing.Y1, req.Easing.X2, req.Easing.Y2)
				if err != nil {
					return err
				}
			} else {
				ec = transition.NewEasingCurve(et)
			}
			opts = append(opts, switcher.WithEasing(ec))
		}
		if req.Type == "stinger" && a.stingerStore != nil {
			clip, ok := a.stingerStore.Get(req.StingerName)
			if !ok {
				return fmt.Errorf("stinger clip not found: %s", req.StingerName)
			}
			sd := stingerClipToData(clip)
			opts = append(opts, switcher.WithStingerData(sd))
		}
		if req.WipeConfig != nil {
			opts = append(opts, switcher.WithWipeConfig(req.WipeConfig))
		}

		return a.sw.StartTransition(context.Background(), req.Source, req.Type, req.DurationMs, req.WipeDirection, opts...)
	})

	d.Register("transition_position", func(payload json.RawMessage) error {
		var req struct {
			Position float64 `json:"position"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return err
		}
		return a.sw.SetTransitionPosition(context.Background(), req.Position)
	})

	d.Register("ftb", func(payload json.RawMessage) error {
		return a.sw.FadeToBlack(context.Background())
	})

	d.Register("ftb_duration_set", func(payload json.RawMessage) error {
		var req struct {
			DurationMs int `json:"durationMs"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return err
		}
		return a.sw.SetFTBDuration(req.DurationMs)
	})
}

// registerAudioActions registers all audio dispatch actions.
func (a *App) registerAudioActions(d *cmdqueue.Dispatcher) {
	d.Register("audio_trim", func(payload json.RawMessage) error {
		if a.mixer == nil {
			return nil
		}
		var req struct {
			Source string  `json:"source"`
			Trim   float64 `json:"trim"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return err
		}
		return a.mixer.SetTrim(req.Source, req.Trim)
	})

	d.Register("audio_level", func(payload json.RawMessage) error {
		if a.mixer == nil {
			return nil
		}
		var req struct {
			Source string  `json:"source"`
			Level  float64 `json:"level"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return err
		}
		return a.mixer.SetLevel(req.Source, req.Level)
	})

	d.Register("audio_mute", func(payload json.RawMessage) error {
		if a.mixer == nil {
			return nil
		}
		var req struct {
			Source string `json:"source"`
			Muted  bool   `json:"muted"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return err
		}
		return a.mixer.SetMuted(req.Source, req.Muted)
	})

	d.Register("audio_afv", func(payload json.RawMessage) error {
		if a.mixer == nil {
			return nil
		}
		var req struct {
			Source string `json:"source"`
			AFV    bool   `json:"afv"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return err
		}
		return a.mixer.SetAFV(req.Source, req.AFV)
	})

	d.Register("audio_phase", func(payload json.RawMessage) error {
		if a.mixer == nil {
			return nil
		}
		var req struct {
			Source string `json:"source"`
			Invert bool   `json:"invert"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return err
		}
		return a.mixer.SetPhaseInvert(req.Source, req.Invert)
	})

	d.Register("audio_balance", func(payload json.RawMessage) error {
		if a.mixer == nil {
			return nil
		}
		var req struct {
			Source  string  `json:"source"`
			Balance float64 `json:"balance"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return err
		}
		return a.mixer.SetBalance(req.Source, req.Balance)
	})

	d.Register("audio_master", func(payload json.RawMessage) error {
		if a.mixer == nil {
			return nil
		}
		var req struct {
			Level float64 `json:"level"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return err
		}
		return a.mixer.SetMasterLevel(req.Level)
	})

	// Path-based audio actions use _path_source injected by timedHandlerWithPath.
	d.Register("audio_eq", func(payload json.RawMessage) error {
		if a.mixer == nil {
			return nil
		}
		var req struct {
			PathSource string  `json:"_path_source"`
			Band       int     `json:"band"`
			Frequency  float64 `json:"frequency"`
			Gain       float64 `json:"gain"`
			Q          float64 `json:"q"`
			Enabled    bool    `json:"enabled"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return err
		}
		return a.mixer.SetEQ(req.PathSource, req.Band, req.Frequency, req.Gain, req.Q, req.Enabled)
	})

	d.Register("audio_compressor", func(payload json.RawMessage) error {
		if a.mixer == nil {
			return nil
		}
		var req struct {
			PathSource string  `json:"_path_source"`
			Threshold  float64 `json:"threshold"`
			Ratio      float64 `json:"ratio"`
			Attack     float64 `json:"attack"`
			Release    float64 `json:"release"`
			MakeupGain float64 `json:"makeupGain"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return err
		}
		return a.mixer.SetCompressor(req.PathSource, req.Threshold, req.Ratio, req.Attack, req.Release, req.MakeupGain)
	})

	d.Register("audio_hpf", func(payload json.RawMessage) error {
		if a.mixer == nil {
			return nil
		}
		var req struct {
			PathSource string  `json:"_path_source"`
			Frequency  float64 `json:"frequency"`
			Enabled    bool    `json:"enabled"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return err
		}
		return a.mixer.SetHPF(req.PathSource, req.Frequency, req.Enabled)
	})

	d.Register("audio_gate", func(payload json.RawMessage) error {
		if a.mixer == nil {
			return nil
		}
		var req struct {
			PathSource string  `json:"_path_source"`
			Threshold  float64 `json:"threshold"`
			Range      float64 `json:"range"`
			Attack     float64 `json:"attack"`
			Release    float64 `json:"release"`
			Hold       float64 `json:"hold"`
			Enabled    bool    `json:"enabled"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return err
		}
		return a.mixer.SetGate(req.PathSource, req.Threshold, req.Range, req.Attack, req.Release, req.Hold, req.Enabled)
	})

	d.Register("audio_delay", func(payload json.RawMessage) error {
		if a.mixer == nil {
			return nil
		}
		var req struct {
			PathSource string `json:"_path_source"`
			DelayMs    int    `json:"delayMs"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return err
		}
		return a.mixer.SetAudioDelay(req.PathSource, req.DelayMs)
	})

	d.Register("audio_limiter", func(payload json.RawMessage) error {
		if a.mixer == nil {
			return nil
		}
		var req struct {
			Threshold float64 `json:"threshold"`
			Ceiling   float64 `json:"ceiling"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return err
		}
		if err := a.mixer.SetLimiterThreshold(req.Threshold); err != nil {
			return err
		}
		return a.mixer.SetLimiterCeiling(req.Ceiling)
	})
}

// registerGraphicsActions registers graphics on/off dispatch actions.
func (a *App) registerGraphicsActions(d *cmdqueue.Dispatcher) {
	d.Register("graphics_on", func(payload json.RawMessage) error {
		if a.compositor == nil {
			return nil
		}
		var req struct {
			PathID string `json:"_path_id"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return err
		}
		id, err := strconv.Atoi(req.PathID)
		if err != nil {
			return fmt.Errorf("invalid layer id: %w", err)
		}
		return a.compositor.On(id)
	})

	d.Register("graphics_off", func(payload json.RawMessage) error {
		if a.compositor == nil {
			return nil
		}
		var req struct {
			PathID string `json:"_path_id"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return err
		}
		id, err := strconv.Atoi(req.PathID)
		if err != nil {
			return fmt.Errorf("invalid layer id: %w", err)
		}
		return a.compositor.Off(id)
	})

	d.Register("dsk_fade_duration_set", func(payload json.RawMessage) error {
		if a.compositor == nil {
			return nil
		}
		var req struct {
			DurationMs int `json:"durationMs"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return err
		}
		return a.compositor.SetDSKFadeDuration(req.DurationMs)
	})
}

// registerKeyActions registers upstream key set/delete dispatch actions.
func (a *App) registerKeyActions(d *cmdqueue.Dispatcher) {
	d.Register("key_set", func(payload json.RawMessage) error {
		if a.keyProcessor == nil {
			return nil
		}
		var req struct {
			PathSource string `json:"_path_source"`
			graphics.KeyConfig
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return err
		}
		a.keyProcessor.SetKey(req.PathSource, req.KeyConfig)
		return nil
	})

	d.Register("key_delete", func(payload json.RawMessage) error {
		if a.keyProcessor == nil {
			return nil
		}
		var req struct {
			PathSource string `json:"_path_source"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return err
		}
		a.keyProcessor.RemoveKey(req.PathSource)
		if a.keyBridge != nil {
			a.keyBridge.RemoveFillSource(req.PathSource)
		}
		return nil
	})
}

// registerDVEActions registers DVE layout, slot, and preset dispatch actions.
func (a *App) registerDVEActions(d *cmdqueue.Dispatcher) {
	d.Register("dve_set", func(payload json.RawMessage) error {
		if a.dveCompositor == nil {
			return nil
		}
		var req struct {
			Preset string     `json:"preset,omitempty"`
			Name   string     `json:"name,omitempty"`
			Slots  []dve.Slot `json:"slots,omitempty"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return err
		}

		format := a.sw.PipelineFormat()
		var dl *dve.Layout
		if req.Preset != "" {
			dl = dveLayoutFromPreset(dve.ResolveBuiltinPreset(req.Preset, format.Width, format.Height))
			if dl == nil && a.dvePresetStore != nil {
				stored, err := a.dvePresetStore.Load(req.Preset)
				if err == nil {
					dl = dveLayoutFromPreset(stored)
				}
			}
			if dl == nil {
				return fmt.Errorf("preset not found: %s", req.Preset)
			}
		} else if len(req.Slots) > 0 {
			name := req.Name
			if name == "" {
				name = "custom"
			}
			dl = &dve.Layout{Name: name, Slots: req.Slots}
		} else {
			return fmt.Errorf("provide preset name or slots")
		}
		if err := dve.ValidateLayout(dl); err != nil {
			return err
		}
		a.dveCompositor.SetLayout(dl)
		return nil
	})

	d.Register("dve_slot_update", func(payload json.RawMessage) error {
		if a.dveCompositor == nil {
			return nil
		}
		var req struct {
			PathID     string         `json:"_path_id"`
			SourceKey  *string        `json:"sourceKey,omitempty"`
			Transform  *dve.Transform `json:"transform,omitempty"`
			ZOrder     *int           `json:"zOrder,omitempty"`
			Enabled    *bool          `json:"enabled,omitempty"`
			ScaleMode  *string        `json:"scaleMode,omitempty"`
			CropAnchor *[2]float64    `json:"cropAnchor,omitempty"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return err
		}
		id, err := strconv.Atoi(req.PathID)
		if err != nil {
			return fmt.Errorf("invalid slot id: %w", err)
		}
		a.dveCompositor.UpdateSlot(id, func(s *dve.Slot) {
			if req.SourceKey != nil {
				s.SourceKey = *req.SourceKey
			}
			if req.Transform != nil {
				s.Transform = *req.Transform
			}
			if req.ZOrder != nil {
				s.ZOrder = *req.ZOrder
			}
			if req.Enabled != nil {
				s.Enabled = *req.Enabled
			}
			if req.ScaleMode != nil {
				s.Transform.ScaleMode = *req.ScaleMode
			}
			if req.CropAnchor != nil {
				s.Transform.CropAnchor = *req.CropAnchor
			}
		})
		return nil
	})

	d.Register("dve_slot_on", func(payload json.RawMessage) error {
		if a.dveCompositor == nil {
			return nil
		}
		var req struct {
			PathID string `json:"_path_id"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return err
		}
		id, err := strconv.Atoi(req.PathID)
		if err != nil {
			return fmt.Errorf("invalid slot id: %w", err)
		}
		a.dveCompositor.SlotOn(id)
		return nil
	})

	d.Register("dve_slot_off", func(payload json.RawMessage) error {
		if a.dveCompositor == nil {
			return nil
		}
		var req struct {
			PathID string `json:"_path_id"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return err
		}
		id, err := strconv.Atoi(req.PathID)
		if err != nil {
			return fmt.Errorf("invalid slot id: %w", err)
		}
		a.dveCompositor.SlotOff(id)
		return nil
	})

	d.Register("dve_preset_recall", func(payload json.RawMessage) error {
		if a.dveCompositor == nil {
			return nil
		}
		var req struct {
			PathID string `json:"_path_id"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return err
		}
		format := a.sw.PipelineFormat()
		preset := dve.ResolveBuiltinPreset(req.PathID, format.Width, format.Height)
		if preset == nil && a.dvePresetStore != nil {
			stored, err := a.dvePresetStore.Load(req.PathID)
			if err == nil {
				preset = stored
			}
		}
		if preset == nil {
			return fmt.Errorf("preset not found: %s", req.PathID)
		}
		a.dveCompositor.SetLayout(dveLayoutFromPreset(preset))
		return nil
	})
}

// registerSourceActions registers source label, delay, and position dispatch actions.
func (a *App) registerSourceActions(d *cmdqueue.Dispatcher) {
	d.Register("source_label", func(payload json.RawMessage) error {
		var req struct {
			PathKey string `json:"_path_key"`
			Label   string `json:"label"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return err
		}
		return a.sw.SetLabel(context.Background(), req.PathKey, req.Label)
	})

	d.Register("source_delay", func(payload json.RawMessage) error {
		var req struct {
			PathKey string `json:"_path_key"`
			DelayMs int    `json:"delayMs"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return err
		}
		return a.sw.SetSourceDelay(req.PathKey, req.DelayMs)
	})

	d.Register("source_position", func(payload json.RawMessage) error {
		var req struct {
			PathKey  string `json:"_path_key"`
			Position int    `json:"position"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return err
		}
		return a.sw.SetSourcePosition(req.PathKey, req.Position)
	})
}

// registerFormatActions registers the format_set dispatch action.
func (a *App) registerFormatActions(d *cmdqueue.Dispatcher) {
	d.Register("format_set", func(payload json.RawMessage) error {
		var req struct {
			Format string `json:"format,omitempty"`
			Width  int    `json:"width,omitempty"`
			Height int    `json:"height,omitempty"`
			FPSNum int    `json:"fpsNum,omitempty"`
			FPSDen int    `json:"fpsDen,omitempty"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return err
		}

		var f switcher.PipelineFormat
		switch {
		case req.Format != "":
			preset, ok := switcher.FormatPresets[req.Format]
			if !ok {
				return fmt.Errorf("unknown format preset: %s", req.Format)
			}
			f = preset
		case req.Width > 0 || req.Height > 0 || req.FPSNum > 0 || req.FPSDen > 0:
			f = switcher.PipelineFormat{
				Width:  req.Width,
				Height: req.Height,
				FPSNum: req.FPSNum,
				FPSDen: req.FPSDen,
			}
		default:
			return fmt.Errorf("provide format preset or width/height/fpsNum/fpsDen")
		}

		if err := a.sw.SetPipelineFormat(f); err != nil {
			return err
		}
		if a.compositor != nil {
			a.compositor.SetPipelineFPS(f.FPSNum, f.FPSDen)
		}
		return nil
	})
}

// registerEncoderActions registers the encoder_set dispatch action.
func (a *App) registerEncoderActions(d *cmdqueue.Dispatcher) {
	d.Register("encoder_set", func(payload json.RawMessage) error {
		var req struct {
			Encoder string `json:"encoder"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return err
		}
		return a.sw.SetEncoder(req.Encoder)
	})
}

// dveLayoutFromPreset converts a DVE Preset to a DVE Layout. Returns nil if preset is nil.
// This duplicates the logic from control/api_dve.go for use in the dispatcher
// (which runs without HTTP context).
func dveLayoutFromPreset(p *dve.Preset) *dve.Layout {
	if p == nil {
		return nil
	}
	return &dve.Layout{
		Name:  p.Name,
		Slots: p.Slots,
	}
}

// stingerClipToData converts a stinger.Clip to transition.StingerData.
// This duplicates the logic from control/api_graphics.go for use in the
// dispatcher (which runs without HTTP context).
func stingerClipToData(clip *stinger.Clip) *transition.StingerData {
	frames := make([]transition.StingerFrameData, len(clip.Frames))
	for i, f := range clip.Frames {
		frames[i] = transition.StingerFrameData{
			YUV:   f.YUV,
			Alpha: f.Alpha,
		}
	}
	return &transition.StingerData{
		Frames:          frames,
		Width:           clip.Width,
		Height:          clip.Height,
		CutPoint:        clip.CutPoint,
		Audio:           clip.Audio,
		AudioSampleRate: clip.AudioSampleRate,
		AudioChannels:   clip.AudioChannels,
	}
}
