package switcher

import (
	"testing"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/clock"
)

// A source delay holds the source's video and its audio back by the same
// amount, so the delayed source stays in lip sync: the tick shows the
// frame and plays the audio of one media time, the delay before its read
// point.
func TestSourceDelayKeepsVideoAndAudioAligned(t *testing.T) {
	s := newTestSwitcherDirect(distribution.NewRelay())
	defer s.Close()
	m := audio.NewMixer(audio.MixerConfig{SampleRate: 48000, Channels: 2})
	defer func() { _ = m.Close() }()
	s.SetSyncMixer(m)

	const key = "mxl:cam"
	s.RegisterMXLSource(key)
	m.AddChannel(key)
	m.SetActive(key, true)
	const delayMs = 100
	if err := s.SetSourceDelay(key, delayMs); err != nil {
		t.Fatal(err)
	}

	vr := clock.Rate{Num: 30000, Den: 1001}
	ar := clock.Rate{Num: 48000, Den: 1}
	const rampMod = 4096
	now := clock.TimestampToIndex(vr, clock.TAINow())
	first := now - 30
	sample := func(grain uint64) uint64 {
		return clock.TimestampToIndex(ar, clock.IndexToTimestamp(vr, grain))
	}
	for g := first; g < now; g++ {
		s.IngestRawVideo(key, &ProcessingFrame{
			Data: make([]byte, 320*240*3/2), Width: 320, Height: 240,
			PTS: clock.IndexPTS(vr, g), Format: ColorYUV420_8bit,
		})
		a, b := sample(g), sample(g+1)
		pcm := make([]float32, 2*(b-a))
		for i := range pcm {
			pcm[i] = float32((a+uint64(i/2))%rampMod) / (2 * rampMod)
		}
		m.IngestPCM(key, pcm, clock.IndexPTS(ar, a), 2)
	}

	read := now - 2
	tick := clock.Tick{
		VideoPTS:      clock.IndexPTS(vr, now),
		ReadPTS:       clock.IndexPTS(vr, read),
		ReadSample:    sample(read),
		AudioSamples:  int(sample(read+1) - sample(read)),
		FrameDuration: s.PipelineFormat().FrameDuration(),
	}
	pf, fresh := s.popProgramFrame(s.frameSync, key, tick)
	if pf == nil || !fresh {
		t.Fatal("no fresh frame of the delayed source at the read point")
	}
	defer pf.ReleaseYUV()
	pcm := m.ProduceSamples(tick.ReadSample, tick.AudioSamples, tick.AudioPTS)

	videoAt := clock.TimestampOf(pf.PTS)
	wantVideo := clock.IndexToTimestamp(vr, read) - delayMs*1_000_000
	frame := clock.IndexToTimestamp(vr, 1)
	if videoAt > wantVideo || videoAt <= wantVideo-frame {
		t.Fatalf("video shows media time %d ns before the read point, want the frame at %d ms before",
			clock.IndexToTimestamp(vr, read)-videoAt, delayMs)
	}
	got := uint64(pcm[0]*2*rampMod + 0.5)
	wantAudio := (sample(read) - delayMs*48) % rampMod
	if got != wantAudio {
		t.Fatalf("audio plays media sample %d (mod %d), want %d, %d ms before the read point like the video",
			got, rampMod, wantAudio, delayMs)
	}
}
