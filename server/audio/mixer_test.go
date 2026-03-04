package audio

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

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
