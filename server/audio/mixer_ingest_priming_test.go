package audio

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

// primingDecoder simulates an AAC-LC decoder that discards the first frame
// as priming (returns nil) and thereafter produces constant PCM.
type primingDecoder struct {
	calls      int
	primingN   int
	pcmPerCall []float32
}

func (d *primingDecoder) Decode(aacFrame []byte) ([]float32, error) {
	d.calls++
	if d.calls <= d.primingN {
		return nil, nil
	}
	out := make([]float32, len(d.pcmPerCall))
	copy(out, d.pcmPerCall)
	return out, nil
}

func (d *primingDecoder) Close() error { return nil }

func TestIngestFrame_CompensatesForDecoderPriming(t *testing.T) {
	t.Parallel()

	const samplesPerFrame = 1024
	const channels = 2
	pcmChunk := make([]float32, samplesPerFrame*channels)
	for i := range pcmChunk {
		pcmChunk[i] = 0.25
	}

	dec := &primingDecoder{primingN: 1, pcmPerCall: pcmChunk}
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   channels,
		Output:     func(frame *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, ch int) (Decoder, error) {
			return dec, nil
		},
		EncoderFactory: func(sampleRate, ch int) (Encoder, error) {
			return &mockEncoder{}, nil
		},
		ClockDriven: true, // avoid ticker draining the ring buffer
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	adts := []byte{0xFF, 0xF1, 0x50, 0x80, 0x02, 0x00, 0xFC}
	frame := &media.AudioFrame{
		Data:       adts,
		SampleRate: 48000,
		Channels:   channels,
	}

	const ingestedFrames = 3
	for i := 0; i < ingestedFrames; i++ {
		m.IngestFrame("cam1", frame)
	}

	m.mu.RLock()
	ch := m.channels["cam1"]
	ringLen := ch.ringBuf.Len()
	m.mu.RUnlock()

	expected := ingestedFrames * samplesPerFrame * channels
	require.Equal(t, expected, ringLen,
		"ring buffer should contain one frame's worth of PCM per ingested AAC frame "+
			"(priming silence compensates for the skipped first decode)")
}

func TestIngestFrame_PrimingOffsetAppliedOncePerSource(t *testing.T) {
	t.Parallel()

	const samplesPerFrame = 1024
	const channels = 2
	pcmChunk := make([]float32, samplesPerFrame*channels)
	for i := range pcmChunk {
		pcmChunk[i] = 0.1
	}

	dec := &primingDecoder{primingN: 2, pcmPerCall: pcmChunk}
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   channels,
		Output:     func(frame *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, ch int) (Decoder, error) {
			return dec, nil
		},
		EncoderFactory: func(sampleRate, ch int) (Encoder, error) {
			return &mockEncoder{}, nil
		},
		ClockDriven: true,
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	adts := []byte{0xFF, 0xF1, 0x50, 0x80, 0x02, 0x00, 0xFC}
	frame := &media.AudioFrame{
		Data:       adts,
		SampleRate: 48000,
		Channels:   channels,
	}

	// Ingest 10 frames. The first two are priming; only the 3rd produces PCM
	// and triggers the one-time compensation. Subsequent valid frames must
	// NOT re-apply the offset.
	for i := 0; i < 10; i++ {
		m.IngestFrame("cam1", frame)
	}

	m.mu.RLock()
	ch := m.channels["cam1"]
	ringLen := ch.ringBuf.Len()
	m.mu.RUnlock()

	// 10 ingested - 2 priming = 8 valid decodes * framePairs = 8*2048 sample values,
	// plus the one-time compensation of 1 frame of silence (2048 values) = 9 frames total.
	expected := 9 * samplesPerFrame * channels
	require.Equal(t, expected, ringLen,
		"compensation should be applied exactly once per source, not on every valid decode")
}

func TestIngestPCM_DoesNotApplyPrimingOffset(t *testing.T) {
	t.Parallel()

	const channels = 2
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   channels,
		Output:     func(frame *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, ch int) (Decoder, error) {
			return &mockDecoder{samples: make([]float32, 2048)}, nil
		},
		EncoderFactory: func(sampleRate, ch int) (Encoder, error) {
			return &mockEncoder{}, nil
		},
		ClockDriven: true,
	})
	defer func() { _ = m.Close() }()

	m.AddChannel("mxl1")
	m.SetActive("mxl1", true)

	// 4 interleaved stereo samples = 2 sample frames.
	pcm := []float32{0.1, 0.2, 0.3, 0.4}
	m.IngestPCM("mxl1", pcm, 1000, channels)

	m.mu.RLock()
	ch := m.channels["mxl1"]
	ringLen := ch.ringBuf.Len()
	m.mu.RUnlock()

	require.Equal(t, len(pcm), ringLen,
		"IngestPCM path must not insert priming silence — raw PCM has no decoder priming")
}

func TestIngestFrame_PrimingOffsetReappliedAfterReconnect(t *testing.T) {
	t.Parallel()

	const samplesPerFrame = 1024
	const channels = 2
	pcmChunk := make([]float32, samplesPerFrame*channels)
	for i := range pcmChunk {
		pcmChunk[i] = 0.25
	}

	var factoryCalls int
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   channels,
		Output:     func(frame *media.AudioFrame) {},
		DecoderFactory: func(sampleRate, ch int) (Decoder, error) {
			factoryCalls++
			return &primingDecoder{primingN: 1, pcmPerCall: pcmChunk}, nil
		},
		EncoderFactory: func(sampleRate, ch int) (Encoder, error) {
			return &mockEncoder{}, nil
		},
		ClockDriven: true,
	})
	defer func() { _ = m.Close() }()

	adts := []byte{0xFF, 0xF1, 0x50, 0x80, 0x02, 0x00, 0xFC}
	frame := &media.AudioFrame{
		Data:       adts,
		SampleRate: 48000,
		Channels:   channels,
	}

	// First connection.
	m.AddChannel("cam1")
	m.SetActive("cam1", true)
	for i := 0; i < 3; i++ {
		m.IngestFrame("cam1", frame)
	}

	m.mu.RLock()
	firstLen := m.channels["cam1"].ringBuf.Len()
	m.mu.RUnlock()
	require.Equal(t, 3*samplesPerFrame*channels, firstLen)

	// Disconnect + reconnect.
	m.RemoveChannel("cam1")
	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	for i := 0; i < 3; i++ {
		m.IngestFrame("cam1", frame)
	}

	m.mu.RLock()
	secondLen := m.channels["cam1"].ringBuf.Len()
	m.mu.RUnlock()

	require.Equal(t, 3*samplesPerFrame*channels, secondLen,
		"reconnect yields a fresh decoder, so priming compensation must be re-applied")
	require.Equal(t, 2, factoryCalls, "a new decoder should be built after reconnect")
}
