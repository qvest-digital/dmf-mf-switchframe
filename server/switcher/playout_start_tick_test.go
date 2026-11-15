package switcher

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/playout"
)

// playoutClip plays its frames and reports their rate exactly, as a
// FileFrameIterator does.
type playoutClip struct {
	*playout.SliceIterator
	fps float64
}

func (c playoutClip) EstimateFPS() float64 { return c.fps }

type playoutClipAssets struct {
	frames []playout.DecodedFrame
	fps    float64
}

func (a playoutClipAssets) Open(string) (playout.FrameIterator, error) {
	return playoutClip{playout.NewSliceIterator(a.frames), a.fps}, nil
}

// stepClip is n pictures at fps, picture i with luma i+1, each with its
// share of 48 kHz stereo PCM at full scale, so the first sample the clip
// plays is a step from silence.
func stepClip(n int, fps float64) []playout.DecodedFrame {
	const rate = 48000
	at := func(i int) int64 { return int64(float64(i)*rate/fps + 0.5) }
	frames := make([]playout.DecodedFrame, n)
	for i := range frames {
		yuv := make([]byte, 64*48*3/2)
		yuv[0] = byte(i + 1)
		pcm := make([]float32, 2*(at(i+1)-at(i)))
		for k := range pcm {
			pcm[k] = 1
		}
		frames[i] = playout.DecodedFrame{
			YUV: yuv, Width: 64, Height: 48, PTS: int64(float64(i)*90000/fps + 0.5), IsKeyframe: i == 0,
			AudioFrames: []playout.AudioFrameData{{PCM: pcm, PTS: at(i) * 90000 / rate, SampleRate: rate, Channels: 2}},
		}
	}
	return frames
}

// A cut or a mix to a cued playout channel airs its first item from the
// first tick that shows it as program: that tick shows the item's first
// picture and plays its first sample at the tick's first sample, the next
// tick shows the picture after it, however late in a period the cut was
// made. Without it the channel stayed cued and program showed black; a
// channel started by hand and cut to later aired from whichever picture
// its own clock had reached.
func TestPlayoutChannelStartsOnTheFirstTickThatShowsIt(t *testing.T) {
	clock.Epoch()
	var now atomic.Int64
	now.Store(clock.TAINow())
	clock.SetTAISource(now.Load)
	t.Cleanup(func() { clock.SetTAISource(nil) })

	for _, mode := range []string{"cut", "mix"} {
		for _, fps := range mixFrameRates {
			// 1024ths of a period from the tick before the cut.
			for _, phase := range []int64{1, 1023} {
				t.Run(fmt.Sprintf("%s/%d_%d/%d_1024", mode, fps.Num, fps.Den, phase), func(t *testing.T) {
					playoutStartCase(t, &now, mode, fps, phase)
				})
			}
		}
	}
}

func playoutStartCase(t *testing.T, now *atomic.Int64, mode string, fps clock.Rate, phase int64) {
	const sr = 48000
	s := tickSwitcher(t)
	f := *s.pipelineFormat.Load()
	f.FPSNum, f.FPSDen = int(fps.Num), int(fps.Den)
	s.pipelineFormat.Store(&f)
	recordPipeline(t, s)

	m := audio.NewMixer(audio.MixerConfig{SampleRate: sr, Channels: 2})
	t.Cleanup(func() { _ = m.Close() })
	var mu sync.Mutex
	var aired [][]float32 // left channel, one slice per tick
	m.SetRawAudioSink(func(pcm []float32, _ int64, _, ch int) {
		left := make([]float32, len(pcm)/ch)
		for i := range left {
			left[i] = pcm[i*ch]
		}
		mu.Lock()
		aired = append(aired, left)
		mu.Unlock()
	})
	onProgram(t, s, "mxl:a")
	s.RegisterTimedReplaySource("playout:1")
	s.SetSyncMixer(m)
	s.SetAACBuffer(clock.NewAACFrameBuffer(2, sr, mockAACEncode))
	s.SetTransitionConfig(TransitionConfig{})
	m.AddChannel("mxl:a")
	m.AddChannel("playout:1")
	m.SetActive("playout:1", true)

	rate := float64(fps.Num) / float64(fps.Den)
	mgr := playout.NewManager(playout.ManagerConfig{
		AssetProvider:        playoutClipAssets{stepClip(200, rate), rate},
		AllowedLocalPrefixes: []string{"/"},
		PrerollItems:         1,
	})
	t.Cleanup(mgr.Close)
	mgr.SetPipelineFormat(64, 48, rate)
	reg := prometheus.NewRegistry()
	reg.MustRegister(playout.NewMetricsCollector(mgr, nil))

	// Each picture the channel puts out becomes a frame whose luma is the
	// picture's index plus one.
	var ingested, chunks atomic.Int64
	mgr.SetRawVideoOutput(func(key string, yuv []byte, _, _ int, pts int64, _ bool) {
		pf := mxlFrame(s, 0, yuv[0])
		pf.PTS = pts
		s.IngestRawVideo(key, pf)
		ingested.Add(1)
	})
	mgr.SetPCMOutput(func(key string, pcm []float32, pts int64, channels int, start bool) {
		m.IngestPCM(key, pcm, pts, channels)
		if start {
			m.PlaceChannel(key)
		}
		chunks.Add(1)
	})
	var started []string
	s.SetOnProgramTick(func(key string, readPTS int64) {
		started = append(started, key)
		if key == "playout:1" {
			mgr.StartCuedAt(1, readPTS)
		}
	})

	g := grainNow(s) + 4
	now.Store(clock.IndexToTimestamp(fps, g-1))
	if err := mgr.LoadPlaylist(1, &playout.Playlist{ID: "pl", Name: "pl", Items: []playout.PlaylistItem{
		{ID: "a", Type: playout.ItemClip, AssetURI: "local:///clips/a.ts", Title: "a"},
	}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return playoutPrerolled(t, reg) }, "the cued item prepared")

	s.processTick(audioTickAt(s, sr, g))
	period := clock.IndexToTimestamp(fps, g+1) - clock.IndexToTimestamp(fps, g)
	now.Store(clock.IndexToTimestamp(fps, g) + phase*period/1024)
	var err error
	if mode == "cut" {
		err = s.Cut(t.Context(), "playout:1")
	} else {
		durationMs := int(8 * 1000 * fps.Den / fps.Num)
		err = s.StartTransition(t.Context(), "playout:1", "mix", durationMs, "")
	}
	if err != nil {
		t.Fatal(err)
	}

	shown := func() (byte, int64) {
		ss := s.frameSync.source("playout:1")
		ss.mu.Lock()
		defer ss.mu.Unlock()
		if ss.lastRawVideo == nil {
			return 0, 0
		}
		return ss.lastRawVideo.Data[0], ss.lastRawVideo.PTS
	}
	first := audioTickAt(s, sr, g+1)
	s.processTick(first)
	if luma, pts := shown(); luma != 1 || pts != first.ReadPTS {
		t.Fatalf("the first tick that shows the channel shows picture %d stamped %+d ticks off its read point, want picture 0 on it",
			int(luma)-1, pts-first.ReadPTS)
	}
	// The run goes on from the picture after the first, on its own, and
	// puts the pictures already due out at once: the next tick reads up to
	// a sample into the third picture's sound.
	waitFor(t, func() bool { return ingested.Load() >= 3 && chunks.Load() >= 3 }, "the pictures after the first")
	next := audioTickAt(s, sr, g+2)
	s.processTick(next)
	if luma, pts := shown(); luma != 2 || pts > next.ReadPTS || pts < next.ReadPTS-1 {
		t.Fatalf("the tick after shows picture %d stamped %+d ticks off its read point, want picture 1 on it",
			int(luma)-1, pts-next.ReadPTS)
	}
	if len(started) != 2 || started[0] != "mxl:a" || started[1] != "playout:1" {
		t.Fatalf("program tick calls %v, want [mxl:a playout:1]", started)
	}
	waitFor(t, func() bool { return mgr.ChannelStates()[0].State == playout.StateOnAir.String() }, "the channel on air")

	mu.Lock()
	defer mu.Unlock()
	if len(aired) != 3 {
		t.Fatalf("%d periods aired, want 3", len(aired))
	}
	for i, v := range aired[0] {
		if v != 0 {
			t.Fatalf("the tick before the cut plays channel audio at sample %d", i)
		}
	}
	// Sample 0 is where the step crosses half scale, placed to the sample
	// but for the read point's rounding to 90 kHz.
	at := -1
	for i, v := range append(aired[1], aired[2]...) {
		if v >= 0.5 {
			at = i
			break
		}
	}
	if at < 0 || at > 1 {
		t.Fatalf("sample 0 plays %d samples into the first tick that shows the channel, want 0", at)
	}
	// The sound goes on from the picture after the first without a gap.
	for i, v := range aired[2] {
		if v < 0.5 {
			t.Fatalf("the tick after the first plays a gap at sample %d", i)
		}
	}
}

// playoutPrerolled reports whether playout channel 1 holds its cued item
// prepared.
func playoutPrerolled(t *testing.T, reg *prometheus.Registry) bool {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() != "switchframe_playout_prerolled_items" {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "channel" && l.GetValue() == "1" && m.GetGauge().GetValue() >= 1 {
					return true
				}
			}
		}
	}
	return false
}
