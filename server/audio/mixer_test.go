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

func TestMixerPassthrough(t *testing.T) {
	var mu sync.Mutex
	var output []*media.AudioFrame

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			mu.Lock()
			output = append(output, frame)
			mu.Unlock()
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Ingest a frame — should passthrough (single active source, 0dB, not muted)
	frame := &media.AudioFrame{PTS: 1000, Data: []byte{0xAA, 0xBB}, SampleRate: 48000, Channels: 2}
	m.IngestFrame("cam1", frame)

	mu.Lock()
	require.Equal(t, 1, len(output))
	require.Equal(t, []byte{0xAA, 0xBB}, output[0].Data, "passthrough should forward raw AAC")
	require.Equal(t, int64(1000), output[0].PTS, "first frame should seed from source PTS")
	mu.Unlock()

	// Second frame verifies the monotonic counter advances by frame duration
	frame2 := &media.AudioFrame{PTS: 2920, Data: []byte{0xCC}, SampleRate: 48000, Channels: 2}
	m.IngestFrame("cam1", frame2)

	mu.Lock()
	require.Equal(t, 2, len(output))
	// 1024 samples / 48000 Hz * 90000 = 1920 ticks
	require.Equal(t, int64(1000+1920), output[1].PTS, "second frame should use monotonic PTS")
	mu.Unlock()
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
func TestMixerCloseConcurrentNoDoubleClose(t *testing.T) {
	t.Parallel()

	var decoderCloses atomic.Int64
	var encoderCloses atomic.Int64

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(_ *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, channels int) (AudioDecoder, error) {
			return &closeCountDecoder{closes: &decoderCloses, samples: make([]float32, 1024)}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (AudioEncoder, error) {
			return &closeCountEncoder{closes: &encoderCloses}, nil
		},
	})

	// Add two channels and ingest frames to trigger lazy decoder init.
	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)
	// Two active channels = mixing mode, which also initializes the encoder.
	m.IngestFrame("cam1", &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})
	m.IngestFrame("cam2", &media.AudioFrame{PTS: 1000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2})

	// Call Close() concurrently from 10 goroutines.
	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_ = m.Close()
		}()
	}
	wg.Wait()

	require.Equal(t, int64(2), decoderCloses.Load(),
		"each decoder should be closed exactly once (2 channels)")
	require.Equal(t, int64(1), encoderCloses.Load(),
		"encoder should be closed exactly once")
}

// closeCountDecoder tracks Close() calls via an atomic counter.
type closeCountDecoder struct {
	closes  *atomic.Int64
	samples []float32
}

func (d *closeCountDecoder) Decode([]byte) ([]float32, error) { return d.samples, nil }
func (d *closeCountDecoder) Close() error                     { d.closes.Add(1); return nil }

// closeCountEncoder tracks Close() calls via an atomic counter.
type closeCountEncoder struct {
	closes *atomic.Int64
}

func (e *closeCountEncoder) Encode([]float32) ([]byte, error) { return []byte{0xFF}, nil }
func (e *closeCountEncoder) Close() error                     { e.closes.Add(1); return nil }

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

func TestMixerPassthroughFlag(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")

	// Single active at 0dB → passthrough
	m.SetActive("cam1", true)
	require.True(t, m.IsPassthrough())

	// Two active → not passthrough
	m.SetActive("cam2", true)
	require.False(t, m.IsPassthrough())

	// Back to one
	m.SetActive("cam2", false)
	require.True(t, m.IsPassthrough())

	// One active but non-zero level → not passthrough
	_ = m.SetLevel("cam1", -6.0)
	require.False(t, m.IsPassthrough())

	// Reset level
	_ = m.SetLevel("cam1", 0.0)
	require.True(t, m.IsPassthrough())
}

func TestMixerGainApplication(t *testing.T) {
	require.InDelta(t, 1.0, DBToLinear(0), 0.001)
	require.InDelta(t, 0.5012, DBToLinear(-6.0), 0.001)
	require.InDelta(t, 0.1, DBToLinear(-20.0), 0.001)
	require.InDelta(t, 2.0, DBToLinear(6.02), 0.01)
	require.Equal(t, 0.0, DBToLinear(math.Inf(-1)))
}

func TestMixerMultiChannelMixing(t *testing.T) {
	// Two active channels → mixing mode → decode both, sum, encode
	var capturedPCM []float32
	var outputFrames []*media.AudioFrame

	// PCM samples returned by each channel's decoder
	cam1PCM := []float32{0.5, 0.5, 0.5, 0.5}
	cam2PCM := []float32{0.3, 0.3, 0.3, 0.3}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (AudioDecoder, error) {
			// Each call returns a new decoder; we'll set samples per-channel below
			return &mockDecoder{samples: nil}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (AudioEncoder, error) {
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)
	require.False(t, m.IsPassthrough())

	// Set the decoder samples for each channel directly
	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: cam1PCM}
	m.channels["cam2"].decoder = &mockDecoder{samples: cam2PCM}
	m.mu.Unlock()

	// Ingest frames from both channels
	frame1 := &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	frame2 := &media.AudioFrame{PTS: 1000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2}

	m.IngestFrame("cam1", frame1)
	m.IngestFrame("cam2", frame2)

	// After both channels contribute, output should appear
	require.Equal(t, 1, len(outputFrames), "should produce one mixed output frame")
	require.Equal(t, []byte{0xFF}, outputFrames[0].Data, "output should be encoded AAC")

	// Verify summed PCM: 0.5 + 0.3 = 0.8 for each sample (both at 0dB gain)
	require.Equal(t, 4, len(capturedPCM))
	for i, s := range capturedPCM {
		require.InDelta(t, 0.8, s, 0.001, "sample %d should be sum of channels", i)
	}
}

func TestMixerMasterLevel(t *testing.T) {
	// Single channel with non-zero master level → not passthrough → decode, apply gain, encode
	var capturedPCM []float32
	var outputFrames []*media.AudioFrame

	pcm := []float32{1.0, 1.0, 1.0, 1.0}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (AudioDecoder, error) {
			return &mockDecoder{samples: pcm}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (AudioEncoder, error) {
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	m.SetMasterLevel(-6.0)
	require.False(t, m.IsPassthrough())

	frame := &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	m.IngestFrame("cam1", frame)

	// Single active unmuted channel → output after 1 frame
	require.Equal(t, 1, len(outputFrames))

	// Verify master gain applied: 1.0 * DBToLinear(-6.0) ≈ 0.5012
	expectedGain := DBToLinear(-6.0)
	require.Equal(t, 4, len(capturedPCM))
	for i, s := range capturedPCM {
		require.InDelta(t, expectedGain, s, 0.001, "sample %d should have master gain applied", i)
	}
}

func TestMixerSetMasterLevel(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	require.True(t, m.IsPassthrough())

	m.SetMasterLevel(-3.0)
	require.False(t, m.IsPassthrough(), "non-zero master level should disable passthrough")

	m.SetMasterLevel(0.0)
	require.True(t, m.IsPassthrough(), "zero master level should enable passthrough")
}

func TestMixerChannelGainApplied(t *testing.T) {
	// Channel with -6dB gain → mixing mode → gain applied to decoded PCM
	var capturedPCM []float32
	var outputFrames []*media.AudioFrame

	pcm := []float32{1.0, 1.0, 1.0, 1.0}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (AudioDecoder, error) {
			return &mockDecoder{samples: pcm}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (AudioEncoder, error) {
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	_ = m.SetLevel("cam1", -6.0)
	require.False(t, m.IsPassthrough())

	frame := &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	m.IngestFrame("cam1", frame)

	require.Equal(t, 1, len(outputFrames))
	expectedGain := DBToLinear(-6.0) // ≈ 0.5012
	require.Equal(t, 4, len(capturedPCM))
	for i, s := range capturedPCM {
		require.InDelta(t, expectedGain, s, 0.001, "sample %d should have channel gain", i)
	}
}

func TestMixerMutedChannelInMixMode(t *testing.T) {
	// Two channels active, one muted → only unmuted contributes
	var capturedPCM []float32
	var outputFrames []*media.AudioFrame

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (AudioDecoder, error) {
			return &mockDecoder{samples: []float32{0.5, 0.5}}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (AudioEncoder, error) {
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)
	_ = m.SetMuted("cam2", true) // cam2 muted — still two active channels, but muted frames are dropped

	frame := &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	m.IngestFrame("cam1", frame)

	// Only cam1 contributes (cam2 muted so not expected), mixer should output
	require.Equal(t, 1, len(outputFrames))
	for i, s := range capturedPCM {
		require.InDelta(t, 0.5, s, 0.001, "sample %d: only cam1 should contribute", i)
	}
}

func TestMixerOnCutCrossfade(t *testing.T) {
	// OnCut triggers a crossfade from old source to new source.
	// During crossfade, old source PCM fades out, new source fades in.
	var allCapturedPCM [][]float32
	var outputFrames []*media.AudioFrame

	oldPCM := []float32{1.0, 1.0, 1.0, 1.0}
	newPCM := []float32{0.0, 0.0, 0.0, 0.0}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (AudioDecoder, error) {
			return &mockDecoder{samples: nil}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (AudioEncoder, error) {
			var captured []float32
			allCapturedPCM = append(allCapturedPCM, captured)
			idx := len(allCapturedPCM) - 1
			return &mockEncoderCapture{pcmRef: &allCapturedPCM[idx]}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	// Set up decoders with known PCM
	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: oldPCM}
	m.channels["cam2"].decoder = &mockDecoder{samples: newPCM}
	m.mu.Unlock()

	// Trigger crossfade: cam1 → cam2
	m.OnCut("cam1", "cam2")

	// During crossfade, both old and new source frames should be accepted
	// even though cam2 might not be "active" yet from the perspective of the channels
	frame1 := &media.AudioFrame{PTS: 2000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	frame2 := &media.AudioFrame{PTS: 2000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2}

	m.IngestFrame("cam1", frame1) // outgoing source
	m.IngestFrame("cam2", frame2) // incoming source

	require.Equal(t, 1, len(outputFrames), "crossfade should produce one output frame")

	// The crossfaded PCM: at start old=1.0, new=0.0
	// EqualPowerCrossfade: result[0] = 1.0*cos(0) + 0.0*sin(0) = 1.0
	// The brickwall limiter at -1 dBFS (~0.891) clamps full-scale values.
	// result[3] = 1.0*cos(3/4·π/2) + 0.0*sin(3/4·π/2) ≈ cos(3π/8) ≈ 0.383 (below threshold)
	limiterThreshold := math.Pow(10, -1.0/20.0) // -1 dBFS ≈ 0.891
	lastPCM := allCapturedPCM[len(allCapturedPCM)-1]
	require.Equal(t, 4, len(lastPCM))
	require.InDelta(t, limiterThreshold, lastPCM[0], 0.02, "first sample should be clamped to limiter threshold")
	// Last sample should be faded from old toward new
	require.True(t, lastPCM[3] < lastPCM[0], "signal should be fading")
}

func TestMixerCrossfadeClears(t *testing.T) {
	// After crossfade completes, subsequent frames go through normal mixing
	var outputFrames []*media.AudioFrame

	pcm := []float32{0.5, 0.5}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (AudioDecoder, error) {
			return &mockDecoder{samples: pcm}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (AudioEncoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	// Trigger crossfade
	m.OnCut("cam1", "cam2")

	// Complete the crossfade with one frame each
	m.IngestFrame("cam1", &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})
	m.IngestFrame("cam2", &media.AudioFrame{PTS: 1000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2})

	require.Equal(t, 1, len(outputFrames))

	// After crossfade, verify it's cleared
	m.mu.RLock()
	active := m.crossfadeActive
	m.mu.RUnlock()
	require.False(t, active, "crossfade should be cleared after completion")
}

func TestMixerCrossfadeTimeout(t *testing.T) {
	// When the outgoing source disconnects, the crossfade should complete
	// after the timeout with only the incoming source's audio.
	var outputFrames []*media.AudioFrame

	pcm := []float32{0.5, 0.5, 0.3, 0.3}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (AudioDecoder, error) {
			return &mockDecoder{samples: pcm}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (AudioEncoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	// Trigger crossfade with immediate deadline (expired)
	m.OnCut("cam1", "cam2")
	m.mu.Lock()
	m.crossfadeDeadline = m.crossfadeDeadline.Add(-crossfadeTimeout * 2) // force expiry
	m.mu.Unlock()

	// Only the incoming source delivers a frame — outgoing timed out
	m.IngestFrame("cam2", &media.AudioFrame{PTS: 1000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2})

	require.Equal(t, 1, len(outputFrames), "should produce output with only incoming source after timeout")

	// Crossfade should be cleared
	m.mu.RLock()
	active := m.crossfadeActive
	m.mu.RUnlock()
	require.False(t, active, "crossfade should be cleared after timeout completion")
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
	m.OnTransitionStart("cam1", "cam2", AudioCrossfade, 1000)

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

	m.OnTransitionStart("cam1", "cam2", AudioCrossfade, 1000)

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

	m.OnTransitionStart("cam1", "cam2", AudioCrossfade, 1000)
	m.OnTransitionPosition(0.5)
	require.True(t, m.IsInTransitionCrossfade())

	m.OnTransitionComplete()

	require.False(t, m.IsInTransitionCrossfade(), "transition crossfade should be cleared")
	require.InDelta(t, 0.0, m.TransitionPosition(), 0.001, "position should be reset")
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

	m.OnTransitionStart("cam1", "cam2", AudioCrossfade, 1000)

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

func TestMixerTransitionCrossfadeIngestFrame(t *testing.T) {
	// During a transition crossfade, IngestFrame should apply position-based
	// gains to the from/to sources, multiplied with channel gain.
	// Use 0.5 amplitude to stay below the -1 dBFS limiter threshold.
	var capturedPCM []float32
	var outputFrames []*media.AudioFrame

	fromPCM := []float32{0.5, 0.5, 0.5, 0.5}
	toPCM := []float32{0.5, 0.5, 0.5, 0.5}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (AudioDecoder, error) {
			return &mockDecoder{samples: nil}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (AudioEncoder, error) {
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	// Set up decoders with known PCM
	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: fromPCM}
	m.channels["cam2"].decoder = &mockDecoder{samples: toPCM}
	m.mu.Unlock()

	// Start transition at 50% with audio position pre-set to 0.5 so both
	// audioPos and cyclePos are at 0.5, giving flat gain (no per-sample ramp).
	m.OnTransitionStart("cam1", "cam2", AudioCrossfade, 1000)
	m.OnTransitionPosition(0.5)
	m.mu.Lock()
	m.transCrossfadeAudioPos = 0.5 // simulate audio already advanced to 0.5
	m.mu.Unlock()

	// Ingest from both sources — the mixer is in mixing mode (2 active channels)
	frame1 := &media.AudioFrame{PTS: 2000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	frame2 := &media.AudioFrame{PTS: 2000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2}

	m.IngestFrame("cam1", frame1)
	m.IngestFrame("cam2", frame2)

	require.Equal(t, 1, len(outputFrames), "should produce one mixed output frame")

	// At position 0.5: oldGain = cos(0.5·π/2) ≈ 0.707, newGain = sin(0.5·π/2) ≈ 0.707
	// Both sources have 0.5 PCM at 0dB channel gain
	// Expected mixed sample: 0.5*0.707 + 0.5*0.707 ≈ 0.707
	expectedSum := 0.5 * (math.Cos(0.5*math.Pi/2) + math.Sin(0.5*math.Pi/2))
	require.Equal(t, 4, len(capturedPCM))
	for i, s := range capturedPCM {
		require.InDelta(t, expectedSum, s, 0.01, "sample %d should have transition gains applied", i)
	}
}

// --- Bug: Audio gain discontinuity when video position updates skip between audio frames ---

func TestMixerTransitionGainContinuityAcrossFrames(t *testing.T) {
	// This test verifies that audio gain is continuous across mix cycles even
	// when multiple video position updates happen between audio frames.
	// Bug: transCrossfadePrevPos was updated by video goroutine, so if 3 video
	// position updates happen between 2 audio frames, the audio gain jumps.
	//
	// Scenario: Audio frame 1 at position 0.1, then video updates to 0.2, 0.3,
	// then audio frame 2. Frame 2's start gain should match frame 1's end gain
	// (both at position 0.1), NOT jump to 0.2 from the video-driven prevPos.

	var capturedPCMs [][]float32
	var outputFrames []*media.AudioFrame

	pcm := []float32{0.5, 0.5, 0.5, 0.5} // 2 stereo samples

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (AudioDecoder, error) {
			return &mockDecoder{samples: nil}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (AudioEncoder, error) {
			return &mockEncoderCapture{pcmRef: new([]float32)}, nil
		},
	})
	defer func() { _ = m.Close() }()

	// Use a capturing encoder that records each call's PCM
	var encodeCalls [][]float32
	m.mu.Lock()
	m.encoder = &mockEncoderMultiCapture{calls: &encodeCalls}
	m.mu.Unlock()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: pcm}
	m.channels["cam2"].decoder = &mockDecoder{samples: pcm}
	m.mu.Unlock()

	// Start transition
	m.OnTransitionStart("cam1", "cam2", AudioCrossfade, 1000)

	// === Audio frame 1: position at 0.1 ===
	m.OnTransitionPosition(0.1)
	m.IngestFrame("cam1", &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})
	m.IngestFrame("cam2", &media.AudioFrame{PTS: 1000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2})
	require.Equal(t, 1, len(outputFrames), "should have 1 output after first mix cycle")

	// Record end gain of frame 1 — the last sample's gain for the "from" source
	require.True(t, len(encodeCalls) >= 1, "encoder should have been called")
	frame1PCM := encodeCalls[0]
	frame1LastSample := frame1PCM[len(frame1PCM)-1]

	// === Simulate video position updates WITHOUT audio frames (video is faster) ===
	m.OnTransitionPosition(0.15)
	m.OnTransitionPosition(0.2)
	m.OnTransitionPosition(0.3) // video jumped ahead

	// === Audio frame 2: position now at 0.3 ===
	m.IngestFrame("cam1", &media.AudioFrame{PTS: 2000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})
	m.IngestFrame("cam2", &media.AudioFrame{PTS: 2000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2})
	require.Equal(t, 2, len(outputFrames), "should have 2 outputs")

	frame2PCM := encodeCalls[1]
	frame2FirstSample := frame2PCM[0]

	// KEY ASSERTION: The first sample of frame 2 should be close to the last
	// sample of frame 1 (continuous gain). The difference should be small —
	// certainly less than 10% of the signal amplitude.
	discontinuity := math.Abs(float64(frame2FirstSample - frame1LastSample))
	maxAllowedDiscontinuity := 0.05 // 5% of 0.5 amplitude = 0.025, be generous
	require.Less(t, discontinuity, maxAllowedDiscontinuity,
		"gain discontinuity between frames: frame1 last=%.4f, frame2 first=%.4f, diff=%.4f",
		frame1LastSample, frame2FirstSample, discontinuity)

	_ = capturedPCMs
}

// TestMixerTransitionCompleteFlushes verifies that OnTransitionComplete flushes
// any pending mix cycle before re-enabling passthrough, preventing frame drops.
func TestMixerTransitionCompleteFlushes(t *testing.T) {
	var mu sync.Mutex
	var outputFrames []*media.AudioFrame

	pcm := []float32{0.5, 0.5, 0.5, 0.5}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			mu.Lock()
			outputFrames = append(outputFrames, frame)
			mu.Unlock()
		},
		DecoderFactory: func(sampleRate, channels int) (AudioDecoder, error) {
			return &mockDecoder{samples: nil}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (AudioEncoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: pcm}
	m.channels["cam2"].decoder = &mockDecoder{samples: pcm}
	m.mu.Unlock()

	m.OnTransitionStart("cam1", "cam2", AudioCrossfade, 1000)
	m.OnTransitionPosition(0.5)

	// Ingest one frame from cam1 only — starts a mix cycle but doesn't flush
	// (needs both sources for eager flush)
	m.IngestFrame("cam1", &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})

	mu.Lock()
	countBefore := len(outputFrames)
	mu.Unlock()

	// Complete the transition — this should flush the pending mix cycle
	m.OnTransitionComplete()

	mu.Lock()
	countAfter := len(outputFrames)
	mu.Unlock()

	require.Greater(t, countAfter, countBefore,
		"OnTransitionComplete should flush pending mix cycle (had %d frames before, %d after)",
		countBefore, countAfter)
}

// mockEncoderMultiCapture captures PCM from each Encode call separately.
type mockEncoderMultiCapture struct {
	calls *[][]float32
}

func (m *mockEncoderMultiCapture) Encode(pcm []float32) ([]byte, error) {
	cp := make([]float32, len(pcm))
	copy(cp, pcm)
	*m.calls = append(*m.calls, cp)
	return []byte{0xFF}, nil
}
func (m *mockEncoderMultiCapture) Close() error { return nil }

// --- Bug 1: FTB Reverse audio should fade IN (not out) ---

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
	m.OnTransitionStart("cam1", "", AudioFadeIn, 1000)

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
	m.OnTransitionStart("cam1", "", AudioFadeOut, 1000)

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

// --- Bug 2: Program mute (FTB held) ---

func TestMixerProgramMute(t *testing.T) {
	var capturedPCM []float32
	var outputFrames []*media.AudioFrame

	pcm := []float32{1.0, 1.0, 1.0, 1.0}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (AudioDecoder, error) {
			return &mockDecoder{samples: pcm}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (AudioEncoder, error) {
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Mute program output (FTB held)
	m.SetProgramMute(true)
	require.True(t, m.IsProgramMuted())
	require.False(t, m.IsPassthrough(), "program mute disables passthrough")

	// Ingest a frame — output should be produced but with silent PCM
	frame := &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	m.IngestFrame("cam1", frame)

	require.Equal(t, 1, len(outputFrames), "muted mixer should still produce output frames")
	require.Equal(t, 4, len(capturedPCM))
	for i, s := range capturedPCM {
		require.InDelta(t, 0.0, s, 0.001, "sample %d should be silent when program muted", i)
	}

	// Verify metering shows silence (LinearToDBFS clamps to -96)
	peak := m.ProgramPeak()
	require.InDelta(t, -96.0, peak[0], 0.001, "left peak should be -96 (silence)")
	require.InDelta(t, -96.0, peak[1], 0.001, "right peak should be -96 (silence)")

	// Unmute — next frame should have audio
	m.SetProgramMute(false)
	require.False(t, m.IsProgramMuted())

	outputFrames = nil
	capturedPCM = nil
	m.IngestFrame("cam1", &media.AudioFrame{PTS: 2000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2})

	require.Equal(t, 1, len(outputFrames))
	for i, s := range capturedPCM {
		require.InDelta(t, 1.0, s, 0.001, "sample %d should have audio after unmute", i)
	}
}

// --- Bug 3: Dip transition dips audio to silence at midpoint ---

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

	m.OnTransitionStart("cam1", "cam2", AudioDipToSilence, 1000)

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

func TestMixerDipIngestFrameMidpoint(t *testing.T) {
	// At the dip midpoint (0.5), both sources should produce silent output.
	var capturedPCM []float32
	var outputFrames []*media.AudioFrame

	fromPCM := []float32{1.0, 1.0, 1.0, 1.0}
	toPCM := []float32{1.0, 1.0, 1.0, 1.0}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (AudioDecoder, error) {
			return &mockDecoder{samples: nil}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (AudioEncoder, error) {
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: fromPCM}
	m.channels["cam2"].decoder = &mockDecoder{samples: toPCM}
	m.mu.Unlock()

	// Start dip at 0.5 (midpoint = silence), set audio position to match
	m.OnTransitionStart("cam1", "cam2", AudioDipToSilence, 1000)
	m.OnTransitionPosition(0.5)
	m.mu.Lock()
	m.transCrossfadeAudioPos = 0.5 // simulate audio already advanced to 0.5
	m.mu.Unlock()

	m.IngestFrame("cam1", &media.AudioFrame{PTS: 2000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})
	m.IngestFrame("cam2", &media.AudioFrame{PTS: 2000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2})

	require.Equal(t, 1, len(outputFrames))
	require.Equal(t, 4, len(capturedPCM))
	for i, s := range capturedPCM {
		require.InDelta(t, 0.0, s, 0.001, "sample %d should be silent at dip midpoint", i)
	}
}

// --- Bug 4: Per-sample interpolation (no zipper noise) ---

func TestMixerTransitionPerSampleInterpolation(t *testing.T) {
	// When position changes between frames, gain should ramp smoothly
	// across samples rather than being a flat block.
	var capturedPCM []float32
	var outputFrames []*media.AudioFrame

	// 8 samples of constant 0.5 — enough to see the ramp.
	// Use 0.5 to stay below the -1 dBFS limiter threshold.
	pcm := make([]float32, 8)
	for i := range pcm {
		pcm[i] = 0.5
	}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (AudioDecoder, error) {
			return &mockDecoder{samples: nil}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (AudioEncoder, error) {
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: pcm}
	m.mu.Unlock()

	// FTB forward from position 0.0 to 0.5 — gain should ramp from 1.0 to 0.707
	m.OnTransitionStart("cam1", "", AudioFadeOut, 1000)
	m.OnTransitionPosition(0.5) // audioPos=0.0 (from start), cyclePos will snapshot 0.5

	m.IngestFrame("cam1", &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})

	require.Equal(t, 1, len(outputFrames))
	require.Equal(t, 8, len(capturedPCM))

	// First sample should be at audioPos gain: 0.5 * cos(0 * π/2) = 0.5
	// Last sample should approach cyclePos gain: 0.5 * cos(0.5 * π/2) ≈ 0.354
	// Intermediate samples should be between these values (monotonically decreasing)
	require.InDelta(t, 0.5, capturedPCM[0], 0.01, "first sample at prevPos gain")
	require.True(t, capturedPCM[7] < capturedPCM[0], "last sample should be less than first (fading out)")
	require.InDelta(t, 0.5*math.Cos(0.5*math.Pi/2), capturedPCM[7], 0.1, "last sample approaching currentPos gain")

	// Check monotonically decreasing (no staircase)
	for i := 1; i < len(capturedPCM); i++ {
		require.True(t, capturedPCM[i] <= capturedPCM[i-1]+0.001,
			"sample %d (%.4f) should be <= sample %d (%.4f) — smooth ramp", i, capturedPCM[i], i-1, capturedPCM[i-1])
	}
}

// --- Deadlock prevention: per-cycle deadline ---

func TestMixerDeadlockPrevention(t *testing.T) {
	// Two active channels, but only one sends frames.
	// Without the fix, the mixer hangs forever waiting for channel 2.
	// With the fix, output is produced after the 50ms deadline.
	var mu sync.Mutex
	var outputFrames []*media.AudioFrame

	cam1PCM := []float32{0.5, 0.5, 0.5, 0.5}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			mu.Lock()
			outputFrames = append(outputFrames, frame)
			mu.Unlock()
		},
		DecoderFactory: func(sampleRate, channels int) (AudioDecoder, error) {
			return &mockDecoder{samples: cam1PCM}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (AudioEncoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)
	require.False(t, m.IsPassthrough())

	// Only send a frame to channel 1 — channel 2 is silent
	frame := &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	m.IngestFrame("cam1", frame)

	// Wait longer than the 50ms deadline
	time.Sleep(100 * time.Millisecond)

	// Output should have been produced despite channel 2 being silent
	mu.Lock()
	count := len(outputFrames)
	mu.Unlock()
	require.GreaterOrEqual(t, count, 1, "mixer should produce output after deadline even if channel 2 is silent")
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
		DecoderFactory: func(sampleRate, channels int) (AudioDecoder, error) {
			factoryCalls.Add(1)
			return &mockDecoder{samples: []float32{0.5, 0.5}}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (AudioEncoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Force non-passthrough mode so the mixing path's decoder init is exercised.
	m.SetMasterLevel(-1.0)
	require.False(t, m.IsPassthrough())

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

func TestMixerCrossfadePreSeedAppliesGain(t *testing.T) {
	// When OnCut pre-seeds old source PCM, it should apply the channel's
	// gain (trim * fader) so the crossfade blends consistently with the
	// new source's gained PCM from ingestCrossfadeFrame.
	var allCapturedPCM [][]float32
	var outputFrames []*media.AudioFrame

	// Both sources produce 1.0 amplitude samples
	oldPCM := []float32{1.0, 1.0, 1.0, 1.0}
	newPCM := []float32{1.0, 1.0, 1.0, 1.0}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (AudioDecoder, error) {
			return &mockDecoder{samples: nil}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (AudioEncoder, error) {
			var captured []float32
			allCapturedPCM = append(allCapturedPCM, captured)
			idx := len(allCapturedPCM) - 1
			return &mockEncoderCapture{pcmRef: &allCapturedPCM[idx]}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	// Set decoders
	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: oldPCM}
	m.channels["cam2"].decoder = &mockDecoder{samples: newPCM}
	m.mu.Unlock()

	// Set cam1 trim to -6dB — this should apply to the pre-seeded PCM
	_ = m.SetTrim("cam1", -6.0)

	// Send frames from cam1 to populate lastDecodedPCM
	for i := 0; i < 3; i++ {
		m.IngestFrame("cam1", &media.AudioFrame{
			PTS: int64(i * 1024), Data: []byte{0xAA}, SampleRate: 48000, Channels: 2,
		})
	}
	outputFrames = nil
	allCapturedPCM = nil

	// Trigger cut — cam1's pre-seeded PCM should have -6dB gain applied
	m.OnCut("cam1", "cam2")

	// Send one frame from cam2 (at 0dB trim, 0dB level → gain=1.0)
	_ = m.SetTrim("cam2", 0)
	m.IngestFrame("cam2", &media.AudioFrame{
		PTS: 3 * 1024, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2,
	})

	require.True(t, len(outputFrames) > 0, "crossfade should produce output")

	// The pre-seeded old source PCM (1.0 * DBToLinear(-6) ≈ 0.501) should be
	// blended with the new source PCM (1.0 * 1.0 = 1.0).
	// At t=0: result ≈ old*cos(0) + new*sin(0) ≈ 0.501*1.0 + 1.0*0.0 = 0.501
	lastPCM := allCapturedPCM[len(allCapturedPCM)-1]
	require.InDelta(t, DBToLinear(-6.0), float64(lastPCM[0]), 0.05,
		"first crossfade sample should reflect cam1's -6dB trim")
}

func TestMixerRemoveChannelCleansUpPCMBuffer(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(f *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, channels int) (AudioDecoder, error) {
			return &mockDecoder{samples: []float32{0.5, 0.5}}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (AudioEncoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	m.SetMasterLevel(-1.0) // force mixing path

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

func TestMixerTrimAppliedBeforeFader(t *testing.T) {
	var capturedPCM []float32
	var outputFrames []*media.AudioFrame

	pcm := []float32{1.0, 1.0, 1.0, 1.0}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(f *media.AudioFrame) {
			outputFrames = append(outputFrames, f)
		},
		DecoderFactory: func(sampleRate, channels int) (AudioDecoder, error) {
			return &mockDecoder{samples: pcm}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (AudioEncoder, error) {
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Set trim to -6dB (fader stays at 0dB)
	err := m.SetTrim("cam1", -6.0)
	require.NoError(t, err)

	// Verify trim is stored in state
	states := m.ChannelStates()
	require.InDelta(t, -6.0, states["cam1"].Trim, 0.01)

	// Verify passthrough is broken (trim != 0)
	require.False(t, m.IsPassthrough())

	// Ingest a frame — output PCM should be attenuated by trim
	m.IngestFrame("cam1", &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})

	require.Equal(t, 1, len(outputFrames))
	// Trim at -6dB: gain ≈ 0.501, fader at 0dB: gain = 1.0
	// Combined: 1.0 * 0.501 * 1.0 ≈ 0.501
	expectedGain := DBToLinear(-6.0)
	require.Equal(t, 4, len(capturedPCM))
	for i, s := range capturedPCM {
		require.InDelta(t, expectedGain, s, 0.001,
			"sample %d should have trim applied (expected %.3f, got %.3f)", i, expectedGain, s)
	}
}

func TestMixerTrimBreaksPassthrough(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(f *media.AudioFrame) {},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Initially passthrough (single source at 0dB)
	require.True(t, m.IsPassthrough())

	// Set non-zero trim
	_ = m.SetTrim("cam1", -3.0)

	// Should break passthrough
	require.False(t, m.IsPassthrough())
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
		DecoderFactory: func(sampleRate, channels int) (AudioDecoder, error) {
			return &mockDecoder{samples: pcm}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (AudioEncoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Force non-passthrough so the mixing path runs
	m.SetMasterLevel(-1.0)

	m.IngestFrame("cam1", &media.AudioFrame{PTS: 0, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})

	// Channel peaks should be populated
	states := m.ChannelStates()
	ch, ok := states["cam1"]
	require.True(t, ok)
	require.True(t, ch.PeakL > -96 || ch.PeakR > -96,
		"per-channel peaks should be populated after frame ingestion")
}

func TestMixerTransitionCrossfadeWithTrim(t *testing.T) {
	// During a transition crossfade, trim should be applied to both sources
	// before the transition gain. Verifies that channelGain (trim * fader)
	// is multiplied with the transition position gain.
	var capturedPCM []float32
	var outputFrames []*media.AudioFrame

	fromPCM := []float32{1.0, 1.0, 1.0, 1.0}
	toPCM := []float32{1.0, 1.0, 1.0, 1.0}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (AudioDecoder, error) {
			return &mockDecoder{samples: nil}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (AudioEncoder, error) {
			return &mockEncoderCapture{pcmRef: &capturedPCM}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	// Apply -6dB trim to cam1 (from source)
	require.NoError(t, m.SetTrim("cam1", -6.0))

	// Set up decoders with known PCM
	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: fromPCM}
	m.channels["cam2"].decoder = &mockDecoder{samples: toPCM}
	m.mu.Unlock()

	// Start transition at 50% with stable position (no per-sample ramp)
	m.OnTransitionStart("cam1", "cam2", AudioCrossfade, 1000)
	m.OnTransitionPosition(0.5)
	m.OnTransitionPosition(0.5)

	frame1 := &media.AudioFrame{PTS: 2000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	frame2 := &media.AudioFrame{PTS: 2000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2}

	m.IngestFrame("cam1", frame1)
	m.IngestFrame("cam2", frame2)

	require.Equal(t, 1, len(outputFrames))

	// At position 0.5: transitionFromGain = cos(0.5·π/2) ≈ 0.707
	//                   transitionToGain   = sin(0.5·π/2) ≈ 0.707
	// cam1: PCM=1.0, trim=-6dB (≈0.501), fader=0dB → channelGain ≈ 0.501
	//   contribution: 1.0 * 0.501 * 0.707 ≈ 0.354
	// cam2: PCM=1.0, trim=0dB, fader=0dB → channelGain = 1.0
	//   contribution: 1.0 * 1.0 * 0.707 ≈ 0.707
	// expected sum ≈ 0.354 + 0.707 ≈ 1.061 (clamped by limiter to ~0.891)
	trimGain := DBToLinear(-6.0)
	transGain := math.Cos(0.5 * math.Pi / 2)
	fromContribution := 1.0 * trimGain * transGain
	toContribution := 1.0 * 1.0 * transGain
	expectedSum := fromContribution + toContribution
	// Sum > 1.0, so limiter clamps. Verify samples are below 1.0 (limiter at -1dBFS ≈ 0.891)
	require.Equal(t, 4, len(capturedPCM))
	if expectedSum > 0.891 {
		// Limiter should have clamped
		for i, s := range capturedPCM {
			require.True(t, s <= 0.891+0.01,
				"sample %d (%.4f) should be limited to -1 dBFS", i, s)
			require.True(t, s > 0.5,
				"sample %d (%.4f) should be non-trivial (both sources contributing)", i, s)
		}
	} else {
		for i, s := range capturedPCM {
			require.InDelta(t, expectedSum, s, 0.02,
				"sample %d should have trim+transition gains applied", i)
		}
	}
}

func TestMixerCrossfadeUsesPreBufferedPCM(t *testing.T) {
	// After sending frames from cam1, triggering a cut to cam2, and sending
	// only ONE frame from cam2, the crossfade should complete immediately
	// because cam1's last PCM is pre-buffered — no waiting needed.
	var outputFrames []*media.AudioFrame

	cam1PCM := []float32{0.8, 0.8, 0.8, 0.8}
	cam2PCM := []float32{0.5, 0.5, 0.5, 0.5}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			outputFrames = append(outputFrames, frame)
		},
		DecoderFactory: func(sampleRate, channels int) (AudioDecoder, error) {
			return &mockDecoder{samples: nil}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (AudioEncoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	// Set up decoders with known PCM
	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: cam1PCM}
	m.channels["cam2"].decoder = &mockDecoder{samples: cam2PCM}
	m.mu.Unlock()

	// Send several frames from cam1 to build up the pre-buffer
	for i := 0; i < 3; i++ {
		m.IngestFrame("cam1", &media.AudioFrame{
			PTS: int64(i * 1024), Data: []byte{0xAA}, SampleRate: 48000, Channels: 2,
		})
	}

	// Clear output from passthrough/mixing frames
	outputFrames = nil

	// Trigger cut from cam1 -> cam2
	m.OnCut("cam1", "cam2")

	// Send ONE frame from cam2 — crossfade should complete immediately
	// because cam1's last PCM is already pre-buffered
	m.IngestFrame("cam2", &media.AudioFrame{
		PTS: 3 * 1024, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2,
	})

	// Should have output: the crossfade should have completed in one frame
	require.True(t, len(outputFrames) > 0,
		"crossfade should produce output immediately on first new-source frame")
}

func TestChannelDecoderInitOnceCrossfade(t *testing.T) {
	// Verify sync.Once works for the crossfade path too.
	var factoryCalls atomic.Int64

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, channels int) (AudioDecoder, error) {
			factoryCalls.Add(1)
			return &mockDecoder{samples: []float32{0.5, 0.5}}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (AudioEncoder, error) {
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
func TestMixerPassthroughRaceSafety(t *testing.T) {
	t.Parallel()

	var outputCount atomic.Int64

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(_ *media.AudioFrame) { outputCount.Add(1) },
	})
	t.Cleanup(func() { _ = m.Close() })

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	// Ingest a few frames synchronously while passthrough is guaranteed
	// (only cam1 active). This ensures outputCount > 0 regardless of
	// goroutine scheduling in the concurrent phase below.
	for i := 0; i < 5; i++ {
		m.IngestFrame("cam1", &media.AudioFrame{
			PTS:        int64(i),
			Data:       []byte{0xAA, 0xBB},
			SampleRate: 48000,
			Channels:   2,
		})
	}

	const iterations = 1000
	var wg sync.WaitGroup

	// Goroutine 1: rapidly ingest frames on cam1
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 5; i < 5+iterations; i++ {
			m.IngestFrame("cam1", &media.AudioFrame{
				PTS:        int64(i),
				Data:       []byte{0xAA, 0xBB},
				SampleRate: 48000,
				Channels:   2,
			})
		}
	}()

	// Goroutine 2: toggle passthrough by activating/deactivating cam2
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			m.SetActive("cam2", true)  // passthrough → false
			m.SetActive("cam2", false) // passthrough → true
		}
	}()

	wg.Wait()

	// If the race detector didn't fire, we're good. The output count
	// doesn't matter — some frames are passthrough, some go through
	// mixing (which has no encoder, so they're dropped). The key is
	// no data race.
	require.Greater(t, outputCount.Load(), int64(0), "should have produced some output")
}

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

func TestMixer_MonotonicOutputPTS(t *testing.T) {
	// Two channels with different PTS values. Verify consecutive output frames
	// have PTS incrementing by exactly frameDuration90k().
	var mu sync.Mutex
	var outputFrames []*media.AudioFrame

	cam1PCM := []float32{0.5, 0.5, 0.5, 0.5}
	cam2PCM := []float32{0.3, 0.3, 0.3, 0.3}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			mu.Lock()
			outputFrames = append(outputFrames, frame)
			mu.Unlock()
		},
		DecoderFactory: func(sampleRate, channels int) (AudioDecoder, error) {
			return &mockDecoder{samples: nil}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (AudioEncoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: cam1PCM}
	m.channels["cam2"].decoder = &mockDecoder{samples: cam2PCM}
	m.mu.Unlock()

	// Ingest 5 pairs with non-monotonic PTS values
	ptsValues := []int64{5000, 3000, 7000, 2000, 9000}
	for _, pts := range ptsValues {
		m.IngestFrame("cam1", &media.AudioFrame{PTS: pts, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})
		m.IngestFrame("cam2", &media.AudioFrame{PTS: pts + 100, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2})
	}

	mu.Lock()
	frames := append([]*media.AudioFrame{}, outputFrames...)
	mu.Unlock()

	require.GreaterOrEqual(t, len(frames), 2, "need at least 2 frames")

	// Output PTS must be strictly monotonically increasing.
	// With source-following PTS, the deltas track input PTS (not a fixed counter),
	// but backward inputs are corrected to advance by frameDuration90k.
	for i := 1; i < len(frames); i++ {
		require.Greater(t, frames[i].PTS, frames[i-1].PTS,
			"frame %d PTS (%d) must be > frame %d PTS (%d)",
			i, frames[i].PTS, i-1, frames[i-1].PTS)
	}
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
		DecoderFactory: func(sampleRate, channels int) (AudioDecoder, error) {
			return &mockDecoder{samples: pcm}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (AudioEncoder, error) {
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

func TestMixer_MonotonicPTS_ArrivalOrderIndependent(t *testing.T) {
	// Swapping which channel delivers first should not affect the output PTS sequence.
	for _, order := range []string{"cam1-first", "cam2-first"} {
		t.Run(order, func(t *testing.T) {
			var mu sync.Mutex
			var outputFrames []*media.AudioFrame

			cam1PCM := []float32{0.5, 0.5, 0.5, 0.5}
			cam2PCM := []float32{0.3, 0.3, 0.3, 0.3}

			m := NewMixer(MixerConfig{
				SampleRate: 48000,
				Channels:   2,
				Output: func(frame *media.AudioFrame) {
					mu.Lock()
					outputFrames = append(outputFrames, frame)
					mu.Unlock()
				},
				DecoderFactory: func(sampleRate, channels int) (AudioDecoder, error) {
					return &mockDecoder{samples: nil}, nil
				},
				EncoderFactory: func(sampleRate, channels int) (AudioEncoder, error) {
					return &mockEncoder{data: []byte{0xFF}}, nil
				},
			})
			defer func() { _ = m.Close() }()

			m.AddChannel("cam1")
			m.AddChannel("cam2")
			m.SetActive("cam1", true)
			m.SetActive("cam2", true)

			m.mu.Lock()
			m.channels["cam1"].decoder = &mockDecoder{samples: cam1PCM}
			m.channels["cam2"].decoder = &mockDecoder{samples: cam2PCM}
			m.mu.Unlock()

			// Ingest 5 pairs, alternating which channel arrives first
			for i := 0; i < 5; i++ {
				f1 := &media.AudioFrame{PTS: int64(1000 + i*2000), Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
				f2 := &media.AudioFrame{PTS: int64(1500 + i*2000), Data: []byte{0xBB}, SampleRate: 48000, Channels: 2}
				if order == "cam1-first" {
					m.IngestFrame("cam1", f1)
					m.IngestFrame("cam2", f2)
				} else {
					m.IngestFrame("cam2", f2)
					m.IngestFrame("cam1", f1)
				}
			}

			mu.Lock()
			frames := append([]*media.AudioFrame{}, outputFrames...)
			mu.Unlock()

			require.GreaterOrEqual(t, len(frames), 2, "need at least 2 frames")

			// Output PTS must be strictly monotonically increasing regardless
			// of ingestion order. Deltas follow source PTS, not a fixed counter.
			for i := 1; i < len(frames); i++ {
				require.Greater(t, frames[i].PTS, frames[i-1].PTS,
					"frame %d PTS (%d) must be > frame %d PTS (%d) (order=%s)",
					i, frames[i].PTS, i-1, frames[i-1].PTS, order)
			}
		})
	}
}

func TestMixer_MonotonicPTS_ResetOnGap(t *testing.T) {
	var mu sync.Mutex
	var outputFrames []*media.AudioFrame

	pcm := []float32{0.5, 0.5, 0.5, 0.5}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			mu.Lock()
			outputFrames = append(outputFrames, frame)
			mu.Unlock()
		},
		DecoderFactory: func(sampleRate, channels int) (AudioDecoder, error) {
			return &mockDecoder{samples: pcm}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (AudioEncoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	_ = m.SetTrim("cam1", 1.0) // force mixing mode

	// Ingest a few frames at PTS ~1000
	for i := range 3 {
		m.IngestFrame("cam1", &media.AudioFrame{PTS: 1000 + int64(i)*1920, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})
	}

	mu.Lock()
	countBefore := len(outputFrames)
	mu.Unlock()

	// Now inject a PTS gap > 5 seconds
	m.IngestFrame("cam1", &media.AudioFrame{PTS: 90000*6 + 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})
	// Then a normal follow-up
	m.IngestFrame("cam1", &media.AudioFrame{PTS: 90000*6 + 2920, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})

	mu.Lock()
	frames := append([]*media.AudioFrame{}, outputFrames...)
	mu.Unlock()

	require.Greater(t, len(frames), countBefore, "should produce frames after gap")

	// The frame after the gap should have reseeded PTS (not necessarily
	// continuous from before the gap)
	lastIdx := len(frames) - 1
	if lastIdx >= 1 {
		delta := frames[lastIdx].PTS - frames[lastIdx-1].PTS
		expectedDelta := int64(1024) * 90000 / int64(48000)
		// After reseed, the next frame should still increment by frameDuration
		require.Equal(t, expectedDelta, delta,
			"frame after gap reseed should still have correct delta")
	}
}

// TestMixer_MonotonicPTSAcrossPassthroughMixingCycles verifies that output PTS
// is monotonically increasing across passthrough↔mixing mode transitions.
// Before the fix, passthrough forwarded raw source PTS while mixing used a
// monotonic counter, causing drift after each transition.
func TestMixer_MonotonicPTSAcrossPassthroughMixingCycles(t *testing.T) {
	var mu sync.Mutex
	var outputFrames []*media.AudioFrame

	// 1024 samples / 48000 Hz = ~21.33ms; in 90 kHz ticks = 1920
	const frameDuration90k = int64(1024) * 90000 / 48000

	pcmSamples := make([]float32, 1024*2) // 1024 stereo samples
	for i := range pcmSamples {
		pcmSamples[i] = 0.1
	}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output: func(frame *media.AudioFrame) {
			mu.Lock()
			outputFrames = append(outputFrames, frame)
			mu.Unlock()
		},
		DecoderFactory: func(sampleRate, channels int) (AudioDecoder, error) {
			return &mockDecoder{samples: pcmSamples}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (AudioEncoder, error) {
			return &mockEncoder{data: []byte{0xFF}}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")

	// Each source has its own PTS sequence — in a real system these come from
	// independent cameras and are NOT synchronized.
	cam1PTS := int64(100000)
	cam2PTS := int64(200000) // intentionally different from cam1

	// Run 4 transition cycles
	for cycle := 0; cycle < 4; cycle++ {
		fromCam, toCam := "cam1", "cam2"
		fromPTS, toPTS := &cam1PTS, &cam2PTS
		if cycle%2 == 1 {
			fromCam, toCam = "cam2", "cam1"
			fromPTS, toPTS = &cam2PTS, &cam1PTS
		}

		// Phase A: passthrough on fromCam (10 frames)
		m.SetActive(fromCam, true)
		m.SetActive(toCam, false)
		for i := 0; i < 10; i++ {
			m.IngestFrame(fromCam, &media.AudioFrame{
				PTS: *fromPTS, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2,
			})
			*fromPTS += frameDuration90k
		}

		// Phase B: transition crossfade (mixing mode)
		m.OnTransitionStart(fromCam, toCam, AudioCrossfade, 500)
		m.OnTransitionPosition(0.5)
		for i := 0; i < 5; i++ {
			m.IngestFrame(fromCam, &media.AudioFrame{
				PTS: *fromPTS, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2,
			})
			*fromPTS += frameDuration90k
			m.IngestFrame(toCam, &media.AudioFrame{
				PTS: *toPTS, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2,
			})
			*toPTS += frameDuration90k
		}
		m.OnTransitionComplete()
	}

	// Collect all output frames
	mu.Lock()
	frames := append([]*media.AudioFrame{}, outputFrames...)
	mu.Unlock()

	require.Greater(t, len(frames), 40, "should have substantial output frames across 4 cycles")

	// Assert: output PTS is strictly monotonically increasing across ALL frames.
	// With source-following PTS, deltas may be larger than frameDuration90k
	// when the mixer switches between sources on different PTS timelines.
	for i := 1; i < len(frames); i++ {
		require.Greater(t, frames[i].PTS, frames[i-1].PTS,
			"frame %d→%d: PTS must be strictly increasing (prev=%d, cur=%d)",
			i-1, i, frames[i-1].PTS, frames[i].PTS)
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
	m.transCrossfadeMode = AudioCrossfade
	m.transCrossfadeAudioPos = 0.0
	m.mixCycleTransPos = 1.0
	m.mixStarted = true

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
	pairCount := float32(len(trimmedPCM) / channels)
	for i, s := range trimmedPCM {
		t := float32(i/channels) / pairCount
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
	m.SetMasterLevel(-3)
	m.mu.RLock()
	expectedMaster := float32(DBToLinear(-3))
	require.InDelta(t, float64(expectedMaster), float64(m.masterLinear), 1e-6, "masterLinear should match DBToLinear(-3)")
	m.mu.RUnlock()
}

func TestMixerCrossfadeLimiterApplied(t *testing.T) {
	// Two full-scale signals crossfaded peak at ~1.414 (+3dB) at the midpoint.
	// The brickwall limiter must clamp output to ≤ -1 dBFS (~0.891).
	var allCapturedPCM [][]float32

	// Both sources at full scale — crossfade midpoint will exceed 1.0
	fullScale := make([]float32, 1024)
	for i := range fullScale {
		fullScale[i] = 1.0
	}

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, channels int) (AudioDecoder, error) {
			return &mockDecoder{samples: nil}, nil
		},
		EncoderFactory: func(sampleRate, channels int) (AudioEncoder, error) {
			var captured []float32
			allCapturedPCM = append(allCapturedPCM, captured)
			idx := len(allCapturedPCM) - 1
			return &mockEncoderCapture{pcmRef: &allCapturedPCM[idx]}, nil
		},
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	// Both decoders return full-scale PCM
	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: fullScale}
	m.channels["cam2"].decoder = &mockDecoder{samples: fullScale}
	m.mu.Unlock()

	// Trigger crossfade: cam1 → cam2
	m.OnCut("cam1", "cam2")

	frame1 := &media.AudioFrame{PTS: 2000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	frame2 := &media.AudioFrame{PTS: 2000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2}

	m.IngestFrame("cam1", frame1)
	m.IngestFrame("cam2", frame2)

	require.GreaterOrEqual(t, len(allCapturedPCM), 1, "should have captured crossfade PCM")
	lastPCM := allCapturedPCM[len(allCapturedPCM)-1]

	// Limiter threshold is -1 dBFS ≈ 0.891
	threshold := float32(math.Pow(10, -1.0/20.0))

	for i, sample := range lastPCM {
		if sample > threshold+0.001 {
			t.Fatalf("sample %d = %.4f exceeds limiter threshold %.4f — limiter not applied during crossfade",
				i, sample, threshold)
		}
	}
}
