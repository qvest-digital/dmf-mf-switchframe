package audio

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

// surroundDecoder is a mock AAC decoder that emits interleaved PCM at a fixed
// channel count and reports Channels() so the mixer can detect surround input.
type surroundDecoder struct {
	samples  []float32
	channels int
}

func (d *surroundDecoder) Decode([]byte) ([]float32, error) {
	out := make([]float32, len(d.samples))
	copy(out, d.samples)
	return out, nil
}
func (d *surroundDecoder) Channels() int { return d.channels }
func (d *surroundDecoder) Close() error  { return nil }

// build51PCM returns `frames` interleaved 5.1 sample-frames with the requested
// per-channel amplitudes laid out L/R/C/LFE/Ls/Rs per ITU-R BS.2051-3.
func build51PCM(frames int, l, r, c, lfe, ls, rs float32) []float32 {
	out := make([]float32, frames*6)
	for i := 0; i < frames; i++ {
		out[i*6+0] = l
		out[i*6+1] = r
		out[i*6+2] = c
		out[i*6+3] = lfe
		out[i*6+4] = ls
		out[i*6+5] = rs
	}
	return out
}

// bs775Lo applies the ITU-R BS.775-3 Lo/Ro downmix reference so tests can
// express expected values directly from source amplitudes.
func bs775Lo(l, r, c, _, ls, rs float32) (float32, float32) {
	const k = float32(0.7071067811865475) // 1/sqrt(2) = -3 dB
	return l + k*c + k*ls, r + k*c + k*rs
}

func absF32(f float32) float32 {
	if f < 0 {
		return -f
	}
	return f
}

func TestMixer_IngestFrame_DownmixSurroundToStereo(t *testing.T) {
	t.Parallel()

	const frames = 1024
	const mixerChans = 2

	cases := []struct {
		name                     string
		l, r, c, lfe, ls, rs     float32
	}{
		{"C_only_phantom_center", 0, 0, 0.8, 0, 0, 0},
		{"L_only_passes_through", 0.5, 0, 0, 0, 0, 0},
		{"R_only_passes_through", 0, 0.5, 0, 0, 0, 0},
		{"Ls_only_asymmetric", 0, 0, 0, 0, 0.6, 0},
		{"Rs_only_asymmetric", 0, 0, 0, 0, 0, 0.6},
		{"LFE_only_dropped", 0, 0, 0, 0.9, 0, 0},
		{"mixed_5_1", 0.2, 0.3, 0.1, 0.9, 0.15, 0.25},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			wantL, wantR := bs775Lo(tc.l, tc.r, tc.c, tc.lfe, tc.ls, tc.rs)
			dec := &surroundDecoder{
				channels: 6,
				samples:  build51PCM(frames, tc.l, tc.r, tc.c, tc.lfe, tc.ls, tc.rs),
			}
			m := NewMixer(MixerConfig{
				SampleRate: 48000,
				Channels:   mixerChans,
				Output:     func(*media.AudioFrame) {},
				DecoderFactory: func(int, int) (Decoder, error) {
					return dec, nil
				},
				EncoderFactory: func(int, int) (Encoder, error) {
					return &mockEncoder{}, nil
				},
				ClockDriven: true,
			})
			defer func() { _ = m.Close() }()

			m.AddChannel("cam1")
			m.SetActive("cam1", true)

			frame := &media.AudioFrame{
				Data:       []byte{0xFF, 0xF1, 0x50, 0x80, 0x02, 0x00, 0xFC},
				SampleRate: 48000,
				Channels:   6,
			}
			m.IngestFrame("cam1", frame)

			m.mu.RLock()
			ch := m.channels["cam1"]
			ringLen := ch.ringBuf.Len()
			peakL := ch.peakL
			peakR := ch.peakR
			popped := ch.ringBuf.Pop(frames * mixerChans)
			m.mu.RUnlock()

			require.Equal(t, frames*mixerChans, ringLen,
				"ring buffer must hold frames*mixerChannels samples after Lo/Ro downmix, not frames*srcChannels")
			require.InDelta(t, float64(wantL), float64(popped[0]),
				1e-5, "L value must follow BS.775-3 Lo/Ro")
			require.InDelta(t, float64(wantR), float64(popped[1]),
				1e-5, "R value must follow BS.775-3 Lo/Ro")
			require.InDelta(t, float64(absF32(wantL)), peakL,
				1e-5, "peakL must reflect stereo L after downmix")
			require.InDelta(t, float64(absF32(wantR)), peakR,
				1e-5, "peakR must reflect stereo R after downmix")
		})
	}
}

func TestMixer_IngestPCM_DownmixSurroundToStereo(t *testing.T) {
	t.Parallel()

	const frames = 1024
	const mixerChans = 2

	cases := []struct {
		name                 string
		l, r, c, lfe, ls, rs float32
	}{
		{"C_only_phantom_center", 0, 0, 0.8, 0, 0, 0},
		{"Ls_only_asymmetric", 0, 0, 0, 0, 0.6, 0},
		{"LFE_only_dropped", 0, 0, 0, 0.9, 0, 0},
		{"mixed_5_1", 0.1, 0.2, 0.15, 0.7, 0.05, 0.3},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			wantL, wantR := bs775Lo(tc.l, tc.r, tc.c, tc.lfe, tc.ls, tc.rs)
			m := NewMixer(MixerConfig{
				SampleRate: 48000,
				Channels:   mixerChans,
				Output:     func(*media.AudioFrame) {},
				DecoderFactory: func(int, int) (Decoder, error) {
					return &mockDecoder{samples: make([]float32, frames*mixerChans)}, nil
				},
				EncoderFactory: func(int, int) (Encoder, error) {
					return &mockEncoder{}, nil
				},
				ClockDriven: true,
			})
			defer func() { _ = m.Close() }()

			m.AddChannel("mxl1")
			m.SetActive("mxl1", true)

			pcm := build51PCM(frames, tc.l, tc.r, tc.c, tc.lfe, tc.ls, tc.rs)
			m.IngestPCM("mxl1", pcm, 1000, 6)

			m.mu.RLock()
			ch := m.channels["mxl1"]
			ringLen := ch.ringBuf.Len()
			peakL := ch.peakL
			peakR := ch.peakR
			popped := ch.ringBuf.Pop(frames * mixerChans)
			m.mu.RUnlock()

			require.Equal(t, frames*mixerChans, ringLen,
				"ring buffer must hold frames*mixerChannels samples after Lo/Ro downmix, not frames*srcChannels")
			require.InDelta(t, float64(wantL), float64(popped[0]),
				1e-5, "L value must follow BS.775-3 Lo/Ro")
			require.InDelta(t, float64(wantR), float64(popped[1]),
				1e-5, "R value must follow BS.775-3 Lo/Ro")
			require.InDelta(t, float64(absF32(wantL)), peakL,
				1e-5, "peakL must reflect stereo L after downmix")
			require.InDelta(t, float64(absF32(wantR)), peakR,
				1e-5, "peakR must reflect stereo R after downmix")
		})
	}
}

// TestMixer_IngestPCM_DownmixReusesBuffer verifies hot-path allocation is
// avoided across repeated surround ingests on the same channel.
func TestMixer_IngestPCM_DownmixReusesBuffer(t *testing.T) {
	t.Parallel()

	const frames = 1024
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(*media.AudioFrame) {},
		DecoderFactory: func(int, int) (Decoder, error) {
			return &mockDecoder{samples: make([]float32, frames*2)}, nil
		},
		EncoderFactory: func(int, int) (Encoder, error) {
			return &mockEncoder{}, nil
		},
		ClockDriven: true,
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("mxl1")
	m.SetActive("mxl1", true)

	pcm := build51PCM(frames, 0, 0, 0.5, 0, 0, 0)
	m.IngestPCM("mxl1", pcm, 1000, 6)

	m.mu.RLock()
	ch := m.channels["mxl1"]
	firstCap := cap(ch.downmixBuf)
	firstPtr := &ch.downmixBuf[0]
	// Drain so the next ingest does not race against previous data.
	_ = ch.ringBuf.Pop(ch.ringBuf.Len())
	m.mu.RUnlock()

	require.Greater(t, firstCap, 0, "downmixBuf must be allocated after first surround ingest")

	// Repeat the ingest — same size buffer should be reused.
	pcm2 := build51PCM(frames, 0, 0, 0.5, 0, 0, 0)
	m.IngestPCM("mxl1", pcm2, 2000, 6)

	m.mu.RLock()
	secondPtr := &ch.downmixBuf[0]
	secondCap := cap(ch.downmixBuf)
	m.mu.RUnlock()

	require.Equal(t, firstCap, secondCap, "downmixBuf capacity must not grow on identical-sized frames")
	require.Same(t, firstPtr, secondPtr, "downmixBuf backing array must be reused, not reallocated")
}

// TestMixer_IngestFrame_DownmixReusesBuffer is the IngestFrame twin of the
// PCM-path buffer reuse test — repeated surround ingests must reuse the same
// backing array rather than allocating per call.
func TestMixer_IngestFrame_DownmixReusesBuffer(t *testing.T) {
	t.Parallel()

	const frames = 1024
	dec := &surroundDecoder{
		channels: 6,
		samples:  build51PCM(frames, 0, 0, 0.5, 0, 0, 0),
	}
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(*media.AudioFrame) {},
		DecoderFactory: func(int, int) (Decoder, error) {
			return dec, nil
		},
		EncoderFactory: func(int, int) (Encoder, error) {
			return &mockEncoder{}, nil
		},
		ClockDriven: true,
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	frame := &media.AudioFrame{
		Data:       []byte{0xFF, 0xF1, 0x50, 0x80, 0x02, 0x00, 0xFC},
		SampleRate: 48000,
		Channels:   6,
	}
	m.IngestFrame("cam1", frame)

	m.mu.RLock()
	ch := m.channels["cam1"]
	require.NotEmpty(t, ch.downmixBuf, "downmixBuf must exist after first ingest")
	firstPtr := &ch.downmixBuf[0]
	firstCap := cap(ch.downmixBuf)
	_ = ch.ringBuf.Pop(ch.ringBuf.Len())
	m.mu.RUnlock()

	m.IngestFrame("cam1", frame)

	m.mu.RLock()
	secondPtr := &ch.downmixBuf[0]
	secondCap := cap(ch.downmixBuf)
	m.mu.RUnlock()

	require.Same(t, firstPtr, secondPtr, "downmixBuf backing array must be reused across IngestFrame calls")
	require.Equal(t, firstCap, secondCap, "downmixBuf capacity must not grow for identical-sized frames")
}

// TestMixer_IngestPCM_DownmixDisengagesWhenLayoutShrinks verifies the guard
// at the downmix branch: once a source reverts to matching channel count,
// the per-DSP chain sees untouched PCM and the ring buffer continues to
// grow with the raw input rather than a stale downmix.
func TestMixer_IngestPCM_DownmixDisengagesWhenLayoutShrinks(t *testing.T) {
	t.Parallel()

	const frames = 1024
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(*media.AudioFrame) {},
		DecoderFactory: func(int, int) (Decoder, error) {
			return &mockDecoder{samples: make([]float32, frames*2)}, nil
		},
		EncoderFactory: func(int, int) (Encoder, error) {
			return &mockEncoder{}, nil
		},
		ClockDriven: true,
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("mxl1")
	m.SetActive("mxl1", true)

	// First ingest is 5.1; downmix engages.
	surround := build51PCM(frames, 0.1, 0.2, 0.3, 0, 0, 0)
	m.IngestPCM("mxl1", surround, 1000, 6)

	m.mu.RLock()
	ch := m.channels["mxl1"]
	lenAfterSurround := ch.ringBuf.Len()
	m.mu.RUnlock()
	require.Equal(t, frames*2, lenAfterSurround, "5.1 ingest must push frames*2 after downmix")

	// Second ingest is stereo (source layout changed or initial detection was wrong);
	// downmix must NOT run, and the raw stereo samples must land in the ring buffer.
	stereo := make([]float32, frames*2)
	for i := 0; i < frames; i++ {
		stereo[i*2+0] = 0.75 // distinguishable from the 5.1 result
		stereo[i*2+1] = 0.25
	}
	m.IngestPCM("mxl1", stereo, 2000, 2)

	m.mu.RLock()
	lenAfterStereo := ch.ringBuf.Len()
	popped := ch.ringBuf.Pop(lenAfterStereo)
	m.mu.RUnlock()
	require.Equal(t, 2*frames*2, lenAfterStereo, "stereo ingest after surround must append frames*2 more samples")

	// The final frames*2 samples should be the raw stereo values (0.75, 0.25),
	// confirming the downmix branch did not fire for the second ingest.
	tail := popped[frames*2:]
	require.InDelta(t, 0.75, float64(tail[0]), 1e-5, "post-shrink L sample must be the raw stereo L, not a downmix")
	require.InDelta(t, 0.25, float64(tail[1]), 1e-5, "post-shrink R sample must be the raw stereo R, not a downmix")
}

// TestMixer_IngestPCM_SurroundLogWarnsOncePerChannelLife verifies the channel
// mismatch warning fires exactly once until the channel is removed.
func TestMixer_IngestPCM_SurroundLogWarnsOncePerChannelLife(t *testing.T) {
	t.Parallel()

	const frames = 1024
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   2,
		Output:     func(*media.AudioFrame) {},
		DecoderFactory: func(int, int) (Decoder, error) {
			return &mockDecoder{samples: make([]float32, frames*2)}, nil
		},
		EncoderFactory: func(int, int) (Encoder, error) {
			return &mockEncoder{}, nil
		},
		ClockDriven: true,
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("mxl1")
	m.SetActive("mxl1", true)

	pcm := build51PCM(frames, 0, 0, 0.1, 0, 0, 0)
	m.IngestPCM("mxl1", pcm, 1000, 6)
	m.IngestPCM("mxl1", pcm, 2000, 6)

	m.mu.RLock()
	ch := m.channels["mxl1"]
	warnedFirst := ch.surroundWarned
	m.mu.RUnlock()
	require.True(t, warnedFirst, "first surround ingest must set surroundWarned")

	m.RemoveChannel("mxl1")
	m.AddChannel("mxl1")
	m.SetActive("mxl1", true)

	m.mu.RLock()
	ch2 := m.channels["mxl1"]
	warnedAfterReconnect := ch2.surroundWarned
	m.mu.RUnlock()
	require.False(t, warnedAfterReconnect, "re-registered channel must reset the surround warning state")
}
