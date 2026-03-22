package audio

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

func TestBalanceGains_Center(t *testing.T) {
	t.Parallel()
	gainL, gainR := BalanceGains(0)
	require.Equal(t, float32(1.0), gainL, "center L")
	require.Equal(t, float32(1.0), gainR, "center R")
}

func TestBalanceGains_HardLeft(t *testing.T) {
	t.Parallel()
	gainL, gainR := BalanceGains(-1.0)
	require.Equal(t, float32(1.0), gainL, "hard left L")
	require.InDelta(t, 0.0, float64(gainR), 1e-6, "hard left R")
}

func TestBalanceGains_HardRight(t *testing.T) {
	t.Parallel()
	gainL, gainR := BalanceGains(1.0)
	require.InDelta(t, 0.0, float64(gainL), 1e-6, "hard right L")
	require.Equal(t, float32(1.0), gainR, "hard right R")
}

func TestBalanceGains_HalfLeft(t *testing.T) {
	t.Parallel()
	gainL, gainR := BalanceGains(-0.5)
	require.Equal(t, float32(1.0), gainL, "half left L")
	// cos(0.5 * pi/2) = cos(pi/4) ~ 0.7071
	expected := float32(math.Cos(0.5 * math.Pi / 2.0))
	require.InDelta(t, float64(expected), float64(gainR), 1e-6, "half left R")
}

func TestBalanceGains_HalfRight(t *testing.T) {
	t.Parallel()
	gainL, gainR := BalanceGains(0.5)
	expected := float32(math.Cos(0.5 * math.Pi / 2.0))
	require.InDelta(t, float64(expected), float64(gainL), 1e-6, "half right L")
	require.Equal(t, float32(1.0), gainR, "half right R")
}

func TestBalanceGains_Symmetry(t *testing.T) {
	t.Parallel()
	// Balance should be symmetric: L gain at +x == R gain at -x
	for _, x := range []float64{0.1, 0.25, 0.5, 0.75, 0.9, 1.0} {
		lPlus, _ := BalanceGains(x)
		_, rMinus := BalanceGains(-x)
		require.InDelta(t, float64(rMinus), float64(lPlus), 1e-6,
			"symmetry broken at %.2f", x)
	}
}

func TestSetBalance_ValidatesRange(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(f *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Valid values
	require.NoError(t, m.SetBalance("cam1", 0.0))
	require.NoError(t, m.SetBalance("cam1", -1.0))
	require.NoError(t, m.SetBalance("cam1", 1.0))

	// Invalid values
	require.Error(t, m.SetBalance("cam1", -1.1))
	require.Error(t, m.SetBalance("cam1", 1.1))

	// Unknown source
	require.Error(t, m.SetBalance("unknown", 0.5))
}

func TestSetBalance_UpdatesCachedGains(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(f *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	require.NoError(t, m.SetBalance("cam1", -0.5))

	m.mu.RLock()
	ch := m.channels["cam1"]
	balL := ch.balL
	balR := ch.balR
	balance := ch.balance
	m.mu.RUnlock()

	require.Equal(t, -0.5, balance)
	require.Equal(t, float32(1.0), balL)
	expectedR := float32(math.Cos(0.5 * math.Pi / 2.0))
	require.InDelta(t, float64(expectedR), float64(balR), 1e-6)
}

func TestBalance_IngestPCM_HardLeftZerosRight(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(f *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Set balance hard left -- should zero out the right channel
	require.NoError(t, m.SetBalance("cam1", -1.0))

	// Stereo PCM: [L, R, L, R, ...] with 0.5 on all samples
	pcm := make([]float32, 2048) // 1024 stereo samples
	for i := range pcm {
		pcm[i] = 0.5
	}

	m.IngestPCM("cam1", pcm, 0, 2)

	// Read from ring buffer
	m.mu.RLock()
	ch := m.channels["cam1"]
	got := ch.ringBuf.Pop(2048)
	m.mu.RUnlock()

	require.NotEmpty(t, got, "ring buffer should have samples")

	// Verify: L samples should be at original level (0.5), R samples should be ~0.0
	for i := 0; i < len(got)-1; i += 2 {
		require.InDelta(t, 0.5, float64(got[i]), 1e-6, "L sample %d", i)
		require.InDelta(t, 0.0, float64(got[i+1]), 1e-6, "R sample %d", i+1)
	}
}

func TestBalance_CenterTransparency(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(f *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Balance at center (default) -- output should equal input
	pcm := make([]float32, 2048)
	for i := range pcm {
		pcm[i] = float32(i) * 0.001
	}

	// Copy the original for comparison
	original := make([]float32, len(pcm))
	copy(original, pcm)

	m.IngestPCM("cam1", pcm, 0, 2)

	m.mu.RLock()
	ch := m.channels["cam1"]
	got := ch.ringBuf.Pop(2048)
	m.mu.RUnlock()

	require.Len(t, got, 2048, "should pop all samples from ring buffer")

	// At center balance, output should match input (trim is 0dB = unity)
	for i := 0; i < len(got); i++ {
		require.InDelta(t, float64(original[i]), float64(got[i]), 1e-6,
			"sample %d: transparency violated", i)
	}
}

func TestBalance_ChannelStatesIncludesBalance(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(f *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	require.NoError(t, m.SetBalance("cam1", 0.75))

	states := m.ChannelStates()
	ac, ok := states["cam1"]
	require.True(t, ok, "cam1 not in channel states")
	require.Equal(t, 0.75, ac.Balance)
}

func TestBalance_DebugSnapshotIncludesBalance(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(f *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	require.NoError(t, m.SetBalance("cam1", -0.3))

	snap := m.DebugSnapshot()
	channels, ok := snap["channels"].(map[string]any)
	require.True(t, ok, "no channels in debug snapshot")
	cam1, ok := channels["cam1"].(map[string]any)
	require.True(t, ok, "no cam1 in debug snapshot channels")
	require.Equal(t, -0.3, cam1["balance"])
}

func BenchmarkBalanceGains(b *testing.B) {
	for i := 0; i < b.N; i++ {
		BalanceGains(float64(i%200-100) / 100.0)
	}
}
