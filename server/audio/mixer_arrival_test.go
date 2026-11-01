//go:build cgo

package audio

import (
	"testing"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/clock"
)

// A relay source's AAC carries the source's own PTS, which is not on the
// engine timeline; its video is stamped with the TAI of its arrival. The
// mixer stamps the decoded audio the same way, so the tick reading the
// media time of its arrival plays it, rather than dropping it as audio
// from a far-away time.
func TestIngestFrameStampsDecodedAudioWithItsArrival(t *testing.T) {
	const frames, spf = 8, 1024
	enc, err := NewFDKEncoder(48000, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = enc.Close() }()
	m := NewMixer(MixerConfig{
		SampleRate: 48000, Channels: 2,
		DecoderFactory: func(sr, ch int) (Decoder, error) { return NewFDKDecoder(sr, ch) },
	})
	defer func() { _ = m.Close() }()
	m.AddChannel("cam1")
	m.SetActive("cam1", true)

	pcm := make([]float32, spf*2)
	for i := range pcm {
		pcm[i] = 0.1
	}
	for i := 0; i < frames+2; i++ {
		aac, err := enc.Encode(pcm)
		if err != nil {
			t.Fatal(err)
		}
		if len(aac) == 0 {
			continue
		}
		m.IngestFrame("cam1", &media.AudioFrame{
			Data: aac, PTS: 90000 + int64(i)*1920, SampleRate: 48000, Channels: 2,
		})
	}

	now := clock.TimestampToIndex(rate48k, clock.TAINow())
	out := m.ProduceSamples(now-3*spf, spf, 0)
	for i, v := range out {
		if v == 0 {
			t.Fatalf("sample %d read %d samples before the arrival is silent: the audio is not at its arrival media time", i/2, 3*spf)
		}
	}
	out = m.ProduceSamples(now+spf, spf, 0)
	for i, v := range out {
		if v != 0 {
			t.Fatalf("sample %d read after the arrival is %v: the audio is stamped later than it arrived", i/2, v)
		}
	}
}
