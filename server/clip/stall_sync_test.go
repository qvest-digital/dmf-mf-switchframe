package clip

import (
	"context"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/clock"
)

// taiClock is the TAI and the pace clock of a player under test: it
// moves only as far as the player waits on it, and as the player's
// decoder or a test advances it. The player blocks once the clock would
// pass end, which closes reached, so what the player put out by then is
// the same on any host.
type taiClock struct {
	mu      sync.Mutex
	ns      int64
	end     int64
	reached chan struct{}
	stop    chan struct{} // closed as the test ends
}

// newTAIClock returns a clock running for d from the current time,
// installed as the TAI source for the rest of the test.
func newTAIClock(t *testing.T, d time.Duration) *taiClock {
	t.Helper()
	now := time.Now().UnixNano()
	c := &taiClock{ns: now, end: now + int64(d), reached: make(chan struct{}), stop: make(chan struct{})}
	clock.Epoch()
	clock.SetTAISource(c.tai)
	t.Cleanup(func() { clock.SetTAISource(nil) })
	return c
}

func (c *taiClock) tai() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ns
}

func (c *taiClock) now() time.Time { return time.Unix(0, c.tai()) }

// advance moves the clock d on, as a stall of the player or a decode
// does. Past the end it blocks until the test ends.
func (c *taiClock) advance(d time.Duration) {
	if c.move(d) {
		<-c.stop
	}
}

// move moves the clock d on, and reports whether that reached its end,
// where it stays.
func (c *taiClock) move(d time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ns+int64(d) <= c.end {
		c.ns += int64(d)
		return false
	}
	c.ns = c.end
	select {
	case <-c.reached:
	default:
		close(c.reached)
	}
	return true
}

func (c *taiClock) wait(ctx context.Context, _ *time.Timer, d time.Duration) bool {
	if !c.move(d) {
		return false
	}
	<-ctx.Done()
	return true
}

// run starts p on c and returns once p reached c's end.
func (c *taiClock) run(t *testing.T, p *Player) {
	t.Helper()
	p.clock = c
	ctx, cancel := context.WithCancel(context.Background())
	p.Start(ctx)
	t.Cleanup(func() { cancel(); close(c.stop); p.Stop(); p.Wait() })
	select {
	case <-c.reached:
	case <-time.After(30 * time.Second):
		t.Fatal("the player did not reach the end of its clock")
	}
}

// rampDecoder decodes the AAC frames rateAudio returns to a ramp: sample
// j of frame i is i*1024+j+1, on every channel, so a sample's value is
// one more than its clip sample and silence is 0.
type rampDecoder struct{ channels int }

func (d *rampDecoder) Decode(adts []byte) ([]float32, error) {
	i := int(adts[len(adts)-2])<<8 | int(adts[len(adts)-1])
	pcm := make([]float32, 1024*d.channels)
	for j := range 1024 {
		for c := range d.channels {
			pcm[j*d.channels+c] = float32(i*1024 + j + 1)
		}
	}
	return pcm, nil
}

func (d *rampDecoder) Channels() int    { return d.channels }
func (d *rampDecoder) OutputDelay() int { return 0 }
func (d *rampDecoder) Close() error     { return nil }

var rampCodec = PCMCodec{
	NewDecoder: func(_, channels int) (PCMDecoder, error) { return &rampDecoder{channels}, nil },
}

// avRecorder records a timed player's pictures and PCM.
type avRecorder struct {
	mu     sync.Mutex
	video  []outRec // id is the picture, at the TAI it went out
	chunks []pcmRun
	onPic  func(n int) // called with the count of pictures put out so far
}

type pcmRun struct {
	pts   int64
	first float32 // value of the first sample
	n     int     // samples per channel
	start bool
	pcm   []float32 // channel 0
	at    int64     // TAI it was handed over at
}

func (r *avRecorder) wire(cfg *PlayerConfig) {
	cfg.RawVideoOutput = func(yuv []byte, _, _ int, pts int64, _ bool) {
		r.mu.Lock()
		r.video = append(r.video, outRec{yuv[0], pts, clock.TAINow()})
		n := len(r.video)
		r.mu.Unlock()
		if r.onPic != nil {
			r.onPic(n)
		}
	}
	cfg.PCMOutput = func(pcm []float32, pts int64, channels int, start bool) {
		ch0 := make([]float32, len(pcm)/channels)
		for i := range ch0 {
			ch0[i] = pcm[i*channels]
		}
		r.mu.Lock()
		r.chunks = append(r.chunks, pcmRun{pts, pcm[0], len(ch0), start, ch0, clock.TAINow()})
		r.mu.Unlock()
	}
}

// passes returns the PTS of each pass's first picture, and of the clip
// sample 0 of each pass of the audio, stamped at sr, in 1/sr ticks of
// 90 kHz so a sample's stamp is not rounded.
func (r *avRecorder) passes(sr int) (video []int64, audio []float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, f := range r.video {
		if f.id == 0 && (i == 0 || r.video[i-1].id != 0) {
			video = append(video, f.pts)
		}
	}
	for _, c := range r.chunks {
		for k, v := range c.pcm {
			if v == 1 {
				audio = append(audio, float64(c.pts)+float64(k)*90000/float64(sr))
			}
		}
	}
	return video, audio
}

// gap returns the first chunk of PCM that does not follow the one before
// it on the PTS its samples put it at, or starts the stream again; 0 for
// none.
func (r *avRecorder) gap(sr int) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	var put int64
	for i, c := range r.chunks {
		if i > 0 && (c.start || c.pts != r.chunks[0].pts+put*90000/int64(sr)) {
			return i
		}
		put += int64(c.n)
	}
	return 0
}

// back returns the first chunk of PCM stamped before the one before it
// ends, which plays over audio already handed over; 0 for none.
func (r *avRecorder) back(sr int) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := 1; i < len(r.chunks); i++ {
		prev := r.chunks[i-1]
		if r.chunks[i].pts < prev.pts+int64(prev.n)*90000/int64(sr) {
			return i
		}
	}
	return 0
}

// A timed player that the host stalls keeps its pictures on its PCM: the
// PCM runs on by itself, and the video drops the slots whose ticks the
// stall passed, so every pass of the pictures after the stall still
// starts on its pass of the sound, within a sample. Put back on the clock
// instead, the video played the stall's length after its sound from then
// on. A stall shorter than the PCM runs ahead leaves the PCM unbroken; a
// longer one has it skip the samples whose time passed, and it runs on,
// on the same timeline; it started again at the next picture's PTS,
// which the stall had left 130 ms before the audio already handed over,
// and played that much of it twice. The clip is 25 pictures at 25 fps,
// looping, its
// audio at the rate it is played at, on every program rate.
func TestTimedPlayerStaysInSyncThroughAStall(t *testing.T) {
	for _, sr := range sampleRates {
		for _, r := range programRates {
			period := time.Duration(int64(time.Second) * r.Den / r.Num)
			for _, stall := range []time.Duration{period / 2, period, 2 * period, 5 * period, 60 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond} {
				for _, cued := range []bool{false, sr == 48000} {
					t.Run(fmt.Sprintf("%dHz/%d_%d/stall_%v/cued_%v", sr, r.Num, r.Den, stall, cued), func(t *testing.T) {
						c := newTAIClock(t, 3500*time.Millisecond)
						frames := make([]BufferedFrame, 25)
						for i := range frames {
							frames[i] = BufferedFrame{wireData: []byte{byte(i)}, pts: int64(i) * 3600, isKeyframe: i == 0}
						}
						rec := &avRecorder{onPic: func(n int) {
							if n == 3 {
								c.advance(stall)
							}
						}}
						cfg := PlayerConfig{
							Clip: frames, AudioClip: rateAudio(sr*11/10/1024, sr), Speed: 1, Loop: true,
							OutputFPSNum: int(r.Num), OutputFPSDen: int(r.Den), Timed: true,
							AudioRate: sr, PCMCodec: rampCodec, Cued: cued,
						}
						rec.wire(&cfg)
						p := NewPlayer(cfg)
						if cued {
							p.Release()
						}
						c.run(t, p)

						video, audio := rec.passes(sr)
						require.GreaterOrEqual(t, len(video), 3, "pictures' passes")
						require.GreaterOrEqual(t, len(audio), 3, "sound's passes")
						tolerance := 90000/float64(sr) + 1
						for k := range 3 {
							require.InDelta(t, float64(video[k]), audio[k], tolerance,
								"pass %d of the pictures starts %.1f ticks after its sound", k, float64(video[k])-audio[k])
						}
						require.Zero(t, rec.back(sr), "the PCM stepped back after a %v stall", stall)
						if stall < pcmLead {
							require.Zero(t, rec.gap(sr), "the PCM broke off after a %v stall", stall)
						}
					})
				}
			}
		}
	}
}

// A timed player that fell behind drops the slot due while its PCM runs
// on, and stays on its PTS; one ahead waits for its slot. Without PCM, or
// after a pause, a seek or a speed change, which place the PCM on the
// video again, it moves onto the clock, late or ahead.
func TestKeepOnTimelineDropsOnlyWhileThePCMRunsOn(t *testing.T) {
	newTAIClock(t, time.Hour)
	s, err := NewPCMStream(rateAudio(10, 48000), 48000, rampCodec)
	require.NoError(t, err)
	defer s.Close()
	now := clock.NowPTS()
	for _, c := range []struct {
		name   string
		pcm    bool
		resync bool
		pts    int64
		drop   bool
		moved  bool
	}{
		{"on time", true, false, now - 1800, false, false},
		{"late, PCM running", true, false, now - 3*1800, true, false},
		{"late, PCM placed again", true, true, now - 3*1800, false, true},
		{"late, no PCM", false, false, now - 3*1800, false, true},
		{"ahead, PCM running", true, false, now + 2*1800, false, false},
		{"ahead, PCM placed again", true, true, now + 2*1800, false, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := NewPlayer(PlayerConfig{Timed: true})
			p.rate = clock.Rate{Num: 50, Den: 1}
			if c.pcm {
				p.pcm = s
			}
			p.pcmResync = c.resync
			pts := c.pts
			require.Equal(t, c.drop, p.keepOnTimeline(&pts), "dropped")
			require.Equal(t, c.moved, pts != c.pts, "moved from %d to %d", c.pts, pts)
		})
	}
}

// A timed player whose decoder is slower than real time keeps putting out
// pictures in sync with its audio. Dropping the slots it is late for
// catches up only a decoder faster than real time; one that stays slower
// dropped every slot from then on, and the clip froze while its audio
// played. After dropping for pcmLead, the player skips its decode to the
// next keyframe it can still show, or, with none left in the pass, places
// its video and its PCM on the clock again, which steps the audio once.
// So the video never stops for longer than that and a group of pictures,
// every picture it shows sits on its audio, and the PCM runs on without a
// gap or a step back, its sound stepping only at a counted re-place. The
// clip is 25 pictures at 25 fps, with a keyframe every sixth or only its
// first, decoded at 0.95 to 2 times real time, on every program rate.
func TestTimedPlayerWithASlowDecoderKeepsItsVideoOnItsAudio(t *testing.T) {
	type tc struct {
		r    clock.Rate
		sr   int
		cost float64 // decode time of a picture, in its periods
		gop  int     // pictures from one keyframe to the next; 0 for one
	}
	var cases []tc
	for _, r := range programRates {
		srs := []int{48000}
		if r == (clock.Rate{Num: 50, Den: 1}) {
			srs = sampleRates
		}
		for _, sr := range srs {
			for _, cost := range []float64{0.95, 1.05, 1.2, 2} {
				for _, gop := range []int{6, 0} {
					cases = append(cases, tc{r, sr, cost, gop})
				}
			}
		}
	}
	const picPeriod = 40 * time.Millisecond
	for _, c := range cases {
		t.Run(fmt.Sprintf("%dHz/%d_%d/cost_%v/gop_%d", c.sr, c.r.Num, c.r.Den, c.cost, c.gop), func(t *testing.T) {
			ck := newTAIClock(t, 5*time.Second)
			frames := make([]BufferedFrame, 25)
			for i := range frames {
				key := i == 0 || (c.gop > 0 && i%c.gop == 0)
				nal := byte(0x41)
				if key {
					nal = 0x65
				}
				frames[i] = BufferedFrame{wireData: []byte{0, 0, 0, 2, nal, byte(i)}, pts: int64(i) * 3600, isKeyframe: key}
			}
			dec := &threadedDecoder{cost: time.Duration(c.cost * float64(picPeriod)), clock: ck}
			rec := &avRecorder{}
			var mu sync.Mutex
			catchUps := map[string]int{}
			cfg := PlayerConfig{
				Clip: frames, AudioClip: rateAudio(c.sr*2/1024, c.sr), Speed: 1, Loop: true,
				OutputFPSNum: int(c.r.Num), OutputFPSDen: int(c.r.Den), Timed: true,
				AudioRate: c.sr, PCMCodec: rampCodec, Decoder: dec, DecodeFrame: dec.Decode,
				OnCatchUp: func(how string) { mu.Lock(); catchUps[how]++; mu.Unlock() },
			}
			rec.wire(&cfg)
			ck.run(t, NewPlayer(cfg))

			rec.mu.Lock()
			video := append([]outRec(nil), rec.video...)
			var pcm []float32
			var stamps []float64
			for _, ch := range rec.chunks {
				for k, v := range ch.pcm {
					pcm = append(pcm, v)
					stamps = append(stamps, float64(ch.pts)+float64(k)*90000/float64(c.sr))
				}
			}
			rec.mu.Unlock()
			require.Greater(t, len(video), 10, "slots put out")

			// The video never stops for longer than the drops a catch-up
			// waits for, the PCM it places the video behind, a group of
			// pictures and the decode of a few.
			gop := max(c.gop, 1)
			bound := 2*pcmLead + time.Duration(gop)*picPeriod + time.Duration((c.cost+1)*4*float64(picPeriod))
			for i := 1; i < len(video); i++ {
				gap := time.Duration(video[i].at - video[i-1].at)
				require.LessOrEqual(t, gap, bound, "the video stopped for %v before slot %d", gap, i)
			}

			// Every slot shows the picture its audio plays: the sample
			// stamped with its PTS lies in the picture's share of the
			// sound, to within two samples. The PCM is one run of samples
			// (checked below), so that sample is found from the PTS.
			for _, v := range video {
				// The first sample at or after the PTS, which is rounded to
				// a tick, more than a sample at 96 kHz: a picture within two
				// samples of the end of the PCM recorded may lie on PCM not
				// handed over yet.
				k := int(math.Ceil((float64(v.pts)-stamps[0])*float64(c.sr)/90000 - 0.01))
				if k < 0 || k >= len(pcm)-2 {
					continue
				}
				share := float64(int64(v.id)*int64(c.sr)/25 + 1)
				d := float64(pcm[k]) - share
				// The last samples of the pass before, as a pass that lasts
				// a fraction of a sample less than its slots leaves.
				for j := 1; j <= 2 && k+j < len(pcm) && d > float64(c.sr)/2; j++ {
					if pcm[k+j] < pcm[k+j-1] {
						d = float64(-j)
					}
				}
				require.True(t, d >= -2 && d <= float64(c.sr)/25+2,
					"picture %d at %d plays with sample %v of its sound, not %v to %v", v.id, v.pts, pcm[k], share, share+float64(c.sr)/25)
			}

			// The PCM runs on: no gap, no step back, and its sound steps
			// only at a pass's wrap and at a re-place.
			require.Zero(t, rec.gap(c.sr), "the PCM broke off")
			require.Zero(t, rec.back(c.sr), "the PCM stepped back")
			rec.mu.Lock()
			chunks := append([]pcmRun(nil), rec.chunks...)
			rec.mu.Unlock()
			for i, ch := range chunks {
				// A tick reads ReadDelay periods behind the clock.
				read := clock.PTS(ch.at) - int64(clock.ReadDelay(c.r))*90000*c.r.Den/c.r.Num
				require.GreaterOrEqual(t, ch.pts, read, "PCM chunk %d handed over after its read point", i)
			}
			steps := 0
			for k := 1; k < len(pcm); k++ {
				if pcm[k] != pcm[k-1]+1 && pcm[k] != 1 {
					steps++
				}
			}
			mu.Lock()
			defer mu.Unlock()
			require.LessOrEqual(t, steps, catchUps["replace"], "the sound stepped %d times, at %d re-places", steps, catchUps["replace"])
			switch {
			case c.cost < 1:
				require.Empty(t, catchUps, "a decoder faster than real time needed catching up")
			case c.gop == 0:
				require.Zero(t, catchUps["skip"], "a skip to a keyframe the clip does not have")
			}
			// A picture's last slots can still be on time when its first
			// are not, so a decoder only slightly slow may not need
			// catching up in the run; a fifth slower does.
			if c.cost >= 1.2 {
				how := "skip"
				if c.gop == 0 {
					how = "replace"
				}
				require.NotZero(t, catchUps[how], "a decoder %v times slower than real time never caught up by %s", c.cost, how)
			}
		})
	}
}
