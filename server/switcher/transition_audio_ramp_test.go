package switcher

import (
	"fmt"
	"math"
	"sync"
	"testing"

	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/clock"
)

// mixFrameRates and mixSampleRates are the formats a mix's audio is
// checked at.
var (
	mixFrameRates = []clock.Rate{
		{Num: 24000, Den: 1001}, {Num: 24, Den: 1}, {Num: 25, Den: 1},
		{Num: 30000, Den: 1001}, {Num: 30, Den: 1}, {Num: 50, Den: 1},
		{Num: 60000, Den: 1001}, {Num: 60, Den: 1}, {Num: 100, Den: 1},
		{Num: 120, Den: 1}, {Num: 240, Den: 1},
	}
	mixSampleRates = []int{44100, 48000, 96000}
)

// audioTickAt builds the tick of program grain n with its audio period on
// sample rate sr, as the reference clock does.
func audioTickAt(s *Switcher, sr int, n uint64) clock.Tick {
	tk := tickAt(s, n)
	pf := s.pipelineFormat.Load()
	rv := clock.Rate{Num: int64(pf.FPSNum), Den: int64(pf.FPSDen)}
	ra := clock.Rate{Num: int64(sr), Den: 1}
	tk.AudioStart = clock.TimestampToIndex(ra, clock.IndexToTimestamp(rv, n))
	tk.AudioSamples = int(clock.TimestampToIndex(ra, clock.IndexToTimestamp(rv, n+1)) - tk.AudioStart)
	tk.AudioPTS = clock.IndexPTS(ra, tk.AudioStart)
	tk.ReadSample = clock.TimestampToIndex(ra, clock.IndexToTimestamp(rv, n-clock.ReaderDelayGrains))
	return tk
}

// A mix airs the incoming source from the tick it starts on, and each
// tick's audio ends at the position that tick's video shows: the audio
// does not trail the picture by a period. Periods join without a step,
// at completion too.
func TestMixAudioFollowsTheTicksVideoPosition(t *testing.T) {
	t.Parallel()
	for _, fps := range mixFrameRates {
		for _, sr := range mixSampleRates {
			t.Run(fmt.Sprintf("%dHz_%d_%d", sr, fps.Num, fps.Den), func(t *testing.T) {
				t.Parallel()
				const level = 0.5
				const periods = 8 // the mix's length in frames
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
				// On program before the mixer is attached, so no cut ramp
				// runs into the mix.
				onProgram(t, s, "mxl:a")
				s.RegisterMXLSource("mxl:b")
				s.SetSyncMixer(m)
				s.SetAACBuffer(clock.NewAACFrameBuffer(2, sr, mockAACEncode))
				s.SetMixer(m)
				s.SetTransitionConfig(TransitionConfig{})
				m.AddChannel("mxl:a")
				m.AddChannel("mxl:b")
				m.SetActive("mxl:a", true)

				// Both sources deliver one period ahead of the read point,
				// period by period, as a live source does.
				ra := clock.Rate{Num: int64(sr), Den: 1}
				g := grainNow(s)
				deliver := func(from, to uint64) {
					pts := clock.IndexPTS(ra, from)
					m.IngestPCM("mxl:a", constPCM(int(to-from), 0), pts, 2)
					m.IngestPCM("mxl:b", constPCM(int(to-from), level), pts, 2)
				}
				first := audioTickAt(s, sr, g)
				deliver(first.ReadSample-2*uint64(first.AudioSamples), audioTickAt(s, sr, g+1).ReadSample)

				durationMs := int(math.Round(periods * 1000 * float64(fps.Den) / float64(fps.Num)))
				if err := s.StartTransition(t.Context(), "mxl:b", "mix", durationMs, ""); err != nil {
					t.Fatal(err)
				}
				var shown []float64 // the position each tick's video shows, while mixing
				for i := uint64(0); i < periods+4; i++ {
					deliver(audioTickAt(s, sr, g+i+1).ReadSample, audioTickAt(s, sr, g+i+2).ReadSample)
					s.processTick(audioTickAt(s, sr, g+i))
					if m.IsInTransitionCrossfade() {
						shown = append(shown, m.TransitionPosition())
					}
				}
				if len(shown) < periods-2 {
					t.Fatalf("the mix ran %d ticks, want about %d", len(shown), periods)
				}

				mu.Lock()
				defer mu.Unlock()
				if len(aired) != periods+4 {
					t.Fatalf("%d periods aired, want %d", len(aired), periods+4)
				}
				if p := aired[0]; p[len(p)-1] <= 0 {
					t.Fatal("the tick the mix starts on airs none of the incoming source")
				}
				for k, pos := range shown {
					want := level * math.Sin(pos*math.Pi/2)
					if got := float64(aired[k][len(aired[k])-1]); math.Abs(got-want) > 1e-6 {
						t.Fatalf("tick %d: audio ends at %.6f, the video shows position %.4f (gain %.6f)",
							k, got, pos, want)
					}
				}
				for k := 1; k < len(aired); k++ {
					prev, cur := aired[k-1][len(aired[k-1])-1], aired[k][0]
					if math.Abs(float64(cur-prev)) > 1e-6 {
						t.Fatalf("tick %d starts at %.6f, the tick before ended at %.6f", k, cur, prev)
					}
				}
				if p := aired[len(aired)-1]; math.Abs(float64(p[0])-level) > 1e-6 {
					t.Fatalf("after the mix the source airs at %v, want %v", p[0], level)
				}
			})
		}
	}
}

// constPCM returns n stereo samples of value v.
func constPCM(n int, v float32) []float32 {
	pcm := make([]float32, 2*n)
	for i := range pcm {
		pcm[i] = v
	}
	return pcm
}
