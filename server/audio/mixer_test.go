package audio

import (
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

// mockEncoderCapture captures the PCM input passed to Encode.
type mockEncoderCapture struct {
	mu     sync.Mutex
	pcmRef *[]float32
}

func (m *mockEncoderCapture) Encode(pcm []float32) ([]byte, error) {
	m.mu.Lock()
	*m.pcmRef = append([]float32{}, pcm...)
	m.mu.Unlock()
	return []byte{0xFF}, nil
}
func (m *mockEncoderCapture) Close() error { return nil }

// mockEncoderCallCounter counts calls to Encode for verifying priming behavior.
type mockEncoderCallCounter struct {
	mu        sync.Mutex
	callCount int
	lastPCM   []float32
}

func (m *mockEncoderCallCounter) Encode(pcm []float32) ([]byte, error) {
	m.mu.Lock()
	m.callCount++
	m.lastPCM = append([]float32{}, pcm...)
	m.mu.Unlock()
	return []byte{0xFF}, nil
}
func (m *mockEncoderCallCounter) Close() error { return nil }
func (m *mockEncoderCallCounter) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount
}

func TestMixerCloseIdempotent(t *testing.T) {
	t.Parallel()
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(_ *media.AudioFrame) {},
	})
	require.NoError(t, m.Close())
	require.NoError(t, m.Close(), "second Close must not panic")
}

// TestMixerCloseConcurrentNoDoubleClose verifies that calling Close()
// concurrently from multiple goroutines closes each decoder and the
// encoder exactly once (not zero, not twice).

func TestMixerIgnoresInactiveChannel(t *testing.T) {
	var output []*media.AudioFrame

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) { output = append(output, frame) },
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	// cam1 is inactive (not activated) — frames should be dropped
	frame := &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	m.IngestFrame("cam1", frame)

	require.Equal(t, 0, len(output), "inactive channel should not output frames")
}


func TestMixerMutedChannelSilent(t *testing.T) {
	var output []*media.AudioFrame

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) { output = append(output, frame) },
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	_ = m.SetMuted("cam1", true)

	frame := &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	m.IngestFrame("cam1", frame)

	// Muted channel: in passthrough mode, no output since single source is muted
	require.Equal(t, 0, len(output), "muted channel should produce no output")
}


func TestMixerGainApplication(t *testing.T) {
	require.InDelta(t, 1.0, DBToLinear(0), 0.001)
	require.InDelta(t, 0.5012, DBToLinear(-6.0), 0.001)
	require.InDelta(t, 0.1, DBToLinear(-20.0), 0.001)
	require.InDelta(t, 2.0, DBToLinear(6.02), 0.01)
	require.Equal(t, 0.0, DBToLinear(math.Inf(-1)))
}


func TestSetMasterLevelRejectsNaN(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	err := m.SetMasterLevel(math.NaN())
	require.Error(t, err)
	require.Contains(t, err.Error(), "finite")
}


func TestSetMasterLevelRejectsInf(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	err := m.SetMasterLevel(math.Inf(1))
	require.Error(t, err)
	require.Contains(t, err.Error(), "finite")

	err = m.SetMasterLevel(math.Inf(-1))
	require.Error(t, err)
	require.Contains(t, err.Error(), "finite")
}


func TestSetMasterLevelRejectsOutOfRange(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	err := m.SetMasterLevel(-101)
	require.Error(t, err)
	require.Contains(t, err.Error(), "out of range")

	err = m.SetMasterLevel(21)
	require.Error(t, err)
	require.Contains(t, err.Error(), "out of range")
}


func TestSetMasterLevelAcceptsValidRange(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	for _, level := range []float64{-100, -60, 0, 10, 20} {
		require.NoError(t, m.SetMasterLevel(level), "level %f should be accepted", level)
	}
}


func TestMixerEnsureEncoderPrimesOnTransitionStart(t *testing.T) {
	// Verify that OnTransitionStart also primes the encoder.
	var enc *mockEncoderCallCounter

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: nil}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			enc = &mockEncoderCallCounter{}
			return enc, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	m.OnTransitionStart("cam1", "cam2", Crossfade, 1000)

	require.NotNil(t, enc, "encoder should have been created by OnTransitionStart")
	require.GreaterOrEqual(t, enc.CallCount(), 1, "encoder should be primed with silent frame")
}


func TestMixerSetAFV(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")

	err := m.SetAFV("cam1", true)
	require.NoError(t, err)

	err = m.SetAFV("nonexistent", true)
	require.Error(t, err, "SetAFV on unknown channel should error")
}


func TestMixerAFVActivatesOnCut(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")

	// Both channels have AFV enabled
	_ = m.SetAFV("cam1", true)
	_ = m.SetAFV("cam2", true)

	// Initially neither is active (no program source set yet)
	require.False(t, m.IsChannelActive("cam1"))
	require.False(t, m.IsChannelActive("cam2"))

	// cam1 goes to program → cam1 activates, cam2 stays inactive
	m.OnProgramChange("cam1")
	require.True(t, m.IsChannelActive("cam1"), "AFV channel on program should be active")
	require.False(t, m.IsChannelActive("cam2"), "AFV channel not on program should be inactive")

	// cam2 goes to program → cam2 activates, cam1 deactivates
	m.OnProgramChange("cam2")
	require.False(t, m.IsChannelActive("cam1"), "cam1 should deactivate when leaving program")
	require.True(t, m.IsChannelActive("cam2"), "cam2 should activate on program")
}


func TestMixerAFVDisabledChannelStaysActive(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("music")

	// cam1 has AFV, music does not
	_ = m.SetAFV("cam1", true)
	// music is manually activated and stays on regardless of program changes
	m.SetActive("music", true)

	require.True(t, m.IsChannelActive("music"), "non-AFV channel should be active")

	// Program changes should not affect non-AFV channels
	m.OnProgramChange("cam1")
	require.True(t, m.IsChannelActive("cam1"), "AFV cam1 on program")
	require.True(t, m.IsChannelActive("music"), "non-AFV music stays active")

	m.OnProgramChange("cam1") // stay on cam1
	require.True(t, m.IsChannelActive("music"), "non-AFV music still active")
}


func TestMixerAFVToggledOff(t *testing.T) {
	// When AFV is toggled off, channel keeps its current active state
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	_ = m.SetAFV("cam1", true)

	// Activate via program change
	m.OnProgramChange("cam1")
	require.True(t, m.IsChannelActive("cam1"))

	// Turn off AFV — channel stays active (was already active)
	_ = m.SetAFV("cam1", false)
	require.True(t, m.IsChannelActive("cam1"), "turning off AFV should not deactivate")

	// Now program changes should not affect cam1
	m.OnProgramChange("cam2") // cam2 doesn't exist, but that's fine
	require.True(t, m.IsChannelActive("cam1"), "non-AFV channel unaffected by program change")
}


func TestMixerIsChannelActive(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	// Unknown channel returns false
	require.False(t, m.IsChannelActive("unknown"))

	m.AddChannel("cam1")
	require.False(t, m.IsChannelActive("cam1"), "new channel starts inactive")

	m.SetActive("cam1", true)
	require.True(t, m.IsChannelActive("cam1"))
}


func TestMixerOnTransitionStart(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	// Before transition, no crossfade active
	require.False(t, m.IsInTransitionCrossfade())

	// Start transition
	m.OnTransitionStart("cam1", "cam2", Crossfade, 1000)

	require.True(t, m.IsInTransitionCrossfade(), "transition crossfade should be active")
	require.InDelta(t, 0.0, m.TransitionPosition(), 0.001, "position should start at 0")

	// Both channels should be considered active during transition
	// The new source channel must be activated so audio frames are accepted
	m.mu.RLock()
	ch2 := m.channels["cam2"]
	active := ch2.active
	m.mu.RUnlock()
	require.True(t, active, "new source channel should be activated on transition start")
}


func TestMixerOnTransitionPosition(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	m.OnTransitionStart("cam1", "cam2", Crossfade, 1000)

	// Update position
	m.OnTransitionPosition(0.25)
	require.InDelta(t, 0.25, m.TransitionPosition(), 0.001)

	m.OnTransitionPosition(0.75)
	require.InDelta(t, 0.75, m.TransitionPosition(), 0.001)
}


func TestMixerOnTransitionComplete(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	m.OnTransitionStart("cam1", "cam2", Crossfade, 1000)
	m.OnTransitionPosition(0.5)
	require.True(t, m.IsInTransitionCrossfade())

	m.OnTransitionComplete()

	require.False(t, m.IsInTransitionCrossfade(), "transition crossfade should be cleared")
	require.InDelta(t, 0.0, m.TransitionPosition(), 0.001, "position should be reset")
}


func TestMixerOnTransitionAbortSnapsPosition(t *testing.T) {
	// OnTransitionAbort should snap position to 0.0 before clearing state,
	// preventing an audio discontinuity when the T-bar is pulled back.
	var capturedPCM []float32
	var outputFrames []*media.AudioFrame

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: nil}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	// Start transition and move to halfway
	m.OnTransitionStart("cam1", "cam2", Crossfade, 1000)
	m.OnTransitionPosition(0.5)
	require.True(t, m.IsInTransitionCrossfade())

	// Ingest frames to start a mix cycle
	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: make([]float32, 2048)}
	m.channels["cam2"].decoder = &mockDecoder{samples: make([]float32, 2048)}
	m.mu.Unlock()

	frame := &media.AudioFrame{PTS: 2000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	m.IngestFrame("cam1", frame)
	m.IngestFrame("cam2", frame)

	// Now abort — should snap to position 0 and clear state
	m.OnTransitionAbort()

	require.False(t, m.IsInTransitionCrossfade(), "transition should be cleared after abort")
	require.InDelta(t, 0.0, m.TransitionPosition(), 0.001, "position should be reset after abort")
}


func TestMixerTransitionCrossfadeGains(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	m.OnTransitionStart("cam1", "cam2", Crossfade, 1000)

	// At position 0.0: old=1.0, new=0.0
	m.OnTransitionPosition(0.0)
	oldGain, newGain := m.TransitionGains()
	require.InDelta(t, 1.0, oldGain, 0.001, "old gain at 0.0")
	require.InDelta(t, 0.0, newGain, 0.001, "new gain at 0.0")

	// At position 0.5: old≈0.707, new≈0.707
	m.OnTransitionPosition(0.5)
	oldGain, newGain = m.TransitionGains()
	require.InDelta(t, 0.707, oldGain, 0.001, "old gain at 0.5")
	require.InDelta(t, 0.707, newGain, 0.001, "new gain at 0.5")

	// At position 1.0: old=0.0, new=1.0
	m.OnTransitionPosition(1.0)
	oldGain, newGain = m.TransitionGains()
	require.InDelta(t, 0.0, oldGain, 0.001, "old gain at 1.0")
	require.InDelta(t, 1.0, newGain, 0.001, "new gain at 1.0")
}


func TestMixerTransitionGainsNotActive(t *testing.T) {
	// When no transition is active, gains should return 1.0, 0.0
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	oldGain, newGain := m.TransitionGains()
	require.InDelta(t, 1.0, oldGain, 0.001, "old gain when inactive")
	require.InDelta(t, 0.0, newGain, 0.001, "new gain when inactive")
}


func TestMixerTransitionFTBReverseGains(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// FTB reverse: fade the "from" source IN from silence
	m.OnTransitionStart("cam1", "", FadeIn, 1000)

	// At position 0.0: audio should be silent (starting from black)
	m.OnTransitionPosition(0.0)
	oldGain, newGain := m.TransitionGains()
	require.InDelta(t, 0.0, oldGain, 0.001, "FTB reverse at 0.0: from source should be silent")
	require.InDelta(t, 0.0, newGain, 0.001, "FTB reverse: no 'to' source")

	// At position 0.5: audio should be at ~0.707 (fading in)
	m.OnTransitionPosition(0.5)
	oldGain, newGain = m.TransitionGains()
	require.InDelta(t, 0.707, oldGain, 0.001, "FTB reverse at 0.5: from source fading in")
	require.InDelta(t, 0.0, newGain, 0.001, "FTB reverse: no 'to' source")

	// At position 1.0: audio should be at full volume
	m.OnTransitionPosition(1.0)
	oldGain, newGain = m.TransitionGains()
	require.InDelta(t, 1.0, oldGain, 0.001, "FTB reverse at 1.0: from source fully in")
	require.InDelta(t, 0.0, newGain, 0.001, "FTB reverse: no 'to' source")
}


func TestMixerTransitionFTBForwardGains(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// FTB forward: fade the "from" source OUT to silence
	m.OnTransitionStart("cam1", "", FadeOut, 1000)

	// At position 0.0: audio should be full
	m.OnTransitionPosition(0.0)
	oldGain, _ := m.TransitionGains()
	require.InDelta(t, 1.0, oldGain, 0.001, "FTB forward at 0.0: full volume")

	// At position 0.5: fading out
	m.OnTransitionPosition(0.5)
	oldGain, _ = m.TransitionGains()
	require.InDelta(t, 0.707, oldGain, 0.001, "FTB forward at 0.5: fading out")

	// At position 1.0: silent
	m.OnTransitionPosition(1.0)
	oldGain, _ = m.TransitionGains()
	require.InDelta(t, 0.0, oldGain, 0.001, "FTB forward at 1.0: silent")
}

// Program mute silences output when FTB is held.


func TestMixerTransitionDipGains(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	m.OnTransitionStart("cam1", "cam2", DipToSilence, 1000)

	// Position 0.0: fully source A
	m.OnTransitionPosition(0.0)
	oldGain, newGain := m.TransitionGains()
	require.InDelta(t, 1.0, oldGain, 0.001, "dip at 0.0: source A full")
	require.InDelta(t, 0.0, newGain, 0.001, "dip at 0.0: source B silent")

	// Position 0.25: source A fading out, source B still silent
	m.OnTransitionPosition(0.25)
	oldGain, newGain = m.TransitionGains()
	require.InDelta(t, 0.707, oldGain, 0.001, "dip at 0.25: source A fading")
	require.InDelta(t, 0.0, newGain, 0.001, "dip at 0.25: source B still silent")

	// Position 0.5: both sources SILENT (the dip midpoint)
	m.OnTransitionPosition(0.5)
	oldGain, newGain = m.TransitionGains()
	require.InDelta(t, 0.0, oldGain, 0.001, "dip at 0.5: source A silent")
	require.InDelta(t, 0.0, newGain, 0.001, "dip at 0.5: source B silent")

	// Position 0.75: source A gone, source B fading in
	m.OnTransitionPosition(0.75)
	oldGain, newGain = m.TransitionGains()
	require.InDelta(t, 0.0, oldGain, 0.001, "dip at 0.75: source A gone")
	require.InDelta(t, 0.707, newGain, 0.001, "dip at 0.75: source B fading in")

	// Position 1.0: fully source B
	m.OnTransitionPosition(1.0)
	oldGain, newGain = m.TransitionGains()
	require.InDelta(t, 0.0, oldGain, 0.001, "dip at 1.0: source A gone")
	require.InDelta(t, 1.0, newGain, 0.001, "dip at 1.0: source B full")
}


func TestMixerTickerProducesOutputWithPartialChannels(t *testing.T) {
	// Two active channels, but only one has data in its ring buffer.
	// The clock-driven ticker still produces output regardless.
	var mu sync.Mutex
	var outputFrames []*media.AudioFrame

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			mu.Lock()
			outputFrames = append(outputFrames, frame)
			mu.Unlock()
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	// Only push data to cam1's ring buffer — cam2 has no data
	pcm := make([]float32, 1024*2)
	for i := range pcm {
		pcm[i] = 0.5
	}
	m.mu.Lock()
	m.channels["cam1"].ringBuf.Push(pcm)
	m.mu.Unlock()

	// Wait for ticker to fire
	time.Sleep(100 * time.Millisecond)

	// Output should have been produced despite channel 2 being empty
	mu.Lock()
	count := len(outputFrames)
	mu.Unlock()
	require.GreaterOrEqual(t, count, 1, "ticker should produce output even if one channel has no data")
}


func TestChannelDecoderInitOnce(t *testing.T) {
	// Verify that the decoder factory is called exactly once per channel,
	// even when multiple goroutines call IngestFrame concurrently.
	const goroutines = 20

	var factoryCalls atomic.Int64

	var mu sync.Mutex
	var outputFrames []*media.AudioFrame

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			mu.Lock()
			outputFrames = append(outputFrames, frame)
			mu.Unlock()
		},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			factoryCalls.Add(1)
			return &mockDecoder{samples: []float32{0.5, 0.5}}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Set non-zero master level so the mixing path's decoder init is exercised.
	require.NoError(t, m.SetMasterLevel(-1.0))

	var wg sync.WaitGroup
	wg.Add(goroutines)
	start := make(chan struct{})

	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			<-start // synchronize all goroutines
			frame := &media.AudioFrame{
				PTS:        int64(1000 + i),
				Data:       []byte{0xAA},
				SampleRate: 48000,
				Channels:   2,
			}
			m.IngestFrame("cam1", frame)
		}(i)
	}

	close(start) // release all goroutines at once
	wg.Wait()

	// The decoder factory must have been called exactly once for "cam1".
	require.Equal(t, int64(1), factoryCalls.Load(),
		"decoder factory should be called exactly once per channel, got %d", factoryCalls.Load())
}


func TestMixerRemoveChannelCleansUpPCMBuffer(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(f *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: []float32{0.5, 0.5}}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	require.NoError(t, m.SetMasterLevel(-1.0)) // force mixing path

	// Ingest a frame to populate lastDecodedPCM
	m.IngestFrame("cam1", &media.AudioFrame{PTS: 0, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})

	m.mu.RLock()
	_, hasPCM := m.lastDecodedPCM["cam1"]
	m.mu.RUnlock()
	require.True(t, hasPCM, "lastDecodedPCM should be populated after IngestFrame")

	// Remove channel — should clean up the PCM buffer
	m.RemoveChannel("cam1")

	m.mu.RLock()
	_, hasPCM = m.lastDecodedPCM["cam1"]
	m.mu.RUnlock()
	require.False(t, hasPCM, "lastDecodedPCM should be cleaned up after RemoveChannel")
}


func TestMixerTrimRangeValidation(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(f *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")

	require.Error(t, m.SetTrim("cam1", -25.0), "trim below -20 should error")
	require.Error(t, m.SetTrim("cam1", 25.0), "trim above +20 should error")
	require.NoError(t, m.SetTrim("cam1", -20.0), "trim at -20 should be OK")
	require.NoError(t, m.SetTrim("cam1", 20.0), "trim at +20 should be OK")
}


func TestMixerPerChannelPeaks(t *testing.T) {
	var outputFrames []*media.AudioFrame

	pcm := []float32{0.5, 0.3, 0.5, 0.3} // L=0.5, R=0.3 interleaved stereo

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: pcm}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Force non-passthrough so the mixing path runs
	require.NoError(t, m.SetMasterLevel(-1.0))

	m.IngestFrame("cam1", &media.AudioFrame{PTS: 0, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})

	// Channel peaks should be populated
	states := m.ChannelStates()
	ch, ok := states["cam1"]
	require.True(t, ok)
	require.True(t, ch.PeakL > -96 || ch.PeakR > -96,
		"per-channel peaks should be populated after frame ingestion")
}


func TestChannelDecoderInitOnceCrossfade(t *testing.T) {
	// Verify sync.Once works for the crossfade path too.
	var factoryCalls atomic.Int64

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			factoryCalls.Add(1)
			return &mockDecoder{samples: []float32{0.5, 0.5}}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	// Trigger crossfade
	m.OnCut("cam1", "cam2")

	// Ingest frames from both sources — each channel's decoder should init once
	m.IngestFrame("cam1", &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})
	m.IngestFrame("cam2", &media.AudioFrame{PTS: 1000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2})

	// Two channels, each initialized once = 2 factory calls total
	require.Equal(t, int64(2), factoryCalls.Load(),
		"decoder factory should be called once per channel (2 channels), got %d", factoryCalls.Load())

	// Now do another crossfade — factories should NOT be called again
	m.OnCut("cam2", "cam1")
	m.IngestFrame("cam2", &media.AudioFrame{PTS: 2000, Data: []byte{0xCC}, SampleRate: 48000, Channels: 2})
	m.IngestFrame("cam1", &media.AudioFrame{PTS: 2000, Data: []byte{0xDD}, SampleRate: 48000, Channels: 2})

	require.Equal(t, int64(2), factoryCalls.Load(),
		"decoder factory should not be called again on subsequent crossfades, got %d", factoryCalls.Load())
}

// TestMixerPassthroughRaceSafety exercises the RLock-to-Lock upgrade in
// IngestFrame. One goroutine ingests frames while another toggles passthrough
// by activating a second channel. With -race this detects the TOCTOU bug
// where passthrough is checked under RLock then re-acquired under Lock.

func TestMixer_SetProgramMute_ResetsEnvelopes(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Directly drive the limiter and compressor with loud signal
	// to build up envelope state.
	loudSamples := make([]float32, 2048)
	for i := range loudSamples {
		loudSamples[i] = 2.0
	}
	m.limiter.Process(loudSamples)
	require.Greater(t, m.limiter.GainReduction(), 0.0, "limiter should have engaged")

	// Also drive the per-channel compressor
	m.mu.RLock()
	ch := m.channels["cam1"]
	m.mu.RUnlock()
	require.NoError(t, ch.compressor.SetParams(-6, 4.0, 0.1, 100.0, 0))
	ch.compressor.Process(loudSamples)
	require.Greater(t, ch.compressor.GainReduction(), 0.0, "compressor should have engaged")

	// Mute (FTB) should reset limiter and all compressor envelopes
	m.SetProgramMute(true)

	require.InDelta(t, 0.0, m.limiter.GainReduction(), 0.001,
		"limiter GR should be 0 after mute/reset")
	require.InDelta(t, 0.0, ch.compressor.GainReduction(), 0.001,
		"compressor GR should be 0 after mute/reset")
}


func TestGrowBuf(t *testing.T) {
	// nil input — should allocate new slice
	buf := growBuf(nil, 10)
	require.Equal(t, 10, len(buf))
	require.GreaterOrEqual(t, cap(buf), 10)

	// Reuse when capacity sufficient
	original := buf
	buf = growBuf(buf, 5)
	require.Equal(t, 5, len(buf))
	// Should be the same underlying array (reused)
	require.Equal(t, cap(original), cap(buf), "should reuse existing capacity")

	// New alloc when capacity insufficient
	buf = growBuf(buf, 100)
	require.Equal(t, 100, len(buf))
	require.GreaterOrEqual(t, cap(buf), 100)

	// Zero length
	buf = growBuf(buf, 0)
	require.Equal(t, 0, len(buf))
}

func BenchmarkMixerMixingPath(b *testing.B) {
	// Measures allocations per frame in the mixing hot path.
	// Per-channel processing allocs (trim/gain/stored buffers) are 0 in steady state.
	// Remaining allocs are unavoidable: output media.AudioFrame + encoded AAC data.
	pcm := make([]float32, 2048)
	for i := range pcm {
		pcm[i] = 0.5
	}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: pcm}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	_ = m.SetTrim("cam1", 1.0) // force mixing path

	// Warm up: run a few frames to initialize all buffers
	for i := 0; i < 10; i++ {
		m.IngestFrame("cam1", &media.AudioFrame{
			PTS: int64(i * 1920), Data: []byte{0xAA}, SampleRate: 48000, Channels: 2,
		})
	}

	frame := &media.AudioFrame{PTS: 100000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		frame.PTS = int64(100000 + i*1920)
		m.IngestFrame("cam1", frame)
	}
}


func TestStereoGainInterpolation(t *testing.T) {
	t.Parallel()
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(f *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	m.mu.Lock()
	m.transCrossfadeActive = true
	m.transCrossfadeFrom = "cam1"
	m.transCrossfadeTo = "cam2"
	m.transCrossfadeMode = Crossfade
	m.transCrossfadeAudioPos = 0.0
	m.mixCycleTransPos = 1.0

	// Create a stereo buffer: L=1.0, R=0.5 for all sample pairs
	numPairs := 512
	trimmedPCM := make([]float32, numPairs*2)
	for i := 0; i < numPairs; i++ {
		trimmedPCM[i*2] = 1.0   // L
		trimmedPCM[i*2+1] = 0.5 // R
	}

	ch := m.channels["cam1"]
	ch.gainBuf = make([]float32, len(trimmedPCM))
	gainedPCM := ch.gainBuf

	// Apply the transition gain interpolation (same code as IngestFrame)
	gainFn := func(p float64) float64 { return transitionFromGain(m.transCrossfadeMode, p) }
	gStart := float32(gainFn(m.transCrossfadeAudioPos))
	gEnd := float32(gainFn(m.mixCycleTransPos))
	channels := m.numChannels
	pairCount := len(trimmedPCM) / channels
	divisor := float32(pairCount)
	if pairCount > 1 {
		divisor = float32(pairCount - 1)
	}
	for i, s := range trimmedPCM {
		t := float32(i/channels) / divisor
		transGain := gStart + (gEnd-gStart)*t
		gainedPCM[i] = s * ch.levelLinear * transGain
	}
	m.mu.Unlock()

	// Verify that L and R at the same time position received identical gain
	for i := 0; i < numPairs; i++ {
		lSample := gainedPCM[i*2]
		rSample := gainedPCM[i*2+1]
		// L input was 1.0, R input was 0.5, so R should be exactly half of L
		// if the same gain was applied to both
		if lSample != 0 {
			ratio := rSample / lSample
			require.InDelta(t, 0.5, float64(ratio), 1e-5,
				"pair %d: L/R ratio should be 0.5 (identical gain), got %v", i, ratio)
		}
	}
}


func TestTransitionGainInterpolation_LastPairReachesTarget(t *testing.T) {
	// Verify that the transition gain interpolation ramp reaches gEnd on the
	// The interpolation ramp reaches gEnd exactly on the last sample pair using (pairCount-1) denominator.
	t.Parallel()

	// Simulate the gain interpolation logic from mixFrameLocked.
	channels := 2
	numPairs := 512
	buf := make([]float32, numPairs*channels)
	for i := range buf {
		buf[i] = 1.0 // uniform input
	}

	gStart := float32(1.0) // from gain at position 0
	gEnd := float32(0.0)   // from gain at position 1 (cos(pi/2) = 0)

	gained := make([]float32, len(buf))
	pairCount := len(buf) / channels
	for i, s := range buf {
		var tVal float32
		if pairCount > 1 {
			tVal = float32(i/channels) / float32(pairCount-1)
		}
		transGain := gStart + (gEnd-gStart)*tVal
		gained[i] = s * transGain
	}

	// First pair should have gain = gStart = 1.0
	require.InDelta(t, float64(gStart), float64(gained[0]), 1e-6,
		"first sample should have gain = gStart")
	require.InDelta(t, float64(gStart), float64(gained[1]), 1e-6,
		"first sample R should have gain = gStart")

	// Last pair should have gain = gEnd = 0.0 exactly
	lastL := gained[len(gained)-2]
	lastR := gained[len(gained)-1]
	require.InDelta(t, float64(gEnd), float64(lastL), 1e-6,
		"last sample L should have gain = gEnd exactly, got %f", lastL)
	require.InDelta(t, float64(gEnd), float64(lastR), 1e-6,
		"last sample R should have gain = gEnd exactly, got %f", lastR)
}


func TestChannelGainCached(t *testing.T) {
	t.Parallel()
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(f *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")

	// Default: 0dB level and 0dB trim should cache as 1.0 linear
	m.mu.RLock()
	ch := m.channels["cam1"]
	require.InDelta(t, 1.0, float64(ch.levelLinear), 1e-6, "default level should cache as 1.0")
	require.InDelta(t, 1.0, float64(ch.trimLinear), 1e-6, "default trim should cache as 1.0")
	m.mu.RUnlock()

	// Set level to -6 dB and verify cached value
	err := m.SetLevel("cam1", -6)
	require.NoError(t, err)
	m.mu.RLock()
	ch = m.channels["cam1"]
	expected := float32(DBToLinear(-6))
	require.InDelta(t, float64(expected), float64(ch.levelLinear), 1e-6, "levelLinear should match DBToLinear(-6)")
	m.mu.RUnlock()

	// Set trim to +3 dB and verify cached value
	err = m.SetTrim("cam1", 3)
	require.NoError(t, err)
	m.mu.RLock()
	ch = m.channels["cam1"]
	expectedTrim := float32(DBToLinear(3))
	require.InDelta(t, float64(expectedTrim), float64(ch.trimLinear), 1e-6, "trimLinear should match DBToLinear(3)")
	m.mu.RUnlock()

	// Set master level to -3 dB and verify cached value
	require.NoError(t, m.SetMasterLevel(-3))
	m.mu.RLock()
	expectedMaster := float32(DBToLinear(-3))
	require.InDelta(t, float64(expectedMaster), float64(m.masterLinear), 1e-6, "masterLinear should match DBToLinear(-3)")
	m.mu.RUnlock()
}


func TestMixer_SetStingerAudio(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	audio := []float32{0.1, 0.2, 0.3, 0.4, 0.5, 0.6}
	m.SetStingerAudio(audio, 48000, 2)

	m.mu.Lock()
	require.Equal(t, audio, m.stingerAudio, "stingerAudio should be stored")
	require.Equal(t, 0, m.stingerOffset, "stingerOffset should be reset to 0")
	require.Equal(t, 2, m.stingerChannels, "stingerChannels should be stored")
	m.mu.Unlock()

	// Calling again should reset offset
	m.mu.Lock()
	m.stingerOffset = 3 // simulate partial consumption
	m.mu.Unlock()

	audio2 := []float32{0.7, 0.8}
	m.SetStingerAudio(audio2, 48000, 2)

	m.mu.Lock()
	require.Equal(t, audio2, m.stingerAudio, "stingerAudio should be replaced")
	require.Equal(t, 0, m.stingerOffset, "stingerOffset should be reset on new call")
	m.mu.Unlock()
}


func TestMixer_SetStingerAudio_SampleRateMismatch(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	audio := []float32{0.1, 0.2, 0.3}
	m.SetStingerAudio(audio, 44100, 2) // different sample rate → resampled, not rejected

	m.mu.RLock()
	stinger := m.stingerAudio
	m.mu.RUnlock()
	require.NotNil(t, stinger, "stingerAudio should be resampled, not rejected")
}


func TestMixer_SetStingerAudio_ChannelMismatch(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	audio := []float32{0.1, 0.2, 0.3}
	m.SetStingerAudio(audio, 48000, 1) // wrong channel count

	m.mu.Lock()
	require.Nil(t, m.stingerAudio, "stingerAudio should be nil on channel mismatch")
	m.mu.Unlock()
}


func TestMixer_AddStingerAudio_Basic(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	// Create stinger audio that's long enough to avoid fade regions for simple testing
	// With 48kHz stereo, fade-in is 10ms = 960 samples, fade-out is 50ms = 4800 samples
	// We need enough samples to have a "middle" region at full gain
	totalSamples := 960 + 100 + 4800 // fade-in + middle + fade-out
	stingerPCM := make([]float32, totalSamples)
	for i := range stingerPCM {
		stingerPCM[i] = 0.5
	}

	m.mu.Lock()
	m.stingerAudio = stingerPCM
	m.stingerOffset = 960 // skip past fade-in
	m.stingerChannels = 2

	// Mix buffer with some existing audio
	mixed := make([]float32, 100)
	for i := range mixed {
		mixed[i] = 0.3
	}

	m.addStingerAudio(mixed)

	// After addStingerAudio, mixed should have stinger added: 0.3 + 0.5 = 0.8
	for i := 0; i < 100; i++ {
		require.InDelta(t, 0.8, mixed[i], 0.01,
			"sample %d: should be source (0.3) + stinger (0.5)", i)
	}
	require.Equal(t, 960+100, m.stingerOffset, "offset should advance by mixed length")
	m.mu.Unlock()
}


func TestMixer_AddStingerAudio_FadeEnvelope(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	// With 48kHz stereo: fadeIn = 48000 * 2 * 10 / 1000 = 960 samples
	// fadeOut = 48000 * 2 * 50 / 1000 = 4800 samples
	fadeInSamples := 960
	fadeOutSamples := 4800
	totalSamples := fadeInSamples + 1000 + fadeOutSamples // some middle section

	stingerPCM := make([]float32, totalSamples)
	for i := range stingerPCM {
		stingerPCM[i] = 1.0 // unity to make fade easy to verify
	}

	// Test fade-in: first samples should be attenuated
	m.mu.Lock()
	m.stingerAudio = stingerPCM
	m.stingerOffset = 0
	m.stingerChannels = 2

	mixed := make([]float32, fadeInSamples)
	m.addStingerAudio(mixed)

	// First sample should be near 0 (pos=0, gain=0/960=0)
	require.InDelta(t, 0.0, mixed[0], 0.01, "first sample should be near zero (fade-in start)")

	// Midpoint of fade-in should be ~0.5
	midIdx := fadeInSamples / 2
	require.InDelta(t, 0.5, mixed[midIdx], 0.05, "midpoint of fade-in should be ~0.5")

	// Last sample of fade-in should be near 1.0
	require.InDelta(t, 1.0, mixed[fadeInSamples-1], 0.01,
		"last sample of fade-in should be near 1.0")
	m.mu.Unlock()

	// Test fade-out: position near the end
	m.mu.Lock()
	m.stingerAudio = stingerPCM
	m.stingerOffset = totalSamples - fadeOutSamples
	m.stingerChannels = 2

	mixed2 := make([]float32, fadeOutSamples)
	m.addStingerAudio(mixed2)

	// First sample of fade-out region should be near 1.0
	require.InDelta(t, 1.0, mixed2[0], 0.01,
		"first sample of fade-out should be near 1.0")

	// Last sample should be near 0
	require.InDelta(t, 0.0, mixed2[fadeOutSamples-1], 0.01,
		"last sample of fade-out should be near zero")

	// Midpoint should be ~0.5
	fadeMid := fadeOutSamples / 2
	require.InDelta(t, 0.5, mixed2[fadeMid], 0.05,
		"midpoint of fade-out should be ~0.5")
	m.mu.Unlock()
}


func TestMixer_AddStingerAudio_Exhaustion(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	// Small stinger audio
	stingerPCM := []float32{0.1, 0.2, 0.3, 0.4}

	m.mu.Lock()
	m.stingerAudio = stingerPCM
	m.stingerOffset = 0
	m.stingerChannels = 2

	// Mix buffer larger than remaining stinger
	mixed := make([]float32, 10)
	m.addStingerAudio(mixed)

	// Only first 4 samples should have stinger added (with fade envelope)
	// After exhaustion, stingerAudio is NOT set to nil here because we consumed
	// exactly 4 samples and offset is now 4 = len(stingerAudio)
	require.Equal(t, 4, m.stingerOffset, "offset should be at end")

	// Call again — should detect remaining <= 0 and set stingerAudio to nil
	mixed2 := make([]float32, 10)
	m.addStingerAudio(mixed2)
	require.Nil(t, m.stingerAudio, "stingerAudio should be nil after exhaustion")
	m.mu.Unlock()
}


func TestMixer_AddStingerAudio_StereoFadeSymmetry(t *testing.T) {
	// Bug: fade envelope advanced per-sample instead of per sample-frame,
	// causing L and R channels at the same time instant to get different gains.
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	// Create stereo stinger audio with all 1.0 values so gain is directly visible.
	// fade-in is 10ms = 480 sample-frames, fade-out is 50ms = 2400 sample-frames.
	// Use a long enough clip so the regions don't overlap.
	totalFrames := 480 + 2400 + 1000             // fade-in + fade-out + middle
	stingerPCM := make([]float32, totalFrames*2) // stereo
	for i := range stingerPCM {
		stingerPCM[i] = 1.0
	}

	m.mu.Lock()
	m.stingerAudio = stingerPCM
	m.stingerOffset = 0
	m.stingerChannels = 2

	// Process the fade-in region
	mixed := make([]float32, 960) // 480 sample-frames * 2 channels = fade-in region
	m.addStingerAudio(mixed)

	// Verify L and R channels at each time instant get the SAME gain.
	for i := 0; i < len(mixed)-1; i += 2 {
		frameIdx := i / 2
		require.InDelta(t, mixed[i], mixed[i+1], 1e-6,
			"frame %d: L (%f) and R (%f) should have identical fade gain",
			frameIdx, mixed[i], mixed[i+1])
	}

	// Also verify that gain is monotonically increasing during fade-in
	for i := 2; i < len(mixed)-1; i += 2 {
		require.GreaterOrEqual(t, mixed[i], mixed[i-2],
			"fade-in should be monotonically increasing at sample %d", i)
	}
	m.mu.Unlock()
}


func TestMixer_StingerAudioClearedOnComplete(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	// Set up stinger audio
	m.SetStingerAudio([]float32{0.1, 0.2, 0.3}, 48000, 2)

	m.mu.Lock()
	require.NotNil(t, m.stingerAudio)
	m.mu.Unlock()

	// Complete the transition
	m.OnTransitionComplete()

	m.mu.Lock()
	require.Nil(t, m.stingerAudio, "stingerAudio should be nil after OnTransitionComplete")
	require.Equal(t, 0, m.stingerOffset, "stingerOffset should be 0 after OnTransitionComplete")
	require.Equal(t, 0, m.stingerChannels, "stingerChannels should be 0 after OnTransitionComplete")
	m.mu.Unlock()
}


func TestMixerUnmuteFadeInRamp(t *testing.T) {
	// When SetProgramMute transitions from true→false, the mixer should apply
	// a 5ms linear fade-in ramp to prevent uncompressed burst after
	// compressor/limiter envelopes were reset to zero during mute.
	var allCapturedPCM [][]float32

	pcm := make([]float32, 2048)
	for i := range pcm {
		pcm[i] = 0.5
	}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: pcm}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			enc := &mockEncoderCapture{pcmRef: new([]float32)}
			allCapturedPCM = append(allCapturedPCM, *enc.pcmRef)
			return enc, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	// Mute, then unmute to schedule fade-in ramp
	m.SetProgramMute(true)
	m.SetProgramMute(false)

	// Verify unmuteFadeRemaining is set
	m.mu.RLock()
	fadeRemaining := m.unmuteFadeRemaining
	m.mu.RUnlock()
	require.Greater(t, fadeRemaining, 0, "unmute should schedule fade-in ramp")

	// Expected ramp length: sampleRate * channels * 5 / 1000 = 48000 * 2 * 5 / 1000 = 480
	expectedRampLen := 48000 * 2 * 5 / 1000
	require.Equal(t, expectedRampLen, fadeRemaining, "ramp length should be 5ms of samples")
}


func TestMixerUnmuteFadeNotScheduledOnMute(t *testing.T) {
	// SetProgramMute(true) should clear any pending fade-in ramp.
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	// Mute then unmute to schedule ramp
	m.SetProgramMute(true)
	m.SetProgramMute(false)

	m.mu.RLock()
	require.Greater(t, m.unmuteFadeRemaining, 0, "ramp should be scheduled")
	m.mu.RUnlock()

	// Re-muting should clear the ramp
	m.SetProgramMute(true)

	m.mu.RLock()
	require.Equal(t, 0, m.unmuteFadeRemaining, "mute should clear pending fade-in ramp")
	m.mu.RUnlock()
}


func TestMixerResamplerCreatedOncePerChannel(t *testing.T) {
	t.Parallel()

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, channels int) (Decoder, error) {
			return &mockDecoder{samples: []float32{0.5, 0.5}}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (Encoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	m.AddChannel("cam2")
	m.SetActive("cam2", true)

	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: []float32{0.5, 0.5}}
	m.channels["cam2"].decoder = &mockDecoder{samples: []float32{0.3, 0.3}}
	m.mu.Unlock()

	frame := &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 44100, Channels: 2}

	m.IngestFrame("cam1", frame)

	m.mu.RLock()
	resampler1 := m.channels["cam1"].resampler
	m.mu.RUnlock()
	require.NotNil(t, resampler1, "resampler should be created on first mismatch")

	// Second frame should reuse the same resampler
	m.IngestFrame("cam1", frame)

	m.mu.RLock()
	resampler2 := m.channels["cam1"].resampler
	m.mu.RUnlock()
	require.True(t, resampler1 == resampler2, "resampler should be reused, not recreated")
}

// TestMixerResamplerDisablesPassthrough verifies that when a source needs
// resampling, passthrough mode is disabled (can't forward raw AAC at wrong rate).

func TestMixer_TickerProducesOutputWithNoSourceData(t *testing.T) {
	// When no active channel produces audio (e.g., program on a video-only
	// source), the clock-driven ticker still produces output frames at the
	// fixed cadence to keep the browser's audio pipeline fed.
	var mu sync.Mutex
	var frames []*media.AudioFrame

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(f *media.AudioFrame) {
			mu.Lock()
			frames = append(frames, f)
			mu.Unlock()
		},
		EncoderFactory: func(sr, ch int) (Encoder, error) {
			return &mockEncoderCapture{pcmRef: new([]float32)}, nil
		},
	})
	defer func() { _ = m.Close() }()

	// Add a channel and activate it, but never send any audio.
	// This simulates a video-only source on program.
	m.AddChannel("video_only")
	m.SetActive("video_only", true)

	// Wait for the ticker to produce frames (~21ms per frame).
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	count := len(frames)
	mu.Unlock()

	require.GreaterOrEqual(t, count, 2,
		"ticker should produce output frames even when no source audio arrives")
}

