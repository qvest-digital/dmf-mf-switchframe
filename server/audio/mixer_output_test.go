package audio

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/metrics"
)

// constPCM returns n stereo samples all of value v.
func constPCM(n int, v float32) []float32 {
	pcm := make([]float32, n*2)
	for i := range pcm {
		pcm[i] = v
	}
	return pcm
}

func TestProduceSamples_SingleChannel_ProducesOutput(t *testing.T) {
	t.Parallel()
	m, tl := newProduceTestMixer(t), newTimeline()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	tl.push(m, "cam1", constPCM(1024, 0.25), 0)

	out := tl.produce(m, 1024)
	require.Len(t, out, 1024*2, "ProduceSamples should return exactly 1024*2 values")
	// With fader at 0dB (levelLinear=1.0) and master at 0dB, after the
	// limiter the value should be close to 0.25 (well below its threshold).
	assert.InDelta(t, 0.25, out[0], 0.01, "PCM should preserve input through unity gain")
}

func TestProduceSamples_NoChannels_ProducesSilence(t *testing.T) {
	t.Parallel()
	m, tl := newProduceTestMixer(t), newTimeline()

	out := tl.produce(m, 1024)
	require.Len(t, out, 1024*2)
	for i, v := range out {
		assert.Equal(t, float32(0), v, "sample %d should be zero (silence)", i)
	}
}

func TestProduceSamples_TwoChannels_SumsOutput(t *testing.T) {
	t.Parallel()
	m, tl := newProduceTestMixer(t), newTimeline()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)
	tl.push(m, "cam1", constPCM(1024, 0.3), 0)
	tl.push(m, "cam2", constPCM(1024, 0.2), 0)

	out := tl.produce(m, 1024)
	require.Len(t, out, 1024*2)
	// After summing and master chain (unity gain, limiter at -1dBFS = ~0.891),
	// 0.5 is well below the limiter threshold so it should pass through.
	assert.InDelta(t, 0.5, out[0], 0.01,
		"output should be sum of both channels (0.3 + 0.2 = 0.5)")
}

func TestProduceSamples_EmptyRingBuf_ProducesSilence(t *testing.T) {
	t.Parallel()
	m, tl := newProduceTestMixer(t), newTimeline()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	tl.push(m, "cam1", constPCM(1024, 0.4), 0)

	// First period consumes the frame
	out := tl.produce(m, 1024)
	require.InDelta(t, 0.4, out[0], 0.01, "first period should carry the pushed audio")

	// Second period: ring buffer is empty, should produce silence (not freeze-repeat)
	out = tl.produce(m, 1024)
	assert.InDelta(t, 0.0, out[0], 0.01,
		"empty ring buffer should produce silence, not freeze-repeat")
}

func TestProduceSamples_MutedChannel_Excluded(t *testing.T) {
	t.Parallel()
	m, tl := newProduceTestMixer(t), newTimeline()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	_ = m.SetMuted("cam1", true)
	tl.push(m, "cam1", constPCM(1024, 0.9), 0)

	out := tl.produce(m, 1024)
	for i, v := range out {
		assert.Equal(t, float32(0), v, "sample %d should be zero (muted channel skipped)", i)
	}
}

func TestProduceSamples_InactiveChannel_Excluded(t *testing.T) {
	t.Parallel()
	m, tl := newProduceTestMixer(t), newTimeline()

	m.AddChannel("cam1")
	// cam1 is NOT activated -- should be excluded from mix
	tl.push(m, "cam1", constPCM(1024, 0.8), 0)

	out := tl.produce(m, 1024)
	for i, v := range out {
		assert.Equal(t, float32(0), v, "sample %d should be zero (inactive channel)", i)
	}
}

func TestProduceSamples_FaderGainApplied(t *testing.T) {
	t.Parallel()
	m, tl := newProduceTestMixer(t), newTimeline()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	// Set fader to -6dB (approximately 0.5012 linear)
	_ = m.SetLevel("cam1", -6.0)
	tl.push(m, "cam1", constPCM(1024, 0.5), 0)

	out := tl.produce(m, 1024)
	// Expected: 0.5 * 0.5012 ~= 0.2506
	assert.InDelta(t, 0.5*0.5012, out[0], 0.02,
		"fader gain should be applied to PCM")
}

func TestProduceSamples_TransitionGainInterpolation(t *testing.T) {
	t.Parallel()
	m, tl := newProduceTestMixer(t), newTimeline()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	// Simulate a transition crossfade at 50%
	m.mu.Lock()
	m.transCrossfadeActive = true
	m.transCrossfadeFrom = "cam1"
	m.transCrossfadeTo = "cam2"
	m.transCrossfadePosition = 0.5
	m.transCrossfadeMode = Crossfade
	m.transCrossfadeAudioPos = 0.5
	m.mu.Unlock()

	tl.push(m, "cam1", constPCM(1024, 1.0), 0)
	tl.push(m, "cam2", constPCM(1024, 1.0), 0)

	out := tl.produce(m, 1024)
	// At the equal-power crossfade midpoint, total gain = cos(pi/4) +
	// sin(pi/4) = sqrt(2) ~= 1.414 before the brickwall limiter, which keeps
	// the output <= 0.891. Check it is non-zero (positive).
	assert.Greater(t, out[0], float32(0), "output should be positive with crossfade")
}

// produceAndAdvance is one reference clock period: the program audio, then
// the cut crossfade advanced, as the sync loop does.
func produceAndAdvance(m *Mixer, tl *timeline, n int) []float32 {
	out := tl.produce(m, n)
	m.AdvanceCrossfade()
	return out
}

func TestMixer_CutCrossfade(t *testing.T) {
	t.Parallel()
	m, tl := newProduceTestMixer(t), newTimeline()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	// cam2 starts inactive -- OnCut should activate it

	// cam1 = old source (full amplitude), cam2 = new source (half amplitude).
	// Enough frames for 5 periods (cut crossfade uses 4 + 1 after).
	for i := 0; i < 5; i++ {
		tl.push(m, "cam1", constPCM(1024, 0.6), i*1024)
		tl.push(m, "cam2", constPCM(1024, 0.3), i*1024)
	}

	// Trigger cut crossfade
	m.OnCut("cam1", "cam2")

	// Verify transition state was set up
	m.mu.RLock()
	assert.True(t, m.transCrossfadeActive, "transCrossfadeActive should be true after OnCut")
	assert.Equal(t, "cam1", m.transCrossfadeFrom)
	assert.Equal(t, "cam2", m.transCrossfadeTo)
	assert.Equal(t, 4, m.cutFramesRemaining, "should have 4 frames remaining")
	assert.Equal(t, 4, m.cutTotalFrames, "total frames should be 4")
	m.mu.RUnlock()

	// The cut period itself ramps the new source from silence to 0.25.
	m.mu.RLock()
	assert.InDelta(t, 0.25, m.transCrossfadePosition, 0.01, "the cut period ramps to 0.25")
	m.mu.RUnlock()

	// Period 1: ramps 0 -> 0.25, then advances to 0.5
	out := produceAndAdvance(m, tl, 1024)
	require.Len(t, out, 1024*2, "first period should produce output")

	m.mu.RLock()
	assert.Equal(t, 3, m.cutFramesRemaining, "should have 3 frames remaining after first period")
	assert.InDelta(t, 0.5, m.transCrossfadePosition, 0.01, "position should be 0.5 after first period")
	assert.True(t, m.transCrossfadeActive, "should still be active after first period")
	m.mu.RUnlock()

	// Periods 2-3: crossfade continues
	for i := 2; i <= 3; i++ {
		produceAndAdvance(m, tl, 1024)
	}

	m.mu.RLock()
	assert.Equal(t, 1, m.cutFramesRemaining, "should have 1 frame remaining after period 3")
	assert.InDelta(t, 1.0, m.transCrossfadePosition, 0.01, "position should be 1.0 after period 3")
	assert.True(t, m.transCrossfadeActive, "should still be active after period 3")
	m.mu.RUnlock()

	// Period 4: ramps 0.75 -> 1.0, then completes
	produceAndAdvance(m, tl, 1024)

	m.mu.RLock()
	assert.False(t, m.transCrossfadeActive, "crossfade should be complete after 4 periods")
	assert.Equal(t, 0, m.cutFramesRemaining, "no frames remaining after completion")
	assert.Equal(t, "", m.transCrossfadeFrom, "from should be cleared")
	assert.Equal(t, "", m.transCrossfadeTo, "to should be cleared")
	m.mu.RUnlock()

	// Period 5: normal operation, no crossfade
	out = produceAndAdvance(m, tl, 1024)
	require.Len(t, out, 1024*2, "fifth period should produce output normally")
}

func TestMixer_CutCrossfade_ActivatesNewSource(t *testing.T) {
	t.Parallel()
	m := newProduceTestMixer(t)

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	// cam2 starts inactive

	m.mu.RLock()
	assert.False(t, m.channels["cam2"].active, "cam2 should be inactive before cut")
	m.mu.RUnlock()

	m.OnCut("cam1", "cam2")

	m.mu.RLock()
	assert.True(t, m.channels["cam2"].active, "cam2 should be activated by OnCut")
	m.mu.RUnlock()
}

func TestMixer_TransitionCrossfade(t *testing.T) {
	t.Parallel()
	m, tl := newProduceTestMixer(t), newTimeline()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	// cam1 is on program and follows video, so the dissolve fades it out.
	require.NoError(t, m.SetAFV("cam1", true))
	m.SetActive("cam1", true)
	// cam2 starts inactive -- OnTransitionStart should activate it

	// Start a dissolve transition
	m.OnTransitionStart("cam1", "cam2", Crossfade, 1000)

	m.mu.RLock()
	assert.True(t, m.transCrossfadeActive, "should be active after OnTransitionStart")
	assert.Equal(t, "cam1", m.transCrossfadeFrom)
	assert.Equal(t, "cam2", m.transCrossfadeTo)
	assert.True(t, m.channels["cam2"].active, "cam2 should be activated")
	assert.Equal(t, 0, m.cutFramesRemaining, "cut frames should not be set for transitions")
	m.mu.RUnlock()

	push := func(offset int) {
		tl.push(m, "cam1", constPCM(1024, 0.5), offset)
		tl.push(m, "cam2", constPCM(1024, 0.5), offset)
	}

	// Period at position 0.0 (fully old source)
	push(0)
	out := tl.produce(m, 1024)
	assert.InDelta(t, 0.5, out[0], 0.01, "position 0.0 should carry only the old source")

	// Advance position to 0.5
	m.OnTransitionPosition(0.5)
	push(1024)
	out = tl.produce(m, 1024)
	// The gain ramps from position 0.0 to 0.5 across the period; at its end
	// both sources contribute ~0.707 * 0.5 = 0.354 each, ~0.707 in sum.
	assert.InDelta(t, 0.707, out[len(out)-1], 0.02,
		"blended output at the period's end should be the equal-power sum")

	// Advance position to 1.0
	m.OnTransitionPosition(1.0)
	push(2048)
	out = tl.produce(m, 1024)
	assert.InDelta(t, 0.5, out[len(out)-1], 0.01, "position 1.0 should end on the new source alone")

	// Complete the transition
	m.OnTransitionComplete()

	m.mu.RLock()
	assert.False(t, m.transCrossfadeActive, "should not be active after complete")
	m.mu.RUnlock()
}

func TestMixer_CutCrossfade_GainProgression(t *testing.T) {
	t.Parallel()
	// A cut ramps the incoming source over cutRampPeriods periods, the cut
	// period included: period k ramps the position from k/4 to (k+1)/4.
	// The cut period starts at silence and ends at the first step, so no
	// period of the ramp is silent.
	m, tl := newProduceTestMixer(t), newTimeline()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	const n, level = 1024, 0.5
	for i := 0; i < cutRampPeriods+2; i++ {
		tl.push(m, "cam1", constPCM(n, 0), i*n)
		tl.push(m, "cam2", constPCM(n, level), i*n)
	}

	m.OnCut("cam1", "cam2")

	for k := 0; k < cutRampPeriods; k++ {
		out := produceAndAdvance(m, tl, n)
		first := level * transitionToGain(Crossfade, float64(k)/cutRampPeriods)
		last := level * transitionToGain(Crossfade, float64(k+1)/cutRampPeriods)
		assert.InDelta(t, first, out[0], 1e-6, "period %d starts at position %d/4", k, k)
		assert.InDelta(t, last, out[2*(n-1)], 1e-6, "period %d ends at position %d/4", k, k+1)
		peakL, _ := PeakLevel(out, 2)
		assert.Greater(t, peakL, 0.0, "period %d of the ramp is silent", k)
	}

	out := tl.produce(m, n)
	assert.InDelta(t, level, out[0], 1e-6, "full gain once the ramp is done")
	m.mu.RLock()
	assert.False(t, m.transCrossfadeActive, "the ramp completes after its last period")
	m.mu.RUnlock()
}

func TestMixer_OnTransitionAbort(t *testing.T) {
	t.Parallel()
	m := newProduceTestMixer(t)

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	token := m.OnTransitionStart("cam1", "cam2", Crossfade, 1000)
	m.OnTransitionPosition(0.5)

	m.mu.RLock()
	assert.True(t, m.transCrossfadeActive)
	m.mu.RUnlock()

	m.OnTransitionAbort(token)

	m.mu.RLock()
	assert.False(t, m.transCrossfadeActive, "should be inactive after abort")
	assert.Equal(t, "", m.transCrossfadeFrom, "from should be cleared after abort")
	assert.Equal(t, "", m.transCrossfadeTo, "to should be cleared after abort")
	assert.InDelta(t, 0.0, m.transCrossfadePosition, 0.001, "position should be reset after abort")
	m.mu.RUnlock()
}

func TestMixer_CutDoesNotSetCutFramesForTransitions(t *testing.T) {
	t.Parallel()
	// Verify that OnTransitionStart does NOT set cutFramesRemaining
	// (only OnCut should set it).
	m := newProduceTestMixer(t)

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	m.OnTransitionStart("cam1", "cam2", Crossfade, 1000)

	m.mu.RLock()
	assert.Equal(t, 0, m.cutFramesRemaining, "transitions should not set cutFramesRemaining")
	assert.Equal(t, 0, m.cutTotalFrames, "transitions should not set cutTotalFrames")
	m.mu.RUnlock()
}

// Every ProduceSamples call is one period mixed, and counts as one.
func TestProduceSamples_PrometheusFramesMixed(t *testing.T) {
	pm := metrics.NewMetrics(prometheus.NewRegistry())

	m, tl := newProduceTestMixer(t), newTimeline()
	m.SetMetrics(pm)
	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	tl.produce(m, 1024)
	tl.produce(m, 1024)
	require.Equal(t, float64(2), testutil.ToFloat64(pm.FramesMixedTotal))
}
