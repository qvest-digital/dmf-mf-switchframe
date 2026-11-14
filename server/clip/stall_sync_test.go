package clip

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/clock"
)

// scriptedClock is the TAI and the wall time of a player under test: it
// moves only as far as the player waits on it, and as a test advances it
// to stall the player. Its player blocks once it would pass end, which
// closes reached, so what the player put out by then is the same on any
// host.
type scriptedClock struct {
	mu      sync.Mutex
	ns      int64
	end     int64
	reached chan struct{}
}

// newScriptedClock returns a clock running for d from the current time,
// installed as the TAI source for the rest of the test.
func newScriptedClock(t *testing.T, d time.Duration) *scriptedClock {
	t.Helper()
	now := time.Now().UnixNano()
	c := &scriptedClock{ns: now, end: now + int64(d), reached: make(chan struct{})}
	clock.Epoch()
	clock.SetTAISource(c.tai)
	t.Cleanup(func() { clock.SetTAISource(nil) })
	return c
}

func (c *scriptedClock) tai() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ns
}

func (c *scriptedClock) now() time.Time { return time.Unix(0, c.tai()) }

// advance moves the clock d on, as a stall of the player does.
func (c *scriptedClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ns += int64(d)
}

func (c *scriptedClock) sleep(ctx context.Context, d time.Duration) bool {
	c.mu.Lock()
	if c.ns+int64(d) <= c.end {
		c.ns += int64(d)
		c.mu.Unlock()
		return false
	}
	c.ns = c.end
	select {
	case <-c.reached:
	default:
		close(c.reached)
	}
	c.mu.Unlock()
	<-ctx.Done()
	return true
}

// run starts p on c and returns once p reached c's end.
func (c *scriptedClock) run(t *testing.T, p *Player) {
	t.Helper()
	p.clk = c
	ctx, cancel := context.WithCancel(context.Background())
	p.Start(ctx)
	t.Cleanup(func() { cancel(); p.Stop(); p.Wait() })
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
		r.chunks = append(r.chunks, pcmRun{pts, pcm[0], len(ch0), start, ch0})
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
				t.Run(fmt.Sprintf("%dHz/%d_%d/stall_%v", sr, r.Num, r.Den, stall), func(t *testing.T) {
					c := newScriptedClock(t, 3500*time.Millisecond)
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
						AudioRate: sr, PCMCodec: rampCodec,
					}
					rec.wire(&cfg)
					p := NewPlayer(cfg)
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

// A timed player that fell behind drops the slot due while its PCM runs
// on, and stays on its PTS; without PCM, or after a pause, a seek or a
// speed change, which place the PCM on the video again, it moves onto the
// clock, as it does a slot more than a period ahead.
func TestKeepOnTimelineDropsOnlyWhileThePCMRunsOn(t *testing.T) {
	newScriptedClock(t, time.Hour)
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
		{"ahead, PCM running", true, false, now + 2*1800, false, true},
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
