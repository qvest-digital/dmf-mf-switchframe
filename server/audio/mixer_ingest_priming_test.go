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

func (d *primingDecoder) Channels() int { return 2 }

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
		DecoderFactory: func(sampleRate, ch int) (Decoder, error) {
			return dec, nil
		},
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
		DecoderFactory: func(sampleRate, ch int) (Decoder, error) {
			return dec, nil
		},
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

	// Ingest 10 frames. The first two are priming; the 3rd produces PCM and
	// triggers the one-time compensation scaled to the number of observed
	// priming skips (2). Subsequent valid frames must NOT re-apply.
	for i := 0; i < 10; i++ {
		m.IngestFrame("cam1", frame)
	}

	m.mu.RLock()
	ch := m.channels["cam1"]
	ringLen := ch.ringBuf.Len()
	m.mu.RUnlock()

	// 10 ingested - 2 priming = 8 valid decodes, plus 2 frames of compensation
	// silence (one per priming skip) = 10 frames total.
	expected := 10 * samplesPerFrame * channels
	require.Equal(t, expected, ringLen,
		"compensation should be 1 silence frame per observed priming skip, applied once per source")
	require.True(t, ch.primingOffsetApplied, "offset should be marked applied after first valid emission")
	require.Equal(t, 2, ch.primingSkipCount, "skip count should equal the number of empty decodes observed")
}

func TestIngestPCM_DoesNotApplyPrimingOffset(t *testing.T) {
	t.Parallel()

	const channels = 2
	m := NewMixer(MixerConfig{
		SampleRate: 48000,
		Channels:   channels,
		DecoderFactory: func(sampleRate, ch int) (Decoder, error) {
			return &mockDecoder{samples: make([]float32, 2048)}, nil
		},
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
	require.Zero(t, ch.primingSkipCount, "IngestPCM must never observe a priming skip")
	require.False(t, ch.primingOffsetApplied, "IngestPCM must never mark the offset applied")
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
		DecoderFactory: func(sampleRate, ch int) (Decoder, error) {
			factoryCalls++
			return &primingDecoder{primingN: 1, pcmPerCall: pcmChunk}, nil
		},
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
