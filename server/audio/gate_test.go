package audio

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

func TestGate_NewGate_StartsBypassed(t *testing.T) {
	t.Parallel()
	g := NewGate(48000, 2)

	require.True(t, g.IsBypassed(), "new gate should start bypassed")
	require.InDelta(t, 1.0, g.envelope, 0.001, "envelope should start at 1.0 (open)")
	require.Equal(t, 0.0, g.GainReduction(), "GR should be 0 when bypassed")
}

func TestGate_SetParams_ValidatesThreshold(t *testing.T) {
	t.Parallel()
	g := NewGate(48000, 2)

	require.ErrorIs(t, g.SetParams(-61, -60, 0.5, 100, 50, true), ErrInvalidGateThreshold)
	require.ErrorIs(t, g.SetParams(1, -60, 0.5, 100, 50, true), ErrInvalidGateThreshold)
	require.NoError(t, g.SetParams(-60, -60, 0.5, 100, 50, true))
	require.NoError(t, g.SetParams(0, -60, 0.5, 100, 50, true))
}

func TestGate_SetParams_ValidatesRange(t *testing.T) {
	t.Parallel()
	g := NewGate(48000, 2)

	require.ErrorIs(t, g.SetParams(-40, -81, 0.5, 100, 50, true), ErrInvalidGateRange)
	require.ErrorIs(t, g.SetParams(-40, -19, 0.5, 100, 50, true), ErrInvalidGateRange)
	require.NoError(t, g.SetParams(-40, -80, 0.5, 100, 50, true))
	require.NoError(t, g.SetParams(-40, -20, 0.5, 100, 50, true))
}

func TestGate_SetParams_ValidatesAttack(t *testing.T) {
	t.Parallel()
	g := NewGate(48000, 2)

	require.ErrorIs(t, g.SetParams(-40, -60, 0.001, 100, 50, true), ErrInvalidGateAttack)
	require.ErrorIs(t, g.SetParams(-40, -60, 11, 100, 50, true), ErrInvalidGateAttack)
	require.NoError(t, g.SetParams(-40, -60, 0.01, 100, 50, true))
	require.NoError(t, g.SetParams(-40, -60, 10.0, 100, 50, true))
}

func TestGate_SetParams_ValidatesRelease(t *testing.T) {
	t.Parallel()
	g := NewGate(48000, 2)

	require.ErrorIs(t, g.SetParams(-40, -60, 0.5, 9, 50, true), ErrInvalidGateRelease)
	require.ErrorIs(t, g.SetParams(-40, -60, 0.5, 501, 50, true), ErrInvalidGateRelease)
	require.NoError(t, g.SetParams(-40, -60, 0.5, 10, 50, true))
	require.NoError(t, g.SetParams(-40, -60, 0.5, 500, 50, true))
}

func TestGate_SetParams_ValidatesHold(t *testing.T) {
	t.Parallel()
	g := NewGate(48000, 2)

	require.ErrorIs(t, g.SetParams(-40, -60, 0.5, 100, -1, true), ErrInvalidGateHold)
	require.ErrorIs(t, g.SetParams(-40, -60, 0.5, 100, 501, true), ErrInvalidGateHold)
	require.NoError(t, g.SetParams(-40, -60, 0.5, 100, 0, true))
	require.NoError(t, g.SetParams(-40, -60, 0.5, 100, 500, true))
}

func TestGate_AttenuatesBelowThreshold(t *testing.T) {
	t.Parallel()
	g := NewGate(48000, 2)

	// Threshold -20 dBFS, range -60 dB, fast attack/release, no hold
	err := g.SetParams(-20, -60, 0.01, 10, 0, true)
	require.NoError(t, err)

	// Feed a quiet signal well below threshold (-40 dBFS)
	level := float32(math.Pow(10, -40.0/20.0)) // ~0.01
	samples := make([]float32, 48000)           // 1 second of stereo at 48kHz
	for i := range samples {
		samples[i] = level
	}

	g.Process(samples)

	// After 1 second of quiet signal, gate should be mostly closed.
	// With range -60 dB, floor is 10^(-60/20) = 0.001
	// Signal should be attenuated significantly
	lastSample := float64(samples[len(samples)-1])
	if lastSample < 0 {
		lastSample = -lastSample
	}
	// The output should be much quieter than the input
	require.Less(t, lastSample, float64(level)*0.1,
		"gate should attenuate signal well below threshold")
}

func TestGate_PassesAboveThreshold(t *testing.T) {
	t.Parallel()
	g := NewGate(48000, 2)

	// Threshold -20 dBFS, range -60 dB, fast attack, medium release
	err := g.SetParams(-20, -60, 0.01, 100, 0, true)
	require.NoError(t, err)

	// Feed a loud signal above threshold (0 dBFS)
	samples := make([]float32, 4096)
	for i := range samples {
		samples[i] = 0.5 // -6 dBFS, well above -20 dBFS threshold
	}
	original := make([]float32, len(samples))
	copy(original, samples)

	g.Process(samples)

	// Gate should be open, signal should be near-unity
	// Check last samples after envelope has settled
	for i := len(samples) - 100; i < len(samples); i++ {
		require.InDelta(t, float64(original[i]), float64(samples[i]), 0.05,
			"sample %d should be near-unchanged when above threshold", i)
	}
}

func TestGate_HoldKeepsGateOpen(t *testing.T) {
	t.Parallel()
	g := NewGate(48000, 2)

	// Threshold -20 dBFS, range -60 dB, fast attack/release, 100ms hold
	err := g.SetParams(-20, -60, 0.01, 10, 100, true)
	require.NoError(t, err)

	// First: send loud signal to open gate
	loud := make([]float32, 4800) // 100ms mono equivalent
	for i := range loud {
		loud[i] = 0.5
	}
	g.Process(loud)

	// Now send quiet signal. With 100ms hold, gate should stay open briefly.
	// 100ms at 48kHz stereo = 9600 samples. We'll check at 50ms (4800 samples).
	quiet := make([]float32, 4800) // 50ms of stereo
	quietLevel := float32(math.Pow(10, -40.0/20.0))
	for i := range quiet {
		quiet[i] = quietLevel
	}
	g.Process(quiet)

	// At 50ms into hold period, gate should still be mostly open
	// The last samples should not be heavily attenuated yet
	lastSample := float64(quiet[len(quiet)-1])
	if lastSample < 0 {
		lastSample = -lastSample
	}
	require.Greater(t, lastSample, float64(quietLevel)*0.5,
		"gate should remain mostly open during hold time")
}

func TestGate_GainReduction_WhenClosed(t *testing.T) {
	t.Parallel()
	g := NewGate(48000, 2)

	// Threshold -20 dBFS, range -60 dB, fast attack/release, no hold
	err := g.SetParams(-20, -60, 0.01, 10, 0, true)
	require.NoError(t, err)

	// Feed quiet signal to close gate
	level := float32(math.Pow(10, -40.0/20.0))
	samples := make([]float32, 48000)
	for i := range samples {
		samples[i] = level
	}
	g.Process(samples)

	// GR should be positive (indicating reduction)
	gr := g.GainReduction()
	require.Greater(t, gr, 10.0,
		"gain reduction should be significant when gate is closed")
}

func TestGate_GainReduction_WhenOpen(t *testing.T) {
	t.Parallel()
	g := NewGate(48000, 2)

	err := g.SetParams(-20, -60, 0.01, 100, 0, true)
	require.NoError(t, err)

	// Feed loud signal to open gate
	samples := make([]float32, 4096)
	for i := range samples {
		samples[i] = 0.5
	}
	g.Process(samples)

	// GR should be near zero when gate is open
	gr := g.GainReduction()
	require.Less(t, gr, 1.0,
		"gain reduction should be near zero when gate is open")
}

func TestGate_Reset(t *testing.T) {
	t.Parallel()
	g := NewGate(48000, 2)

	err := g.SetParams(-20, -60, 0.01, 10, 0, true)
	require.NoError(t, err)

	// Close the gate
	level := float32(math.Pow(10, -40.0/20.0))
	samples := make([]float32, 48000)
	for i := range samples {
		samples[i] = level
	}
	g.Process(samples)

	require.Greater(t, g.GainReduction(), 10.0, "gate should be closed")

	// Reset should restore envelope to open
	g.Reset()

	// Process one frame to apply the reset
	small := make([]float32, 2)
	small[0] = 0.5
	small[1] = 0.5
	g.Process(small)

	require.InDelta(t, 1.0, g.envelope, 0.01, "envelope should be near 1.0 after reset")
}

func TestGate_IsBypassed_WhenDisabled(t *testing.T) {
	t.Parallel()
	g := NewGate(48000, 2)

	// Default is disabled
	require.True(t, g.IsBypassed())

	// Enable
	err := g.SetParams(-40, -60, 0.5, 100, 50, true)
	require.NoError(t, err)
	require.False(t, g.IsBypassed())

	// Disable again
	err = g.SetParams(-40, -60, 0.5, 100, 50, false)
	require.NoError(t, err)
	require.True(t, g.IsBypassed())
}

func TestGate_Process_Bypassed_NoChange(t *testing.T) {
	t.Parallel()
	g := NewGate(48000, 2)

	// Gate is disabled by default
	samples := []float32{0.5, -0.3, 0.7, -0.1}
	original := make([]float32, len(samples))
	copy(original, samples)

	g.Process(samples)

	for i := range samples {
		require.Equal(t, original[i], samples[i],
			"bypassed gate should not modify samples")
	}
}

func TestGate_Settings_ReturnsCurrentParams(t *testing.T) {
	t.Parallel()
	g := NewGate(48000, 2)

	err := g.SetParams(-30, -50, 1.0, 200, 75, true)
	require.NoError(t, err)

	threshold, rangeDB, attack, release, hold, enabled := g.Settings()
	require.Equal(t, -30.0, threshold)
	require.Equal(t, -50.0, rangeDB)
	require.Equal(t, 1.0, attack)
	require.Equal(t, 200.0, release)
	require.Equal(t, 75.0, hold)
	require.True(t, enabled)
}

func TestGate_Settings_DefaultValues(t *testing.T) {
	t.Parallel()
	g := NewGate(48000, 2)

	threshold, rangeDB, attack, release, hold, enabled := g.Settings()
	require.Equal(t, -40.0, threshold)
	require.Equal(t, -60.0, rangeDB)
	require.Equal(t, 0.5, attack)
	require.Equal(t, 100.0, release)
	require.Equal(t, 50.0, hold)
	require.False(t, enabled)
}

func TestGate_StereoLinkedEnvelope(t *testing.T) {
	t.Parallel()
	g := NewGate(48000, 2)

	// Threshold -20 dBFS, range -60 dB
	err := g.SetParams(-20, -60, 0.01, 100, 0, true)
	require.NoError(t, err)

	// Stereo signal: left loud, right quiet
	// Gate should open based on peak of both channels
	samples := make([]float32, 2048)
	for i := 0; i < len(samples); i += 2 {
		samples[i] = 0.5                                   // left: loud
		samples[i+1] = float32(math.Pow(10, -40.0/20.0))  // right: quiet
	}
	original := make([]float32, len(samples))
	copy(original, samples)

	g.Process(samples)

	// Both channels should pass through because left channel is above threshold
	for i := len(samples) - 100; i < len(samples); i++ {
		require.InDelta(t, float64(original[i]), float64(samples[i]), 0.05,
			"sample %d should pass when either channel exceeds threshold", i)
	}
}

func TestMixer_SetGate_UnknownSource(t *testing.T) {
	t.Parallel()
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(*media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	err := m.SetGate("nonexistent", -40, -60, 0.5, 100, 50, true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nonexistent")
}

func TestMixer_SetGate_ValidParams(t *testing.T) {
	t.Parallel()
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(*media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")

	err := m.SetGate("cam1", -30, -50, 1.0, 200, 75, true)
	require.NoError(t, err)
}

func TestMixer_GetGate_ReturnsState(t *testing.T) {
	t.Parallel()
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(*media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")

	err := m.SetGate("cam1", -30, -50, 1.0, 200, 75, true)
	require.NoError(t, err)

	state, err := m.GetGate("cam1")
	require.NoError(t, err)
	require.Equal(t, -30.0, state.Threshold)
	require.Equal(t, -50.0, state.Range)
	require.Equal(t, 1.0, state.Attack)
	require.Equal(t, 200.0, state.Release)
	require.Equal(t, 75.0, state.Hold)
	require.True(t, state.Enabled)
}

func TestMixer_GetGate_UnknownSource(t *testing.T) {
	t.Parallel()
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(*media.AudioFrame) {},
	})
	defer m.Close()

	_, err := m.GetGate("nonexistent")
	require.Error(t, err)
}

func TestMixer_ChannelStates_IncludesGate(t *testing.T) {
	t.Parallel()
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(*media.AudioFrame) {},
	})
	defer m.Close()

	m.AddChannel("cam1")
	err := m.SetGate("cam1", -25, -45, 2.0, 150, 60, true)
	require.NoError(t, err)

	states := m.ChannelStates()
	ch, ok := states["cam1"]
	require.True(t, ok)
	require.True(t, ch.GateEnabled)
	require.Equal(t, -25.0, ch.Gate.Threshold)
	require.Equal(t, -45.0, ch.Gate.Range)
	require.Equal(t, 2.0, ch.Gate.Attack)
	require.Equal(t, 150.0, ch.Gate.Release)
	require.Equal(t, 60.0, ch.Gate.Hold)
}

func TestMixer_DebugSnapshot_IncludesGate(t *testing.T) {
	t.Parallel()
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(*media.AudioFrame) {},
	})
	defer m.Close()

	m.AddChannel("cam1")
	err := m.SetGate("cam1", -30, -60, 0.5, 100, 50, true)
	require.NoError(t, err)

	snap := m.DebugSnapshot()
	channels, ok := snap["channels"].(map[string]any)
	require.True(t, ok)
	cam1, ok := channels["cam1"].(map[string]any)
	require.True(t, ok)

	_, hasGateBypassed := cam1["gate_bypassed"]
	require.True(t, hasGateBypassed, "debug snapshot should include gate_bypassed")
}

func TestMixer_IngestPCM_GateApplied(t *testing.T) {
	t.Parallel()
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(*media.AudioFrame) {},
	})
	defer m.Close()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Enable gate with threshold at -10 dBFS, hard gate
	err := m.SetGate("cam1", -10, -80, 0.01, 10, 0, true)
	require.NoError(t, err)

	// Feed quiet signal (-30 dBFS) for 1 second to close the gate
	level := float32(math.Pow(10, -30.0/20.0))
	for i := 0; i < 47; i++ { // ~1 second of 1024-sample frames
		pcm := make([]float32, 2048) // 1024 samples * 2 channels
		for j := range pcm {
			pcm[j] = level
		}
		m.IngestPCM("cam1", pcm, int64(i)*1920, 2)
	}

	// After feeding quiet signal, the gate should have attenuated it.
	// We verify via the gate's gain reduction metering.
	m.mu.RLock()
	ch := m.channels["cam1"]
	m.mu.RUnlock()

	gr := ch.gate.GainReduction()
	require.Greater(t, gr, 5.0, "gate should show gain reduction for quiet signal")
}
