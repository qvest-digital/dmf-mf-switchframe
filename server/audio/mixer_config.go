package audio

import "log/slog"

// recalcPassthrough updates the passthrough flag. Caller must hold m.mu write lock.
// Logs when the mode actually changes (rare — only on cuts, mute toggles, etc.).
func (m *Mixer) recalcPassthrough() {
	prev := m.passthrough

	// Program mute or active transition crossfade require the mixing path.
	if m.programMuted || m.transCrossfadeActive {
		m.passthrough = false
		if prev != m.passthrough {
			m.modeTransitions.Add(1)
			m.log.Info("passthrough mode changed",
				slog.Bool("passthrough", false),
				slog.String("reason", "program muted or transition crossfade active"))
		}
		return
	}

	activeCount := 0
	var activeKey string
	for key, ch := range m.channels {
		if ch.active {
			activeCount++
			activeKey = key
		}
	}

	if activeCount == 1 && m.masterLevel == 0 {
		ch := m.channels[activeKey]
		m.passthrough = !ch.muted && ch.level == 0 && ch.trim == 0 &&
			ch.eq.IsBypassed() && ch.compressor.IsBypassed()
	} else {
		m.passthrough = false
	}

	if prev != m.passthrough {
		m.modeTransitions.Add(1)
		var reason string
		if m.passthrough {
			reason = "single active source at 0dB"
		} else if activeCount == 0 {
			reason = "no active sources"
		} else if activeCount == 1 {
			reason = "single active source with gain or mute"
		} else {
			reason = "multiple active sources or master gain"
		}
		m.log.Info("passthrough mode changed",
			slog.Bool("passthrough", m.passthrough),
			slog.String("reason", reason),
			slog.Int("active_count", activeCount))
	}
}

// ProgramPeak returns the current program output peak levels in dBFS.
// Returns [leftDBFS, rightDBFS]. Silence is -Inf.

// DebugSnapshot implements debug.SnapshotProvider.
func (m *Mixer) DebugSnapshot() map[string]any {
	m.mu.RLock()
	mode := "mixing"
	if m.passthrough {
		mode = "passthrough"
	}
	activeCount := 0
	mutedCount := 0
	channelDetails := make(map[string]any, len(m.channels))
	for key, ch := range m.channels {
		if ch.active {
			activeCount++
		}
		if ch.muted {
			mutedCount++
		}
		detail := map[string]any{
			"active":              ch.active,
			"muted":               ch.muted,
			"afv":                 ch.afv,
			"level":               ch.level,
			"trim":                ch.trim,
			"eq_bypassed":         ch.eq.IsBypassed(),
			"compressor_bypassed": ch.compressor.IsBypassed(),
			"delay_ms":            ch.audioDelay.DelayMs(),
			"peak_l_dbfs":         LinearToDBFS(ch.peakL),
			"peak_r_dbfs":         LinearToDBFS(ch.peakR),
		}
		channelDetails[key] = detail
	}
	transCrossfadeActive := m.transCrossfadeActive
	transCrossfadePos := m.transCrossfadePosition
	transCrossfadeFrom := m.transCrossfadeFrom
	transCrossfadeTo := m.transCrossfadeTo
	peak := [2]float64{LinearToDBFS(m.programPeakL), LinearToDBFS(m.programPeakR)}
	m.mu.RUnlock()

	maxGapMs := m.maxInterFrameNano.Load() / 1e6

	result := map[string]any{
		"mode":                   mode,
		"program_peak_dbfs":      peak,
		"channels_active":        activeCount,
		"channels_muted":         mutedCount,
		"channels":               channelDetails,
		"frames_passthrough":     m.framesPassthrough.Load(),
		"frames_mixed":           m.framesMixed.Load(),
		"frames_output_total":    m.outputFrameCount.Load(),
		"crossfade_count":        m.crossfadeCount.Load(),
		"crossfade_timeouts":     m.crossfadeTimeouts.Load(),
		"trans_crossfade_active": transCrossfadeActive,
		"trans_crossfade_pos":    transCrossfadePos,
		"trans_crossfade_from":   transCrossfadeFrom,
		"trans_crossfade_to":     transCrossfadeTo,
		"trans_crossfade_count":  m.transCrossfades.Load(),
		"decode_errors":          m.decodeErrors.Load(),
		"encode_errors":          m.encodeErrors.Load(),
		"deadline_flushes":       m.deadlineFlushes.Load(),
		"max_inter_frame_gap_ms": maxGapMs,
		"mode_transitions":       m.modeTransitions.Load(),
	}

	if m.loudness != nil {
		result["loudness"] = map[string]any{
			"momentary_lufs":  m.loudness.MomentaryLUFS(),
			"short_term_lufs": m.loudness.ShortTermLUFS(),
			"integrated_lufs": m.loudness.IntegratedLUFS(),
		}
	}

	return result
}
