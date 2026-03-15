package preset

import (
	"context"
	"fmt"
	"log/slog"
)

// RecallTarget is the interface used by Recall to apply a preset to the
// switcher and audio mixer. It abstracts away the real switcher/mixer so
// the recall logic is testable without real hardware or relays.
type RecallTarget interface {
	// Cut switches the program source.
	Cut(ctx context.Context, source string) error

	// SetPreview sets the preview source.
	SetPreview(ctx context.Context, source string) error

	// SetLevel sets the audio level for a source channel.
	SetLevel(sourceKey string, levelDB float64) error

	// SetMuted sets the mute state for a source channel.
	SetMuted(sourceKey string, muted bool) error

	// SetAFV sets the audio-follows-video state for a source channel.
	SetAFV(sourceKey string, afv bool) error

	// SetMasterLevel sets the master output level.
	SetMasterLevel(level float64) error
}

// Recall applies a preset to the given target, returning warnings for any
// sources or audio channels that could not be applied (e.g., because the
// source is no longer connected). Recall is best-effort: it applies as
// much of the preset as possible and collects warnings for the rest.
func Recall(ctx context.Context, p Preset, target RecallTarget) []string {
	var warnings []string

	// Set program source
	if p.ProgramSource != "" {
		if err := target.Cut(ctx, p.ProgramSource); err != nil {
			slog.Warn("preset recall: program source failed",
				"source", p.ProgramSource, "error", err)
			warnings = append(warnings, fmt.Sprintf("program source %q: %v", p.ProgramSource, err))
		}
	}

	// Set preview source
	if p.PreviewSource != "" {
		if err := target.SetPreview(ctx, p.PreviewSource); err != nil {
			slog.Warn("preset recall: preview source failed",
				"source", p.PreviewSource, "error", err)
			warnings = append(warnings, fmt.Sprintf("preview source %q: %v", p.PreviewSource, err))
		}
	}

	// Apply audio channel settings
	for key, ch := range p.AudioChannels {
		if err := target.SetLevel(key, ch.Level); err != nil {
			slog.Warn("preset recall: audio level failed",
				"channel", key, "error", err)
			warnings = append(warnings, fmt.Sprintf("audio channel %q level: %v", key, err))
			continue // skip mute/AFV if channel doesn't exist
		}
		if err := target.SetMuted(key, ch.Muted); err != nil {
			slog.Warn("preset recall: audio mute failed",
				"channel", key, "error", err)
			warnings = append(warnings, fmt.Sprintf("audio channel %q mute: %v", key, err))
		}
		if err := target.SetAFV(key, ch.AFV); err != nil {
			slog.Warn("preset recall: audio AFV failed",
				"channel", key, "error", err)
			warnings = append(warnings, fmt.Sprintf("audio channel %q afv: %v", key, err))
		}
	}

	// Set master level
	if err := target.SetMasterLevel(p.MasterLevel); err != nil {
		slog.Warn("preset recall: master level failed",
			"level", p.MasterLevel, "error", err)
		warnings = append(warnings, fmt.Sprintf("master level %.1f: %v", p.MasterLevel, err))
	}

	return warnings
}
