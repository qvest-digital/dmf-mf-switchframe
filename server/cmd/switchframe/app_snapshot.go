package main

import (
	"context"
	"fmt"
	"image"
	"log/slog"

	"github.com/zsiec/switchframe/server/caption"
	"github.com/zsiec/switchframe/server/control"
	"github.com/zsiec/switchframe/server/dve"
	"github.com/zsiec/switchframe/server/graphics"
	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/output"
	"github.com/zsiec/switchframe/server/replay"
)

// snapshotProviderAdapter bridges the App to the control.SnapshotProvider interface.
type snapshotProviderAdapter struct {
	app *App
}

func (sp *snapshotProviderAdapter) CaptureFullSnapshot() internal.FullStateSnapshot {
	return sp.app.captureFullSnapshot()
}

func (sp *snapshotProviderAdapter) ApplyTarget() internal.SnapshotApplyTarget {
	return sp.app
}

// Verify snapshotProviderAdapter satisfies the interface at compile time.
var _ control.SnapshotProvider = (*snapshotProviderAdapter)(nil)

// Verify App satisfies SnapshotApplyTarget at compile time.
var _ internal.SnapshotApplyTarget = (*App)(nil)

// Verify App satisfies all optional snapshot applier interfaces at compile time.
var _ internal.SnapshotClipApplier = (*App)(nil)
var _ internal.SnapshotRecordingApplier = (*App)(nil)
var _ internal.SnapshotDestinationApplier = (*App)(nil)
var _ internal.SnapshotReplayApplier = (*App)(nil)
var _ internal.SnapshotOperatorApplier = (*App)(nil)
var _ internal.SnapshotPlayoutApplier = (*App)(nil)
var _ internal.SnapshotSCTE35Applier = (*App)(nil)
var _ internal.SnapshotMacroApplier = (*App)(nil)
var _ internal.SnapshotEncoderApplier = (*App)(nil)

// --- SnapshotApplyTarget implementation ---

// Cut switches the program source.
func (a *App) Cut(ctx context.Context, source string) error {
	return a.sw.Cut(ctx, source)
}

// SetPreview sets the preview source.
func (a *App) SetPreview(ctx context.Context, source string) error {
	return a.sw.SetPreview(ctx, source)
}

// SetTransitionConfig stores the transition type, duration, and easing.
// These values are used by the next transition start. Since the switcher does
// not have a persistent "default transition" setter (the type/duration are only
// active during a transition), this is a best-effort no-op that logs the config
// for reconciliation but does not trigger a transition.
func (a *App) SetTransitionConfig(transitionType string, durationMs int, easing string) error {
	// The transition type/duration/easing are parameters passed to StartTransition
	// rather than stored defaults. During snapshot reconciliation, capturing
	// the "last used" transition type is informational — the real state is
	// whether an active transition is running (which is transient).
	return nil
}

// SetFTB sets or clears the Fade to Black state. If the desired state matches
// the current state, this is a no-op. If they differ, FadeToBlack is toggled.
func (a *App) SetFTB(active bool) error {
	state := a.sw.State()
	if state.FTBActive == active {
		return nil // already in desired state
	}
	if active {
		// Need to enter FTB — toggle it on.
		return a.sw.FadeToBlack(context.Background())
	}
	// Need to exit FTB — toggle it off (FadeToBlack toggles when in FTB state).
	return a.sw.FadeToBlack(context.Background())
}

// --- Audio per-channel ---

func (a *App) SetLevel(sourceKey string, levelDB float64) error {
	return a.mixer.SetLevel(sourceKey, levelDB)
}

func (a *App) SetTrim(sourceKey string, trimDB float64) error {
	return a.mixer.SetTrim(sourceKey, trimDB)
}

func (a *App) SetMuted(sourceKey string, muted bool) error {
	return a.mixer.SetMuted(sourceKey, muted)
}

func (a *App) SetAFV(sourceKey string, afv bool) error {
	return a.mixer.SetAFV(sourceKey, afv)
}

func (a *App) SetPhaseInvert(sourceKey string, invert bool) error {
	return a.mixer.SetPhaseInvert(sourceKey, invert)
}

func (a *App) SetBalance(sourceKey string, balance float64) error {
	return a.mixer.SetBalance(sourceKey, balance)
}

func (a *App) SetHPF(sourceKey string, frequency float64, enabled bool) error {
	return a.mixer.SetHPF(sourceKey, frequency, enabled)
}

func (a *App) SetEQ(sourceKey string, band int, frequency, gain, q float64, enabled bool) error {
	return a.mixer.SetEQ(sourceKey, band, frequency, gain, q, enabled)
}

func (a *App) SetCompressor(sourceKey string, threshold, ratio, attack, release, makeupGain float64) error {
	return a.mixer.SetCompressor(sourceKey, threshold, ratio, attack, release, makeupGain)
}

func (a *App) SetGate(sourceKey string, threshold, rangeDB, attack, release, hold float64, enabled bool) error {
	return a.mixer.SetGate(sourceKey, threshold, rangeDB, attack, release, hold, enabled)
}

func (a *App) SetAudioDelay(sourceKey string, delayMs int) error {
	return a.mixer.SetAudioDelay(sourceKey, delayMs)
}

// --- Audio master ---

func (a *App) SetMasterLevel(levelDB float64) error {
	return a.mixer.SetMasterLevel(levelDB)
}

func (a *App) SetLimiterThreshold(thresholdDB float64) error {
	return a.mixer.SetLimiterThreshold(thresholdDB)
}

func (a *App) SetLimiterCeiling(ceilingDB float64) error {
	return a.mixer.SetLimiterCeiling(ceilingDB)
}

// --- Graphics ---

func (a *App) SetGraphicsLayerActive(id int, active bool) error {
	if a.compositor == nil {
		return nil
	}
	if active {
		return a.compositor.On(id)
	}
	return a.compositor.Off(id)
}

func (a *App) SetGraphicsLayerZOrder(id int, zOrder int) error {
	if a.compositor == nil {
		return nil
	}
	return a.compositor.SetLayerZOrder(id, zOrder)
}

func (a *App) SetGraphicsLayerRect(id int, x, y, width, height int) error {
	if a.compositor == nil {
		return nil
	}
	return a.compositor.SetLayerRect(id, image.Rect(x, y, x+width, y+height))
}

// --- DVE Layout ---

func (a *App) SetDVELayout(layout internal.LayoutSnapshot) error {
	if a.dveCompositor == nil {
		return nil
	}

	l := &dve.Layout{
		Name:  layout.ActivePreset,
		Slots: make([]dve.Slot, len(layout.Slots)),
	}

	for i, ss := range layout.Slots {
		slot := dve.Slot{
			SourceKey: ss.SourceKey,
			Enabled:   ss.Enabled,
			ZOrder:    ss.ZOrder,
			Transform: dve.Transform{
				PositionX:    ss.PositionX,
				PositionY:    ss.PositionY,
				Width:        ss.Width,
				Height:       ss.Height,
				RotationDeg:  ss.RotationDeg,
				AnchorX:      ss.AnchorX,
				AnchorY:      ss.AnchorY,
				PerspectiveX: ss.PerspectiveX,
				PerspectiveY: ss.PerspectiveY,
				CropTop:      ss.CropTop,
				CropBottom:   ss.CropBottom,
				CropLeft:     ss.CropLeft,
				CropRight:    ss.CropRight,
				CropSoftness: ss.CropSoftness,
				Opacity:      ss.Opacity,
				ScaleMode:    ss.ScaleMode,
				CropAnchor:   ss.CropAnchor,
				DefocusRadius: ss.DefocusRadius,
				MirrorH:      ss.MirrorH,
				MirrorV:      ss.MirrorV,
				MosaicSize:   ss.MosaicSize,
				Saturation:   ss.Saturation,
				Brightness:   ss.Brightness,
				Contrast:     ss.Contrast,
			},
		}

		if ss.Border != nil {
			slot.Transform.Border = dve.BorderConfig{
				Width:        ss.Border.Width,
				ColorY:       ss.Border.ColorY,
				ColorCb:      ss.Border.ColorCb,
				ColorCr:      ss.Border.ColorCr,
				CornerRadius: ss.Border.CornerRadius,
				Softness:     ss.Border.Softness,
				InnerWidth:   ss.Border.InnerWidth,
			}
		}

		if ss.Shadow != nil {
			slot.Transform.Shadow = dve.ShadowConfig{
				Enabled: ss.Shadow.Enabled,
				OffsetX: ss.Shadow.OffsetX,
				OffsetY: ss.Shadow.OffsetY,
				Blur:    ss.Shadow.Blur,
				Opacity: ss.Shadow.Opacity,
				ColorY:  ss.Shadow.ColorY,
			}
		}

		if ss.Mode3D != "" {
			slot.Transform.Mode3D = ss.Mode3D
			slot.Transform.Transform3D = &dve.Transform3D{
				RotateX: ss.RotateX,
				RotateY: ss.RotateY,
				RotateZ: ss.RotateZ,
			}
		}

		l.Slots[i] = slot
	}

	a.dveCompositor.SetLayout(l)
	return nil
}

// --- Source config ---

func (a *App) SetSourceLabel(ctx context.Context, sourceKey, label string) error {
	return a.sw.SetLabel(ctx, sourceKey, label)
}

func (a *App) SetSourcePosition(sourceKey string, position int) error {
	return a.sw.SetSourcePosition(sourceKey, position)
}

// --- Upstream keys ---

func (a *App) SetUpstreamKey(sourceKey string, config internal.UpstreamKeySnapshot) error {
	if a.keyProcessor == nil {
		return nil
	}
	a.keyProcessor.SetKey(sourceKey, graphics.KeyConfig{
		Type:           graphics.KeyType(config.Type),
		Enabled:        config.Enabled,
		KeyColorY:      config.KeyColorY,
		KeyColorCb:     config.KeyColorCb,
		KeyColorCr:     config.KeyColorCr,
		Similarity:     config.Similarity,
		Smoothness:     config.Smoothness,
		SpillSuppress:  config.SpillSuppress,
		SpillReplaceCb: config.SpillReplaceCb,
		SpillReplaceCr: config.SpillReplaceCr,
		LowClip:        config.LowClip,
		HighClip:       config.HighClip,
		Softness:       config.Softness,
		FillSource:     config.FillSource,
	})
	return nil
}

// --- Captions ---

func (a *App) SetCaptionMode(mode string) error {
	if a.captionMgr == nil {
		return nil
	}
	m, ok := caption.ParseMode(mode)
	if !ok {
		return fmt.Errorf("unknown caption mode %q", mode)
	}
	a.captionMgr.SetMode(m)
	return nil
}

// --- Color grading ---

func (a *App) SetColorGrade(lookName string, intensity float32) error {
	if a.colorGradeCtrl == nil {
		return nil
	}
	return a.colorGradeCtrl.SetColorGrade(lookName, intensity)
}

func (a *App) DisableColorGrade() error {
	if a.colorGradeCtrl == nil {
		return nil
	}
	a.colorGradeCtrl.DisableColorGrade()
	return nil
}

// --- ST maps ---

func (a *App) AssignSTMapSource(sourceKey, mapName string) error {
	if a.stmapRegistry == nil {
		return nil
	}
	return a.stmapRegistry.AssignSource(sourceKey, mapName)
}

func (a *App) AssignSTMapProgram(mapName string) error {
	if a.stmapRegistry == nil {
		return nil
	}
	return a.stmapRegistry.AssignProgram(mapName)
}

// --- Clip player restore ---

// LoadClipByName resolves a clip name to its ID via the store and loads it
// into the given player slot. Used for cross-engine snapshot restore where
// clip IDs differ between engines but names are consistent.
func (a *App) LoadClipByName(slotIndex int, clipName string) error {
	if a.clipMgr == nil {
		return nil
	}
	return a.clipMgr.LoadByName(slotIndex, clipName)
}

// --- Optional snapshot applier interfaces (operational state) ---

// ApplyRecording reconciles recording state from a snapshot. If the snapshot
// says recording should be active but we're not recording, start. If the
// snapshot says inactive but we are recording, stop. Best-effort: errors
// are logged but not returned.
func (a *App) ApplyRecording(snap internal.RecordingSnapshot) error {
	if a.outputMgr == nil {
		return nil
	}

	status := a.outputMgr.RecordingStatus()

	if snap.Active && !status.Active {
		// Snapshot says recording should be active — start with default config.
		if err := a.outputMgr.StartRecording(output.RecorderConfig{}); err != nil {
			slog.Warn("snapshot apply: start recording failed", "error", err)
		}
	} else if !snap.Active && status.Active {
		// Snapshot says recording should be stopped — stop.
		if err := a.outputMgr.StopRecording(); err != nil {
			slog.Warn("snapshot apply: stop recording failed", "error", err)
		}
	}

	return nil
}

// ApplyOutputDestinations reconciles output destinations from a snapshot.
// Only updates the active state of existing destinations — does not create
// or delete destinations (IDs are engine-specific).
func (a *App) ApplyOutputDestinations(dests []internal.DestinationSnapshot) error {
	if a.outputMgr == nil {
		return nil
	}

	// Build a lookup of desired state by ID.
	desired := make(map[string]bool, len(dests))
	for _, d := range dests {
		desired[d.ID] = d.Active
	}

	// Walk existing destinations and reconcile active state.
	existing := a.outputMgr.ListDestinations()
	for _, dest := range existing {
		wantActive, ok := desired[dest.ID]
		if !ok {
			// Destination not in snapshot — leave unchanged.
			continue
		}

		isActive := dest.State == "connected" || dest.State == "starting"
		if wantActive && !isActive {
			if err := a.outputMgr.StartDestination(dest.ID); err != nil {
				slog.Warn("snapshot apply: start destination failed", "id", dest.ID, "error", err)
			}
		} else if !wantActive && isActive {
			if err := a.outputMgr.StopDestination(dest.ID); err != nil {
				slog.Warn("snapshot apply: stop destination failed", "id", dest.ID, "error", err)
			}
		}
	}

	return nil
}

// ApplyReplayMarks restores replay mark-in/mark-out points from a snapshot.
// Uses SetMarks to set both points from Unix millisecond timestamps.
func (a *App) ApplyReplayMarks(marks internal.ReplayMarksSnapshot) error {
	if a.replayMgr == nil {
		return nil
	}

	markIn := marks.MarkIn
	markOut := marks.MarkOut
	if err := a.replayMgr.SetMarks(&markIn, &markOut); err != nil {
		slog.Warn("snapshot apply: set replay marks failed", "error", err)
	}

	return nil
}

// ApplyReplayPlayer reconciles replay player state from a snapshot.
// If the snapshot says playing but we're idle, we cannot start playback
// (requires mark points and clip extraction which is transient). If the
// snapshot says idle but we're playing, stop. Best-effort.
func (a *App) ApplyReplayPlayer(player internal.ReplayPlayerSnapshot) error {
	if a.replayMgr == nil {
		return nil
	}

	status := a.replayMgr.Status()
	snapshotPlaying := player.State == "playing"
	currentlyPlaying := status.State == replay.PlayerPlaying

	if !snapshotPlaying && currentlyPlaying {
		// Snapshot says idle but we're playing — stop.
		if err := a.replayMgr.Stop(); err != nil {
			slog.Warn("snapshot apply: stop replay player failed", "error", err)
		}
	}
	// Cannot start playback from a snapshot — requires live clip extraction
	// from the circular buffer, which is transient engine-local state.

	return nil
}

// ApplyOperatorSessions is a no-op. Operator sessions are per-browser-connection
// and cannot be transferred between engines. The snapshot captures them for
// informational purposes only.
func (a *App) ApplyOperatorSessions(_ []internal.OperatorSessionSnapshot) error {
	return nil
}

// ApplyPlayoutChannels reconciles playout channel positions from a snapshot.
// Best-effort: seeks each channel's scheduler cursor to the snapshot position
// if the channel is currently on-air.
func (a *App) ApplyPlayoutChannels(channels []internal.PlayoutChannelSnapshot) error {
	if a.playoutMgr == nil {
		return nil
	}

	states := a.playoutMgr.ChannelStates()
	for _, chSnap := range channels {
		idx := chSnap.ChannelIndex
		if idx < 0 || idx >= len(states) {
			continue
		}

		// Only adjust cursor for channels that are currently on-air.
		if states[idx].State != "on_air" {
			continue
		}

		// SetCursor is on the Channel directly. Access through the manager's
		// exported method if available, or log a skip.
		slog.Info("snapshot apply: playout channel position noted",
			"channel", idx+1,
			"targetItem", chSnap.CurrentItemIndex,
			"schedulerState", chSnap.SchedulerState,
		)
		// Playout channels don't expose a public seek-to-item-index on the Manager.
		// The scheduler cursor is internal state managed by the tick loop. Attempting
		// to forcibly set it from outside could cause frame discontinuities.
		// This is informational — the channel continues from its current position.
	}

	return nil
}

// ApplySCTE35Active reconciles active SCTE-35 events from a snapshot.
// Best-effort: if a held event exists in the snapshot but not locally,
// we cannot recreate it (it requires a live muxer sink). If a local event
// should be held per the snapshot, apply the hold.
func (a *App) ApplySCTE35Active(events []internal.SCTE35ActiveEventSnapshot) error {
	if a.scte35Injector == nil {
		return nil
	}

	// Build lookup of desired state.
	desired := make(map[uint32]internal.SCTE35ActiveEventSnapshot, len(events))
	for _, e := range events {
		desired[e.EventID] = e
	}

	// Walk active events and apply hold state if needed.
	activeIDs := a.scte35Injector.ActiveEventIDs()
	for _, id := range activeIDs {
		snapEvent, ok := desired[id]
		if !ok {
			continue
		}
		if snapEvent.Hold {
			if err := a.scte35Injector.HoldBreak(id); err != nil {
				slog.Warn("snapshot apply: hold SCTE-35 event failed", "eventId", id, "error", err)
			}
		}
	}

	return nil
}

// ApplyMacroExecution is a best-effort no-op. Macro execution is managed by
// the control API layer (which owns the goroutine, cancel context, and progress
// callbacks). The App layer does not have direct access to start or interrupt
// macro execution. The snapshot captures macro state for informational display.
func (a *App) ApplyMacroExecution(_ internal.MacroExecutionSnapshot) error {
	return nil
}

// SetEncoderIfAvailable switches to the named encoder if it is available on
// this engine. Used during cross-engine snapshot reconciliation so that both
// engines converge on the same encoder.
func (a *App) SetEncoderIfAvailable(name string) error {
	for _, enc := range a.sw.AvailableEncoders() {
		if enc.Name == name {
			return a.sw.SetEncoder(name)
		}
	}
	return fmt.Errorf("encoder %q not available on this engine", name)
}
