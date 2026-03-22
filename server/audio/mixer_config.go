package audio

import "fmt"

// SetBalance sets the stereo balance for a source channel.
// balance: -1.0 (hard left) to +1.0 (hard right), 0.0 = center.
// At center both L/R gains are 1.0 (transparent, zero CPU).
func (m *Mixer) SetBalance(sourceKey string, balance float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch, ok := m.channels[sourceKey]
	if !ok {
		return fmt.Errorf("audio: unknown source %q", sourceKey)
	}
	if balance < -1.0 || balance > 1.0 {
		return fmt.Errorf("audio: balance must be between -1.0 and 1.0")
	}
	ch.balance = balance
	ch.balL, ch.balR = BalanceGains(balance)
	return nil
}

// SetPhaseInvert enables or disables phase inversion for a source channel.
// When enabled, all PCM samples are multiplied by -1.0 (180-degree phase flip).
func (m *Mixer) SetPhaseInvert(sourceKey string, invert bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch, ok := m.channels[sourceKey]
	if !ok {
		return fmt.Errorf("audio: unknown source %q", sourceKey)
	}
	ch.phaseInvert = invert
	return nil
}

// SetHPF sets the high-pass filter parameters for a channel.
func (m *Mixer) SetHPF(sourceKey string, frequency float64, enabled bool) error {
	m.mu.Lock()
	ch, ok := m.channels[sourceKey]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("audio: unknown source %q", sourceKey)
	}
	hpf := ch.hpf
	m.mu.Unlock()
	return hpf.SetParams(frequency, enabled)
}

// GetHPF returns the current HPF frequency and enabled state for a channel.
func (m *Mixer) GetHPF(sourceKey string) (frequency float64, enabled bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ch, ok := m.channels[sourceKey]
	if !ok {
		return 80, false
	}
	return ch.hpf.Frequency(), ch.hpf.Enabled()
}

// SetGate sets the noise gate parameters for a channel.
func (m *Mixer) SetGate(sourceKey string, threshold, rangeDB, attack, release, hold float64, enabled bool) error {
	m.mu.Lock()
	ch, ok := m.channels[sourceKey]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("audio: unknown source %q", sourceKey)
	}
	gate := ch.gate
	m.mu.Unlock()
	return gate.SetParams(threshold, rangeDB, attack, release, hold, enabled)
}

// GetGate returns the current noise gate settings and gain reduction for a channel.
func (m *Mixer) GetGate(sourceKey string) (GateState, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ch, ok := m.channels[sourceKey]
	if !ok {
		return GateState{}, fmt.Errorf("audio: unknown source %q", sourceKey)
	}
	threshold, rangeDB, attack, release, hold, enabled := ch.gate.Settings()
	return GateState{
		Threshold:     threshold,
		Range:         rangeDB,
		Attack:        attack,
		Release:       release,
		Hold:          hold,
		Enabled:       enabled,
		GainReduction: ch.gate.GainReduction(),
	}, nil
}

// DebugSnapshot implements debug.SnapshotProvider.
func (m *Mixer) DebugSnapshot() map[string]any {
	m.mu.RLock()
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
			"phase_invert":        ch.phaseInvert,
			"balance":             ch.balance,
			"hpf_frequency":       ch.hpf.Frequency(),
			"hpf_enabled":         ch.hpf.Enabled(),
			"gate_bypassed":       ch.gate.IsBypassed(),
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
		"mode":                   "mixing",
		"program_peak_dbfs":      peak,
		"channels_active":        activeCount,
		"channels_muted":         mutedCount,
		"channels":               channelDetails,
		"frames_mixed":           m.framesMixed.Load(),
		"frames_output_total":    m.outputFrameCount.Load(),
		"trans_crossfade_active": transCrossfadeActive,
		"trans_crossfade_pos":    transCrossfadePos,
		"trans_crossfade_from":   transCrossfadeFrom,
		"trans_crossfade_to":     transCrossfadeTo,
		"trans_crossfade_count":  m.transCrossfades.Load(),
		"decode_errors":          m.decodeErrors.Load(),
		"encode_errors":          m.encodeErrors.Load(),
		"max_inter_frame_gap_ms": maxGapMs,
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
