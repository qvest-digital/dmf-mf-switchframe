package playout

import (
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/internal/video"
)

// A playout channel's video and audio are placed on the engine timeline by
// a clock.Anchor, fixed at the first frame because the channel's ticker
// runs on the engine's own clock. A run loop that falls more than
// maxTickBacklog behind skips ahead and loses those frames; its PTS must
// skip with it. PTS that run on as if no tick was lost lag the clock by the
// skip from then on, under the 2 s at which the anchor places a source
// again, and every AAC frame lands behind the mixer's read point: the
// channel stays silent for good.
func TestPlayoutKeepsItsClockAcrossASkipAhead(t *testing.T) {
	const (
		fps     = 48000.0 / 1024 // one AAC frame a video frame: both step 1920 ticks
		samples = 1024
		stall   = 1200 * time.Millisecond
		skipAt  = 250
		fires   = 1000
	)
	taiStart := clock.TAINow()
	wallStart := time.Now()
	var wall time.Duration
	clock.SetTAISource(func() int64 { return taiStart + int64(wall) })
	t.Cleanup(func() { clock.SetTAISource(nil) })
	now := func() time.Time { return wallStart.Add(wall) }

	yuv := make([]byte, video.YUV420FrameSize(16, 16))
	frames := make([]DecodedFrame, 2*fires)
	for i := range frames {
		frames[i] = DecodedFrame{
			YUV: yuv, Width: 16, Height: 16, PTS: int64(i) * 1920, IsKeyframe: i == 0,
			AudioData: []byte{1}, AudioPTS: int64(i) * 1920, AudioSampleRate: 48000, AudioChannels: 2,
		}
	}
	ch := NewChannel(1, ChannelConfig{
		AssetProvider: NewFakeAssetProvider(frames), Width: 16, Height: 16, FPS: fps,
		AllowedLocalPrefixes: []string{"/"},
	})
	m := audio.NewMixer(audio.MixerConfig{SampleRate: 48000, Channels: 2})
	t.Cleanup(func() { _ = m.Close() })
	m.AddChannel("playout:1")
	m.SetActive("playout:1", true)

	var anchor clock.Anchor
	pcm := make([]float32, 2*samples)
	for i := range pcm {
		pcm[i] = 0.25
	}
	var lag []int64 // per video frame: the clock less its placed PTS
	ch.SetRawVideoOutput(func(_ []byte, _, _ int, pts int64, _ bool) {
		lag = append(lag, clock.NowPTS()-anchor.PTS(pts))
	})
	ch.SetAudioOutput(func(_ []byte, pts int64, _, _ int) {
		m.IngestPCM("playout:1", pcm, anchor.PTS(pts), 2)
	})
	if err := ch.LoadPlaylist(testPlaylist(clipItem("a"))); err != nil {
		t.Fatal(err)
	}
	ch.mu.Lock()
	ch.state = StateOnAir
	ch.scheduler.Start()
	ch.mu.Unlock()

	period := ch.frameDuration
	k := tickClock{period: period, next: now().Add(period)}
	rate := clock.Rate{Num: 48000, Den: 1}
	silentBefore, silentAfter := 0, 0
	for i := 1; i <= fires; i++ {
		wall = time.Duration(i) * period
		if i >= skipAt {
			wall += stall // the loop is held up once and fires late from then on
		}
		if ch.runDue(&k, now) {
			t.Fatal("the playlist completed")
		}
		read := clock.TimestampToIndex(rate, clock.TAINow()) - 2*samples
		out := m.ProduceSamples(read, samples, clock.IndexPTS(rate, read))
		silent := true
		for _, v := range out {
			if v != 0 {
				silent = false
				break
			}
		}
		switch {
		case !silent || i <= 10:
		case i < skipAt:
			silentBefore++
		default:
			silentAfter++
		}
	}
	t.Logf("silent reads: %d before the skip, %d of %d after it", silentBefore, silentAfter, fires-skipAt+1)
	if silentBefore != 0 || silentAfter != 0 {
		t.Fatalf("silent reads: %d before the skip, %d of %d after it (anchor moves %d)",
			silentBefore, silentAfter, fires-skipAt+1, anchor.Moves())
	}
	first, final := lag[0], lag[len(lag)-1]
	if d := final - first; d < -1920 || d > 1920 {
		t.Fatalf("the channel's video lags the clock by %d ticks more after the skip; want its PTS to skip with it", d)
	}
}
