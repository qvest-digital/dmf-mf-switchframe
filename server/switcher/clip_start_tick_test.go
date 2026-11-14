package switcher

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/clip"
	"github.com/zsiec/switchframe/server/clock"
)

// stepDecoder decodes every AAC frame to 1024 samples per channel at
// full scale, so the first sample a clip plays is a step from silence.
type stepDecoder struct{ ch int }

func (d stepDecoder) Decode([]byte) ([]float32, error) {
	out := make([]float32, 1024*d.ch)
	for i := range out {
		out[i] = 1
	}
	return out, nil
}
func (d stepDecoder) Channels() int    { return d.ch }
func (d stepDecoder) OutputDelay() int { return 0 }
func (d stepDecoder) Close() error     { return nil }

var stepCodec = clip.PCMCodec{
	NewDecoder: func(_, channels int) (clip.PCMDecoder, error) { return stepDecoder{channels}, nil },
	NewResampler: func(srcRate, dstRate, channels int) clip.PCMResampler {
		return audio.NewResampler(srcRate, dstRate, channels)
	},
}

// A cut or a mix to a cued clip player airs the clip from the first tick
// that shows it as program: that tick shows the clip's first picture and
// plays its first sample at the tick's first sample, the next tick shows
// the slot after it, however late in a period the cut was made. Started
// from the cut instead, a player that woke in the last few milliseconds
// before the next tick, or after it, started a tick late: the cut tick
// showed the cued still a second time and played silence.
func TestClipStartsOnTheFirstTickThatShowsIt(t *testing.T) {
	frames, aac, err := clip.DemuxFile("../clip/testdata/h264-aac-stereo.mp4")
	if err != nil {
		t.Fatal(err)
	}
	clock.Epoch()
	var now atomic.Int64
	now.Store(clock.TAINow())
	clock.SetTAISource(now.Load)
	t.Cleanup(func() { clock.SetTAISource(nil) })

	for _, mode := range []string{"cut", "mix"} {
		for _, fps := range mixFrameRates {
			for _, sr := range mixSampleRates {
				// 1024ths of a period from the tick before the cut: right
				// after it, mid-period, and the last few microseconds.
				for _, phase := range []int64{1, 512, 1000, 1023} {
					t.Run(fmt.Sprintf("%s/%d_%d/%dHz/%d_1024", mode, fps.Num, fps.Den, sr, phase), func(t *testing.T) {
						clipStartCase(t, &now, frames, aac, mode, fps, sr, phase)
					})
				}
			}
		}
	}
}

func clipStartCase(t *testing.T, now *atomic.Int64, frames []clip.BufferedFrame, aac []clip.BufferedAudioFrame, mode string, fps clock.Rate, sr int, phase int64) {
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
	// The mixer runs no crossfade, so what the clip channel plays is what
	// it delivered.
	onProgram(t, s, "mxl:a")
	s.RegisterTimedReplaySource("clip:1")
	s.SetSyncMixer(m)
	s.SetAACBuffer(clock.NewAACFrameBuffer(2, sr, mockAACEncode))
	s.SetTransitionConfig(TransitionConfig{})
	m.AddChannel("mxl:a")
	m.AddChannel("clip:1")
	m.SetActive("clip:1", true)

	// Each picture the player puts out becomes a frame whose luma is the
	// picture's index plus one.
	picture := func(wire []byte) int {
		for i := range frames {
			if &frames[i].WireData()[0] == &wire[0] {
				return i
			}
		}
		t.Fatal("a picture the clip does not hold")
		return -1
	}
	var ingested atomic.Int64
	p := clip.NewPlayer(clip.PlayerConfig{
		Clip: frames, AudioClip: aac,
		OutputFPSNum: f.FPSNum, OutputFPSDen: f.FPSDen,
		Timed: true, Cued: true,
		AudioRate: sr, PCMCodec: stepCodec,
		RawVideoOutput: func(wire []byte, _, _ int, pts int64, _ bool) {
			pf := mxlFrame(s, 0, byte(picture(wire)+1))
			pf.PTS = pts
			s.IngestRawVideo("clip:1", pf)
			ingested.Add(1)
		},
		PCMOutput: func(pcm []float32, pts int64, channels int, start bool) {
			m.IngestPCM("clip:1", pcm, pts, channels)
			if start {
				m.PlaceChannel("clip:1")
			}
		},
	})
	var started []string
	s.SetOnProgramTick(func(key string, readPTS int64) {
		started = append(started, key)
		if key == "clip:1" {
			p.StartAt(readPTS)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); p.Stop(); p.Wait() })

	g := grainNow(s) + 4
	now.Store(clock.IndexToTimestamp(fps, g-1))
	p.Start(ctx)
	waitFor(t, func() bool { return ingested.Load() == 1 }, "the cued still")

	s.processTick(audioTickAt(s, sr, g))
	period := clock.IndexToTimestamp(fps, g+1) - clock.IndexToTimestamp(fps, g)
	now.Store(clock.IndexToTimestamp(fps, g) + phase*period/1024)
	var err error
	if mode == "cut" {
		err = s.Cut(t.Context(), "clip:1")
	} else {
		durationMs := int(8 * 1000 * fps.Den / fps.Num)
		err = s.StartTransition(t.Context(), "clip:1", "mix", durationMs, "")
	}
	if err != nil {
		t.Fatal(err)
	}

	shown := func() (byte, int64) {
		ss := s.frameSync.source("clip:1")
		ss.mu.Lock()
		defer ss.mu.Unlock()
		return ss.lastRawVideo.Data[0], ss.lastRawVideo.PTS
	}
	first := audioTickAt(s, sr, g+1)
	s.processTick(first)
	if luma, pts := shown(); luma != 1 || pts != first.ReadPTS {
		t.Fatalf("the first tick that shows the clip shows picture %d stamped %+d periods off its read point, want picture 0 on it",
			int(luma)-1, (pts-first.ReadPTS)*fps.Num/(90000*fps.Den))
	}
	// The playback goes on from the slot after the first, on its own. A
	// slot's PTS runs on in whole 90 kHz ticks, so it is at most one short
	// of the read point.
	waitFor(t, func() bool { return ingested.Load() >= 3 }, "the slot after the first")
	next := audioTickAt(s, sr, g+2)
	s.processTick(next)
	if _, pts := shown(); pts <= first.ReadPTS || pts < next.ReadPTS-1 {
		t.Fatalf("the tick after shows a slot %+d periods off its read point, want the slot after the first",
			(pts-next.ReadPTS)*fps.Num/(90000*fps.Den))
	}
	if len(started) != 2 || started[0] != "mxl:a" || started[1] != "clip:1" {
		t.Fatalf("program tick calls %v, want [mxl:a clip:1]", started)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(aired) != 3 {
		t.Fatalf("%d periods aired, want 3", len(aired))
	}
	for i, v := range aired[0] {
		if v != 0 {
			t.Fatalf("the tick before the cut plays clip audio at sample %d", i)
		}
	}
	// Clip sample 0 is where the step crosses half scale. It is stamped
	// with the read point as a 90 kHz PTS, which the mixer places to the
	// sample but for its rounding: a read point within a few microseconds
	// of the midpoint of two samples lands on the second.
	at := -1
	for i, v := range append(aired[1], aired[2]...) {
		if v >= 0.5 {
			at = i
			break
		}
	}
	if at < 0 || at > 1 {
		t.Fatalf("clip sample 0 plays %d samples into the first tick that shows the clip, want 0", at)
	}
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("no %s", what)
		}
		time.Sleep(100 * time.Microsecond)
	}
}
