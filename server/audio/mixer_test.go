package audio

import (
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/internal"
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

func TestMixerOnTransitionStart(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer m.Close()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	// Before transition, no crossfade active
	require.False(t, m.IsInTransitionCrossfade())

	// Start transition
	m.OnTransitionStart("cam1", "cam2", internal.AudioCrossfade, 1000)

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
	defer m.Close()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	m.OnTransitionStart("cam1", "cam2", internal.AudioCrossfade, 1000)

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
	defer m.Close()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	m.OnTransitionStart("cam1", "cam2", internal.AudioCrossfade, 1000)
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
	defer m.Close()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	m.OnTransitionStart("cam1", "cam2", internal.AudioCrossfade, 1000)

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
	defer m.Close()

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
	defer m.Close()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	// Set up decoders with known PCM
	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: fromPCM}
	m.channels["cam2"].decoder = &mockDecoder{samples: toPCM}
	m.mu.Unlock()

	// Start transition at 50% — set position twice so prevPos and currentPos
	// are both 0.5, ensuring a flat gain (no per-sample ramp).
	m.OnTransitionStart("cam1", "cam2", internal.AudioCrossfade, 1000)
	m.OnTransitionPosition(0.5)
	m.OnTransitionPosition(0.5) // stabilize: prevPos = currentPos = 0.5

	// Ingest from both sources — the mixer is in mixing mode (2 active channels)
	frame1 := &media.AudioFrame{PTS: 2000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2}
	frame2 := &media.AudioFrame{PTS: 2000, Data: []byte{0xBB}, SampleRate: 48000, Channels: 2}

	m.IngestFrame("cam1", frame1)
	m.IngestFrame("cam2", frame2)

	require.Equal(t, 1, len(outputFrames), "should produce one mixed output frame")

	// At position 0.5: oldGain = cos(0.5·π/2) ≈ 0.707, newGain = sin(0.5·π/2) ≈ 0.707
	// Both sources have 0.5 PCM at 0dB channel gain
	// Expected mixed sample: 0.5*0.707 + 0.5*0.707 ≈ 0.707
	expectedSum := 0.5*(math.Cos(0.5*math.Pi/2) + math.Sin(0.5*math.Pi/2))
	require.Equal(t, 4, len(capturedPCM))
	for i, s := range capturedPCM {
		require.InDelta(t, expectedSum, s, 0.01, "sample %d should have transition gains applied", i)
	}
}

// --- Bug 1: FTB Reverse audio should fade IN (not out) ---

func TestMixerTransitionFTBReverseGains(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(frame *media.AudioFrame) {},
	})
	defer m.Close()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// FTB reverse: fade the "from" source IN from silence
	m.OnTransitionStart("cam1", "", internal.AudioFadeIn, 1000)

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
	defer m.Close()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// FTB forward: fade the "from" source OUT to silence
	m.OnTransitionStart("cam1", "", internal.AudioFadeOut, 1000)

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
	defer m.Close()

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
	defer m.Close()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)

	m.OnTransitionStart("cam1", "cam2", internal.AudioDipToSilence, 1000)

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
	defer m.Close()

	m.AddChannel("cam1")
	m.AddChannel("cam2")
	m.SetActive("cam1", true)
	m.SetActive("cam2", true)

	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: fromPCM}
	m.channels["cam2"].decoder = &mockDecoder{samples: toPCM}
	m.mu.Unlock()

	// Start dip at 0.5 (midpoint = silence), stabilize position
	m.OnTransitionStart("cam1", "cam2", internal.AudioDipToSilence, 1000)
	m.OnTransitionPosition(0.5)
	m.OnTransitionPosition(0.5)

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
	defer m.Close()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	m.mu.Lock()
	m.channels["cam1"].decoder = &mockDecoder{samples: pcm}
	m.mu.Unlock()

	// FTB forward from position 0.0 to 0.5 — gain should ramp from 1.0 to 0.707
	m.OnTransitionStart("cam1", "", internal.AudioFadeOut, 1000)
	m.OnTransitionPosition(0.0) // prevPos=0, currentPos=0
	m.OnTransitionPosition(0.5) // prevPos=0, currentPos=0.5

	m.IngestFrame("cam1", &media.AudioFrame{PTS: 1000, Data: []byte{0xAA}, SampleRate: 48000, Channels: 2})

	require.Equal(t, 1, len(outputFrames))
	require.Equal(t, 8, len(capturedPCM))

	// First sample should be at prevPos gain: 0.5 * cos(0 * π/2) = 0.5
	// Last sample should approach currentPos gain: 0.5 * cos(0.5 * π/2) ≈ 0.354
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
	defer m.Close()

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
	defer m.Close()

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
	defer m.Close()

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
	m.SetTrim("cam1", -6.0)

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
	m.SetTrim("cam2", 0)
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
	defer m.Close()

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
	defer m.Close()

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
	defer m.Close()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	// Initially passthrough (single source at 0dB)
	require.True(t, m.IsPassthrough())

	// Set non-zero trim
	m.SetTrim("cam1", -3.0)

	// Should break passthrough
	require.False(t, m.IsPassthrough())
}

func TestMixerTrimRangeValidation(t *testing.T) {
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(f *media.AudioFrame) {},
	})
	defer m.Close()

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
	defer m.Close()

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
	defer m.Close()

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
	defer m.Close()

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
