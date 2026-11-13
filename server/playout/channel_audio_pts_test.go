package playout

import (
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/internal/video"
)

// A playout channel places its video and its audio on the engine timeline
// through one clock.Anchor, by two PTS counters: the video PTS advances
// with every frame, the audio PTS with every AAC frame. An item without
// audio of its own (a slate, a live item, a clip without a sound track)
// advances the video PTS only. The next clip's audio then carries PTS that
// lag its video by that item's length, under the 2 s at which the anchor
// places a source again, and every AAC frame lands behind the mixer's read
// point: the clip airs silent to its end.
func TestPlayoutAudioFollowsVideoAfterAnItemWithoutAudio(t *testing.T) {
	const (
		fps     = 48000.0 / 1024 // one AAC frame a video frame: both step 1920 ticks
		samples = 1024
		clipLen = 100 // frames, about 2.1 s
		slateMs = 1000
	)
	taiStart := clock.TAINow()
	wallStart := time.Now()
	var wall time.Duration
	clock.SetTAISource(func() int64 { return taiStart + int64(wall) })
	t.Cleanup(func() { clock.SetTAISource(nil) })
	now := func() time.Time { return wallStart.Add(wall) }

	yuv := make([]byte, video.YUV420FrameSize(16, 16))
	frames := make([]DecodedFrame, clipLen)
	for i := range frames {
		frames[i] = DecodedFrame{
			YUV: yuv, Width: 16, Height: 16, PTS: int64(i) * 1920, IsKeyframe: i == 0,
			AudioFrames: []AudioFrameData{{Data: []byte{1}, PTS: int64(i) * 1920, SampleRate: 48000, Channels: 2}},
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
	clipB := false // the second clip is on air
	ch.SetRawVideoOutput(func(_ []byte, _, _ int, pts int64, _ bool) {
		anchor.PTS(pts)
	})
	ch.SetAudioOutput(func(_ []byte, pts int64, _, _ int) {
		m.IngestPCM("playout:1", pcm, anchor.PTS(pts), 2)
	})
	if err := ch.LoadPlaylist(testPlaylist(clipItem("a"), slateItem("s", slateMs), clipItem("b"))); err != nil {
		t.Fatal(err)
	}
	ch.mu.Lock()
	ch.state = StateOnAir
	ch.scheduler.Start()
	ch.mu.Unlock()

	period := ch.frameDuration
	k := tickClock{period: period, next: now().Add(period)}
	rate := clock.Rate{Num: 48000, Den: 1}
	silentA, silentB, readsB, checkedB := 0, 0, 0, 0
	for i := 1; ; i++ {
		wall = time.Duration(i) * period
		if ch.runDue(&k, now) {
			break
		}
		if ch.Status().CurrentItemID == "b" && !clipB {
			clipB = true
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
		case ch.Status().CurrentItemID == "a" && i > 10 && i < clipLen-5:
			if silent {
				silentA++
			}
		case clipB:
			readsB++
			if readsB > 10 && readsB < clipLen-5 {
				checkedB++
				if silent {
					silentB++
				}
			}
		}
		if i > 10*clipLen {
			t.Fatal("the playlist did not complete")
		}
	}
	if silentA != 0 {
		t.Fatalf("%d silent reads in the first clip", silentA)
	}
	if silentB != 0 {
		t.Fatalf("%d of %d reads silent in the clip after the slate (anchor moves %d); want its audio on its video",
			silentB, checkedB, anchor.Moves())
	}
}
