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
// point. It holds before a realtime clock has started, and with the
// program at the source's rate, where the frame store keeps only the
// frames a tick still to come may show.
//
// The engine clock stands still on the tick's time. The frame store
// judges by the clock at each frame's arrival which frames a tick may
// still show, and the tick here is built before the frames arrive: on the
// running clock a host slow enough to take a frame period over them moved
// that point past the tick's read point, and the frame it shows was gone.
func TestSourceDelayKeepsVideoAndAudioAligned(t *testing.T) {
	vr := clock.Rate{Num: 30000, Den: 1001}
	clock.Epoch()
	at := clock.TAINow()
	clock.SetTAISource(func() int64 { return at })
	t.Cleanup(func() { clock.SetTAISource(nil) })
	t.Cleanup(clock.ResetProgramRate())
	for _, tc := range []struct {
		name    string
		program clock.Rate
	}{
		{"before a realtime clock", clock.Rate{}},
		{"program at the source's rate", vr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clock.ResetProgramRate()
			if tc.program.Num > 0 {
				clock.SetProgramRate(tc.program)
			}
			checkSourceDelayAlignment(t, vr)
		})
	}
}

// checkSourceDelayAlignment delays a source at vr by 100 ms and checks
// the tick at the current time shows its video and plays its audio from
// the same media time.
func checkSourceDelayAlignment(t *testing.T, vr clock.Rate) {
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

	// The tick of grain now, built as the reference clock builds it: its
	// audio is read its own period's samples, ReadDelay periods' samples
	// behind its period, within a sample of the read grain's own.
	d := clock.ReadDelay(vr)
	read := now - d
	tick := clock.Tick{
		VideoPTS:      clock.IndexPTS(vr, now),
		ReadPTS:       clock.IndexPTS(vr, read),
		ReadSample:    sample(now) - sample(d),
		AudioSamples:  int(sample(now+1) - sample(now)),
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
	got := int64(pcm[0]*2*rampMod + 0.5)
	wantAudio := int64((sample(read) - delayMs*48) % rampMod)
	if d := (got-wantAudio+rampMod/2+rampMod)%rampMod - rampMod/2; d < -1 || d > 1 {
		t.Fatalf("audio plays media sample %d (mod %d), want %d or a sample either side, %d ms before the read point like the video",
			got, rampMod, wantAudio, delayMs)
	}
}
