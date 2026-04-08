package internal

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
)

// SnapshotApplyTarget is the interface used by ApplyFullSnapshot to restore
// engine state from a FullStateSnapshot. The real implementation lives in the
// main package (the App struct satisfies this interface).
//
// This is deliberately broader than preset.RecallTarget — it covers every
// subsystem that has restorable state in the full snapshot.
type SnapshotApplyTarget interface {
	// Switching
	Cut(ctx context.Context, source string) error
	SetPreview(ctx context.Context, source string) error

	// Transition config
	SetTransitionConfig(transitionType string, durationMs int, easing string) error
	SetFTB(active bool) error

	// Audio per-channel
	SetLevel(sourceKey string, levelDB float64) error
	SetTrim(sourceKey string, trimDB float64) error
	SetMuted(sourceKey string, muted bool) error
	SetAFV(sourceKey string, afv bool) error
	SetPhaseInvert(sourceKey string, invert bool) error
	SetBalance(sourceKey string, balance float64) error
	SetHPF(sourceKey string, frequency float64, enabled bool) error
	SetEQ(sourceKey string, band int, frequency, gain, q float64, enabled bool) error
	SetCompressor(sourceKey string, threshold, ratio, attack, release, makeupGain float64) error
	SetGate(sourceKey string, threshold, rangeDB, attack, release, hold float64, enabled bool) error
	SetAudioDelay(sourceKey string, delayMs int) error

	// Audio master
	SetMasterLevel(levelDB float64) error
	SetLimiterThreshold(thresholdDB float64) error
	SetLimiterCeiling(ceilingDB float64) error

	// Graphics
	SetGraphicsLayerActive(id int, active bool) error
	SetGraphicsLayerZOrder(id int, zOrder int) error
	SetGraphicsLayerRect(id int, x, y, width, height int) error

	// DVE Layout
	SetDVELayout(layout LayoutSnapshot) error

	// Source config
	SetSourceLabel(ctx context.Context, sourceKey, label string) error
	SetSourcePosition(sourceKey string, position int) error

	// Upstream keys
	SetUpstreamKey(sourceKey string, config UpstreamKeySnapshot) error

	// Captions
	SetCaptionMode(mode string) error

	// Color grading
	SetColorGrade(lookName string, intensity float32) error
	DisableColorGrade() error

	// ST maps
	AssignSTMapSource(sourceKey, mapName string) error
	AssignSTMapProgram(mapName string) error
}

// Optional applier interfaces for operational state. Targets may implement
// any subset of these — ApplyFullSnapshot uses type assertions to check.

// SnapshotRecordingApplier is optionally implemented for recording state.
type SnapshotRecordingApplier interface {
	ApplyRecording(snap RecordingSnapshot) error
}

// SnapshotDestinationApplier is optionally implemented for output destinations.
type SnapshotDestinationApplier interface {
	ApplyOutputDestinations(dests []DestinationSnapshot) error
}

// SnapshotReplayApplier is optionally implemented for replay marks and player state.
type SnapshotReplayApplier interface {
	ApplyReplayMarks(marks ReplayMarksSnapshot) error
	ApplyReplayPlayer(player ReplayPlayerSnapshot) error
}

// SnapshotOperatorApplier is optionally implemented for operator sessions.
type SnapshotOperatorApplier interface {
	ApplyOperatorSessions(sessions []OperatorSessionSnapshot) error
}

// SnapshotPlayoutApplier is optionally implemented for playout channel state.
type SnapshotPlayoutApplier interface {
	ApplyPlayoutChannels(channels []PlayoutChannelSnapshot) error
}

// SnapshotSCTE35Applier is optionally implemented for active SCTE-35 events.
type SnapshotSCTE35Applier interface {
	ApplySCTE35Active(events []SCTE35ActiveEventSnapshot) error
}

// SnapshotMacroApplier is optionally implemented for macro execution state.
type SnapshotMacroApplier interface {
	ApplyMacroExecution(exec MacroExecutionSnapshot) error
}

// SnapshotClipApplier is optionally implemented for loading clips by name
// into player slots during cross-engine snapshot restore. Clip IDs differ
// between engines, so name-based lookup is used instead.
type SnapshotClipApplier interface {
	LoadClipByName(slotIndex int, clipName string) error
}

// SnapshotGraphicsHTML5Applier is optionally implemented for HTML5 graphics overlay reload.
type SnapshotGraphicsHTML5Applier interface {
	LoadGraphicsHTML5(layerID int, url string) error
}

// ApplyFullSnapshot restores engine state from a FullStateSnapshot. It is
// best-effort: individual failures are collected as warning strings rather
// than aborting the entire apply. This is the inverse of state capture.
//
// Apply order is intentional — transition config is set before switching so
// that Cut/SetPreview use the correct transition parameters, audio is set
// before program source to avoid brief wrong-level output, etc.
//
// Returns a list of human-readable warning strings (empty on full success).
func ApplyFullSnapshot(ctx context.Context, snap FullStateSnapshot, target SnapshotApplyTarget) []string {
	var warnings []string

	// 1. Transition config — so cuts/previews use the right transition
	if err := target.SetTransitionConfig(snap.TransitionType, snap.TransitionDurationMs, snap.TransitionEasing); err != nil {
		slog.Warn("snapshot apply: transition config failed", "error", err)
		warnings = append(warnings, fmt.Sprintf("transition config: %v", err))
	}

	// 2. Audio channels — all per-channel settings (sorted for determinism)
	warnings = applyAudioChannels(snap.AudioChannels, target, warnings)

	// 3. Audio master — master level + limiter
	if err := target.SetMasterLevel(snap.AudioMaster.MasterLevel); err != nil {
		slog.Warn("snapshot apply: master level failed", "error", err)
		warnings = append(warnings, fmt.Sprintf("master level: %v", err))
	}
	if err := target.SetLimiterThreshold(snap.AudioMaster.LimiterThreshold); err != nil {
		slog.Warn("snapshot apply: limiter threshold failed", "error", err)
		warnings = append(warnings, fmt.Sprintf("limiter threshold: %v", err))
	}
	if err := target.SetLimiterCeiling(snap.AudioMaster.LimiterCeiling); err != nil {
		slog.Warn("snapshot apply: limiter ceiling failed", "error", err)
		warnings = append(warnings, fmt.Sprintf("limiter ceiling: %v", err))
	}

	// 4. Program source (Cut)
	if err := target.Cut(ctx, snap.ProgramSource); err != nil {
		slog.Warn("snapshot apply: program source failed", "source", snap.ProgramSource, "error", err)
		warnings = append(warnings, fmt.Sprintf("program source %q: %v", snap.ProgramSource, err))
	}

	// 5. Preview source
	if err := target.SetPreview(ctx, snap.PreviewSource); err != nil {
		slog.Warn("snapshot apply: preview source failed", "source", snap.PreviewSource, "error", err)
		warnings = append(warnings, fmt.Sprintf("preview source %q: %v", snap.PreviewSource, err))
	}

	// 6. FTB — fade to black state
	if err := target.SetFTB(snap.FTBActive); err != nil {
		slog.Warn("snapshot apply: FTB failed", "error", err)
		warnings = append(warnings, fmt.Sprintf("FTB: %v", err))
	}

	// 7. Graphics layers
	for _, layer := range snap.Graphics {
		if err := target.SetGraphicsLayerActive(layer.ID, layer.Active); err != nil {
			slog.Warn("snapshot apply: graphics layer active failed", "id", layer.ID, "error", err)
			warnings = append(warnings, fmt.Sprintf("graphics layer %d active: %v", layer.ID, err))
		}
		if err := target.SetGraphicsLayerZOrder(layer.ID, layer.ZOrder); err != nil {
			slog.Warn("snapshot apply: graphics layer z-order failed", "id", layer.ID, "error", err)
			warnings = append(warnings, fmt.Sprintf("graphics layer %d z-order: %v", layer.ID, err))
		}
		if err := target.SetGraphicsLayerRect(layer.ID, layer.X, layer.Y, layer.Width, layer.Height); err != nil {
			slog.Warn("snapshot apply: graphics layer rect failed", "id", layer.ID, "error", err)
			warnings = append(warnings, fmt.Sprintf("graphics layer %d rect: %v", layer.ID, err))
		}
		if layer.HTML5URL != "" {
			if applier, ok := target.(SnapshotGraphicsHTML5Applier); ok {
				if err := applier.LoadGraphicsHTML5(layer.ID, layer.HTML5URL); err != nil {
					slog.Warn("snapshot apply: graphics layer HTML5 reload failed", "id", layer.ID, "url", layer.HTML5URL, "error", err)
					warnings = append(warnings, fmt.Sprintf("graphics layer %d: load HTML5 %q: %v", layer.ID, layer.HTML5URL, err))
				}
			}
		}
	}

	// 8. DVE layout — full slot configuration
	if err := target.SetDVELayout(snap.Layout); err != nil {
		slog.Warn("snapshot apply: DVE layout failed", "error", err)
		warnings = append(warnings, fmt.Sprintf("DVE layout: %v", err))
	}

	// 9. Source config — labels and positions (sorted for determinism)
	warnings = applySourceConfig(ctx, snap.Sources, target, warnings)

	// 10. Upstream keys (sorted for determinism)
	warnings = applyUpstreamKeys(snap.UpstreamKeys, target, warnings)

	// 11. Caption mode
	if err := target.SetCaptionMode(snap.CaptionMode); err != nil {
		slog.Warn("snapshot apply: caption mode failed", "error", err)
		warnings = append(warnings, fmt.Sprintf("caption mode: %v", err))
	}

	// 12. Color grading
	warnings = applyColorGrade(snap.ColorGrade, target, warnings)

	// 13. ST maps — source and program assignments
	warnings = applySTMaps(snap.STMaps, target, warnings)

	// 14. Clip player state — load clips by name (IDs differ between engines).
	if len(snap.ClipPlayerPositions) > 0 {
		if applier, ok := target.(SnapshotClipApplier); ok {
			for _, cp := range snap.ClipPlayerPositions {
				if cp.ClipName == "" {
					continue
				}
				if err := applier.LoadClipByName(cp.PlayerIndex, cp.ClipName); err != nil {
					slog.Warn("snapshot apply: clip player load failed",
						"slot", cp.PlayerIndex, "clipName", cp.ClipName, "error", err)
					warnings = append(warnings, fmt.Sprintf("clip player slot %d (%q): %v", cp.PlayerIndex, cp.ClipName, err))
				}
			}
		}
	}

	// --- Operational state (optional applier interfaces) ---

	// 15. Output destinations
	if snap.OutputDestinations != nil {
		if applier, ok := target.(SnapshotDestinationApplier); ok {
			if err := applier.ApplyOutputDestinations(snap.OutputDestinations); err != nil {
				slog.Warn("snapshot apply: output destinations failed", "error", err)
				warnings = append(warnings, fmt.Sprintf("output destinations: %v", err))
			}
		}
	}

	// 16. Recording
	if snap.Recording != nil {
		if applier, ok := target.(SnapshotRecordingApplier); ok {
			if err := applier.ApplyRecording(*snap.Recording); err != nil {
				slog.Warn("snapshot apply: recording failed", "error", err)
				warnings = append(warnings, fmt.Sprintf("recording: %v", err))
			}
		}
	}

	// 17. Replay marks
	if snap.ReplayMarks != nil {
		if applier, ok := target.(SnapshotReplayApplier); ok {
			if err := applier.ApplyReplayMarks(*snap.ReplayMarks); err != nil {
				slog.Warn("snapshot apply: replay marks failed", "error", err)
				warnings = append(warnings, fmt.Sprintf("replay marks: %v", err))
			}
		}
	}

	// 18. Replay player
	if snap.ReplayPlayer != nil {
		if applier, ok := target.(SnapshotReplayApplier); ok {
			if err := applier.ApplyReplayPlayer(*snap.ReplayPlayer); err != nil {
				slog.Warn("snapshot apply: replay player failed", "error", err)
				warnings = append(warnings, fmt.Sprintf("replay player: %v", err))
			}
		}
	}

	// 19. Playout channels
	if snap.PlayoutChannels != nil {
		if applier, ok := target.(SnapshotPlayoutApplier); ok {
			if err := applier.ApplyPlayoutChannels(snap.PlayoutChannels); err != nil {
				slog.Warn("snapshot apply: playout channels failed", "error", err)
				warnings = append(warnings, fmt.Sprintf("playout channels: %v", err))
			}
		}
	}

	// 20. SCTE-35 active events
	if snap.SCTE35Active != nil {
		if applier, ok := target.(SnapshotSCTE35Applier); ok {
			if err := applier.ApplySCTE35Active(snap.SCTE35Active); err != nil {
				slog.Warn("snapshot apply: scte-35 events failed", "error", err)
				warnings = append(warnings, fmt.Sprintf("scte-35 events: %v", err))
			}
		}
	}

	// 21. Operator sessions
	if snap.OperatorSessions != nil {
		if applier, ok := target.(SnapshotOperatorApplier); ok {
			if err := applier.ApplyOperatorSessions(snap.OperatorSessions); err != nil {
				slog.Warn("snapshot apply: operator sessions failed", "error", err)
				warnings = append(warnings, fmt.Sprintf("operator sessions: %v", err))
			}
		}
	}

	// 22. Macro execution
	if snap.MacroExecution != nil {
		if applier, ok := target.(SnapshotMacroApplier); ok {
			if err := applier.ApplyMacroExecution(*snap.MacroExecution); err != nil {
				slog.Warn("snapshot apply: macro execution failed", "error", err)
				warnings = append(warnings, fmt.Sprintf("macro execution: %v", err))
			}
		}
	}

	// Note: CommsParticipants and StingerLoaded are informational — no apply
	// action needed. Comms requires re-joining the WebTransport stream, and
	// stingers are pre-cached by name on both engines.

	return warnings
}

// applyAudioChannels applies per-channel audio settings in sorted key order.
func applyAudioChannels(channels map[string]AudioChannelSnapshot, target SnapshotApplyTarget, warnings []string) []string {
	keys := make([]string, 0, len(channels))
	for k := range channels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		ch := channels[key]

		if err := target.SetLevel(key, ch.Level); err != nil {
			slog.Warn("snapshot apply: audio level failed", "channel", key, "error", err)
			warnings = append(warnings, fmt.Sprintf("audio channel %q level: %v", key, err))
		}
		if err := target.SetTrim(key, ch.Trim); err != nil {
			slog.Warn("snapshot apply: audio trim failed", "channel", key, "error", err)
			warnings = append(warnings, fmt.Sprintf("audio channel %q trim: %v", key, err))
		}
		if err := target.SetMuted(key, ch.Muted); err != nil {
			slog.Warn("snapshot apply: audio mute failed", "channel", key, "error", err)
			warnings = append(warnings, fmt.Sprintf("audio channel %q mute: %v", key, err))
		}
		if err := target.SetAFV(key, ch.AFV); err != nil {
			slog.Warn("snapshot apply: audio AFV failed", "channel", key, "error", err)
			warnings = append(warnings, fmt.Sprintf("audio channel %q afv: %v", key, err))
		}
		if err := target.SetPhaseInvert(key, ch.PhaseInvert); err != nil {
			slog.Warn("snapshot apply: audio phase invert failed", "channel", key, "error", err)
			warnings = append(warnings, fmt.Sprintf("audio channel %q phase invert: %v", key, err))
		}
		if err := target.SetBalance(key, ch.Balance); err != nil {
			slog.Warn("snapshot apply: audio balance failed", "channel", key, "error", err)
			warnings = append(warnings, fmt.Sprintf("audio channel %q balance: %v", key, err))
		}
		if err := target.SetHPF(key, ch.HPFFrequency, ch.HPFEnabled); err != nil {
			slog.Warn("snapshot apply: audio HPF failed", "channel", key, "error", err)
			warnings = append(warnings, fmt.Sprintf("audio channel %q hpf: %v", key, err))
		}
		for band := 0; band < 3; band++ {
			eq := ch.EQ[band]
			if err := target.SetEQ(key, band, eq.Frequency, eq.Gain, eq.Q, eq.Enabled); err != nil {
				slog.Warn("snapshot apply: audio EQ failed", "channel", key, "band", band, "error", err)
				warnings = append(warnings, fmt.Sprintf("audio channel %q eq band %d: %v", key, band, err))
			}
		}
		if err := target.SetCompressor(key, ch.Compressor.Threshold, ch.Compressor.Ratio,
			ch.Compressor.Attack, ch.Compressor.Release, ch.Compressor.MakeupGain); err != nil {
			slog.Warn("snapshot apply: audio compressor failed", "channel", key, "error", err)
			warnings = append(warnings, fmt.Sprintf("audio channel %q compressor: %v", key, err))
		}
		if err := target.SetGate(key, ch.Gate.Threshold, ch.Gate.Range, ch.Gate.Attack,
			ch.Gate.Release, ch.Gate.Hold, ch.Gate.Enabled); err != nil {
			slog.Warn("snapshot apply: audio gate failed", "channel", key, "error", err)
			warnings = append(warnings, fmt.Sprintf("audio channel %q gate: %v", key, err))
		}
		if err := target.SetAudioDelay(key, ch.AudioDelayMs); err != nil {
			slog.Warn("snapshot apply: audio delay failed", "channel", key, "error", err)
			warnings = append(warnings, fmt.Sprintf("audio channel %q delay: %v", key, err))
		}
	}

	return warnings
}

// applySourceConfig applies per-source label and position in sorted key order.
func applySourceConfig(ctx context.Context, sources map[string]SourceConfigSnapshot, target SnapshotApplyTarget, warnings []string) []string {
	keys := make([]string, 0, len(sources))
	for k := range sources {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		src := sources[key]
		if err := target.SetSourceLabel(ctx, key, src.Label); err != nil {
			slog.Warn("snapshot apply: source label failed", "source", key, "error", err)
			warnings = append(warnings, fmt.Sprintf("source %q label: %v", key, err))
		}
		if err := target.SetSourcePosition(key, src.Position); err != nil {
			slog.Warn("snapshot apply: source position failed", "source", key, "error", err)
			warnings = append(warnings, fmt.Sprintf("source %q position: %v", key, err))
		}
	}

	return warnings
}

// applyUpstreamKeys applies upstream key configurations in sorted key order.
func applyUpstreamKeys(keys map[string]UpstreamKeySnapshot, target SnapshotApplyTarget, warnings []string) []string {
	sortedKeys := make([]string, 0, len(keys))
	for k := range keys {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)

	for _, key := range sortedKeys {
		cfg := keys[key]
		if err := target.SetUpstreamKey(key, cfg); err != nil {
			slog.Warn("snapshot apply: upstream key failed", "source", key, "error", err)
			warnings = append(warnings, fmt.Sprintf("upstream key %q: %v", key, err))
		}
	}

	return warnings
}

// applyColorGrade applies or disables color grading based on the snapshot.
func applyColorGrade(cg *ColorGradeSnapshot, target SnapshotApplyTarget, warnings []string) []string {
	if cg == nil || !cg.Enabled {
		if err := target.DisableColorGrade(); err != nil {
			slog.Warn("snapshot apply: disable color grade failed", "error", err)
			warnings = append(warnings, fmt.Sprintf("disable color grade: %v", err))
		}
		return warnings
	}

	if err := target.SetColorGrade(cg.LookName, cg.Intensity); err != nil {
		slog.Warn("snapshot apply: color grade failed", "error", err)
		warnings = append(warnings, fmt.Sprintf("color grade %q: %v", cg.LookName, err))
	}
	return warnings
}

// applySTMaps applies ST map source and program assignments.
func applySTMaps(stmaps *STMapSnapshot, target SnapshotApplyTarget, warnings []string) []string {
	if stmaps == nil {
		return warnings
	}

	// Source assignments (sorted for determinism)
	srcKeys := make([]string, 0, len(stmaps.Sources))
	for k := range stmaps.Sources {
		srcKeys = append(srcKeys, k)
	}
	sort.Strings(srcKeys)

	for _, key := range srcKeys {
		mapName := stmaps.Sources[key]
		if err := target.AssignSTMapSource(key, mapName); err != nil {
			slog.Warn("snapshot apply: ST map source failed", "source", key, "map", mapName, "error", err)
			warnings = append(warnings, fmt.Sprintf("ST map source %q → %q: %v", key, mapName, err))
		}
	}

	// Program assignment
	if stmaps.Program != "" {
		if err := target.AssignSTMapProgram(stmaps.Program); err != nil {
			slog.Warn("snapshot apply: ST map program failed", "map", stmaps.Program, "error", err)
			warnings = append(warnings, fmt.Sprintf("ST map program %q: %v", stmaps.Program, err))
		}
	}

	return warnings
}
