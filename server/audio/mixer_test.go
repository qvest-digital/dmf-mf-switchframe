package audio

import (
	"math"
	"sync"
	"testing"

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
	defer m.Close()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Ingest a frame — should passthrough (single active source, 0dB, not muted)
	frame := &media.AudioFrame{PTS: 1000, Data: []byte{0xAA, 0xBB}, SampleRate: 48000, Channels: 2}
	m.IngestFrame("cam1", frame)

	mu.Lock()
	require.Equal(t, 1, len(output))
	require.Equal(t, []byte{0xAA, 0xBB}, output[0].Data, "passthrough should forward raw AAC")
	require.Equal(t, int64(1000), output[0].PTS)
	mu.Unlock()
}

func TestMixerIgnoresInactiveChannel(t *testing.T) {
	var output []*media.AudioFrame

	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) { output = append(output, frame) },
	})
	defer m.Close()

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
	defer m.Close()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	m.SetMuted("cam1", true)

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
	defer m.Close()

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
	m.SetLevel("cam1", -6.0)
	require.False(t, m.IsPassthrough())

	// Reset level
	m.SetLevel("cam1", 0.0)
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
	defer m.Close()

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
	defer m.Close()

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
	defer m.Close()

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
	defer m.Close()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	m.SetLevel("cam1", -6.0)
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
	defer m.Close()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)
	m.SetMuted("cam2", true) // cam2 muted — still two active channels, but muted frames are dropped

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
	defer m.Close()

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
	// result[3] = 1.0*cos(3/4·π/2) + 0.0*sin(3/4·π/2) ≈ cos(3π/8) ≈ 0.383
	// The crossfade output should be between 0 and 1 for all samples
	lastPCM := allCapturedPCM[len(allCapturedPCM)-1]
	require.Equal(t, 4, len(lastPCM))
	require.InDelta(t, 1.0, lastPCM[0], 0.01, "first sample should be ~old")
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
	defer m.Close()

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
	defer m.Close()

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
	defer m.Close()

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
	defer m.Close()

	m.AddChannel("cam1")
	m.AddChannel("cam2")

	// Both channels have AFV enabled
	m.SetAFV("cam1", true)
	m.SetAFV("cam2", true)

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
	defer m.Close()

	m.AddChannel("cam1")
	m.AddChannel("music")

	// cam1 has AFV, music does not
	m.SetAFV("cam1", true)
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
	defer m.Close()

	m.AddChannel("cam1")
	m.SetAFV("cam1", true)

	// Activate via program change
	m.OnProgramChange("cam1")
	require.True(t, m.IsChannelActive("cam1"))

	// Turn off AFV — channel stays active (was already active)
	m.SetAFV("cam1", false)
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
	defer m.Close()

	// Unknown channel returns false
	require.False(t, m.IsChannelActive("unknown"))

	m.AddChannel("cam1")
	require.False(t, m.IsChannelActive("cam1"), "new channel starts inactive")

	m.SetActive("cam1", true)
	require.True(t, m.IsChannelActive("cam1"))
}
