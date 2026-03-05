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
	SetMasterLevel(level float64)
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
			msg := fmt.Sprintf("program source %q: %v", p.ProgramSource, err)
			slog.Warn("preset recall: "+msg)
			warnings = append(warnings, msg)
		}
	}

	// Set preview source
	if p.PreviewSource != "" {
		if err := target.SetPreview(ctx, p.PreviewSource); err != nil {
			msg := fmt.Sprintf("preview source %q: %v", p.PreviewSource, err)
			slog.Warn("preset recall: "+msg)
			warnings = append(warnings, msg)
		}
	}

	// Apply audio channel settings
	for key, ch := range p.AudioChannels {
		if err := target.SetLevel(key, ch.Level); err != nil {
			msg := fmt.Sprintf("audio channel %q level: %v", key, err)
			slog.Warn("preset recall: "+msg)
			warnings = append(warnings, msg)
			continue // skip mute/AFV if channel doesn't exist
		}
		if err := target.SetMuted(key, ch.Muted); err != nil {
			msg := fmt.Sprintf("audio channel %q mute: %v", key, err)
			slog.Warn("preset recall: "+msg)
			warnings = append(warnings, msg)
		}
		if err := target.SetAFV(key, ch.AFV); err != nil {
			msg := fmt.Sprintf("audio channel %q afv: %v", key, err)
			slog.Warn("preset recall: "+msg)
			warnings = append(warnings, msg)
		}
	}

	// Set master level
	target.SetMasterLevel(p.MasterLevel)

	return warnings
}
