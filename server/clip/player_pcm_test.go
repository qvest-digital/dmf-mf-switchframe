//go:build cgo

package clip

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/internal/aacfixture"
)

type pcmChunk struct {
	pcm   []float32
	pts   int64
	start bool
}

// pcmRecorder records a player's first picture and its PCM.
type pcmRecorder struct {
	mu       sync.Mutex
	firstPTS []int64 // PTS of the first slot of picture 0, each pass
	last     int     // the picture last put out, -1 for none
	chunks   []pcmChunk
}

func (r *pcmRecorder) wire(cfg *PlayerConfig) {
	r.last = -1
	cfg.RawVideoOutput = func(yuv []byte, _, _ int, pts int64, _ bool) {
		r.mu.Lock()
		defer r.mu.Unlock()
		if yuv[0] == 0 && r.last != 0 {
			r.firstPTS = append(r.firstPTS, pts)
		}
		r.last = int(yuv[0])
	}
	cfg.PCMOutput = func(pcm []float32, pts int64, channels int, start bool) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.chunks = append(r.chunks, pcmChunk{append([]float32(nil), pcm...), pts, start})
	}
}

// samples returns the PCM recorded, checking that every chunk starts
// where the one before it ends.
func (r *pcmRecorder) samples(t *testing.T, channels int) []float32 {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	require.NotEmpty(t, r.chunks)
	var out []float32
	base := r.chunks[0].pts
	for i, c := range r.chunks {
		at := int64(len(out) / channels)
		require.Equal(t, base+at*90000/48000, c.pts, "chunk %d does not continue the audio before it", i)
		assert.Equal(t, i == 0, c.start, "chunk %d start", i)
		out = append(out, c.pcm...)
	}
	return out
}

// restart returns the first chunk at which the PCM started again: one
// not continuing the audio before it, or a second start. 0 when none did.
func (r *pcmRecorder) restart(channels int) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	var at int64
	for i, c := range r.chunks {
		if i > 0 && (c.start || c.pts != r.chunks[0].pts+at*90000/48000) {
			return i
		}
		at += int64(len(c.pcm) / channels)
	}
	return 0
}

// pictures returns n pictures at 25 fps, each carrying its index.
func pictures(n int) []BufferedFrame {
	frames := make([]BufferedFrame, n)
	for i := range frames {
		frames[i] = BufferedFrame{wireData: []byte{byte(i)}, pts: int64(i) * 3600, isKeyframe: i == 0}
	}
	return frames
}

// A timed player stamps the first sample of the clip's sound with the PTS
// of its first picture and every sample after it on from there. It
// played the AAC frames from the first on, and the first is the encoder
// priming: the sound came a frame late.
func TestPlayerPCMStartsOnTheFirstPicture(t *testing.T) {
	for _, ext := range []string{".ts", ".mp4"} {
		t.Run(ext, func(t *testing.T) {
			audio, ref := fixtureAudio(t, aacfixture.Clip{Ext: ext, Channels: 2})
			var rec pcmRecorder
			cfg := PlayerConfig{
				Clip: pictures(25), AudioClip: audio, Speed: 1,
				OutputFPSNum: 50, OutputFPSDen: 1, Timed: true, PCMCodec: testPCMCodec,
			}
			rec.wire(&cfg)
			p := NewPlayer(cfg)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			p.Start(ctx)
			defer func() { p.Stop(); p.Wait() }()
			require.Eventually(t, func() bool { return p.State() == StateHolding }, 20*time.Second, 10*time.Millisecond)
			time.Sleep(50 * time.Millisecond)

			got := rec.samples(t, 2)
			rec.mu.Lock()
			first := rec.firstPTS[0]
			startPTS := rec.chunks[0].pts
			rec.mu.Unlock()
			assert.Equal(t, first, startPTS, "the sound starts on the first picture")
			assert.Zero(t, aacfixture.Lag(got, ref, 2, 0, 3000, 20000), "the first sample is the sound's first")
			assert.GreaterOrEqual(t, len(got), len(ref), "the whole sound plays")
		})
	}
}

// A loop plays a pass of the sound for each pass of the pictures, the
// next following the last sample without a gap, an overlap or the
// priming: every pass is the sound from its first sample, on the first
// picture of the pass. The AAC frames, played as they are, put the
// priming in again at every wrap and lost the last 1024 samples to it.
//
// A host that stalls the player for more than two periods has it put its
// video back on the clock while its PCM runs on, and for more than its
// PCM lead start the PCM again (TestPlayerPCMRunsOnThroughAStall): either
// moves a pass off its audio for reasons that are not the loop's. A play
// that shows either is played again, a few times at most.
func TestPlayerPCMLoopIsGapless(t *testing.T) {
	const attempts = 10
	for _, ext := range []string{".ts", ".mp4"} {
		t.Run(ext, func(t *testing.T) {
			audio, ref := fixtureAudio(t, aacfixture.Clip{Ext: ext, Channels: 2})
			for attempt := 1; ; attempt++ {
				rec, moves := playLoop(t, audio)
				stall := ""
				if moves > 0 {
					stall = fmt.Sprintf("the video was put back on the clock %d times", moves)
				} else if i := rec.restart(2); i > 0 {
					stall = fmt.Sprintf("the PCM started again at chunk %d", i)
				}
				if stall == "" {
					checkLoopPasses(t, rec, ref)
					return
				}
				if attempt == attempts {
					t.Fatalf("the host stalled the player in all %d plays, the last time: %s", attempts, stall)
				}
				t.Logf("play %d: %s; playing again", attempt, stall)
			}
		})
	}
}

// loopPasses is how many passes of a loop playLoop records past the first.
const loopPasses = 3

// playLoop plays 25 pictures at 25 fps with audio in a loop at 50 fps
// until the pictures' fourth pass starts, and returns what it put out and
// how often it put its video back on the clock.
func playLoop(t *testing.T, audio []BufferedAudioFrame) (*pcmRecorder, int) {
	t.Helper()
	rec := &pcmRecorder{}
	cfg := PlayerConfig{
		Clip: pictures(25), AudioClip: audio, Speed: 1, Loop: true,
		OutputFPSNum: 50, OutputFPSDen: 1, Timed: true, PCMCodec: testPCMCodec,
	}
	rec.wire(&cfg)
	p := NewPlayer(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	defer func() { p.Stop(); p.Wait() }()
	require.Eventually(t, func() bool {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		return len(rec.firstPTS) > loopPasses
	}, 10*time.Second, 10*time.Millisecond)
	p.Stop()
	p.Wait()
	return rec, p.timelineMoves
}

// checkLoopPasses checks that every pass of a loop rec recorded is the
// sound from its first sample, on the first picture of the pass.
func checkLoopPasses(t *testing.T, rec *pcmRecorder, ref []float32) {
	t.Helper()
	got := rec.samples(t, 2)
	rec.mu.Lock()
	firsts := append([]int64(nil), rec.firstPTS...)
	base := rec.chunks[0].pts
	rec.mu.Unlock()
	const pass = 48000 // 25 pictures at 25 fps
	require.GreaterOrEqual(t, len(got)/2, loopPasses*pass)
	for k := 0; k < loopPasses; k++ {
		assert.Equal(t, firsts[k], base+int64(k*pass)*90000/48000, "pass %d starts on its first picture", k)
		seg := got[2*k*pass : 2*(k+1)*pass]
		assert.Zero(t, aacfixture.Lag(seg, ref, 2, 0, 1500, 15000), "pass %d", k)
		assert.Less(t, aacfixture.RMS(seg, ref, 2, 0, 0), 0.02, "pass %d is not the sound", k)
	}
}

// A player that stalls, as on a slow decode, keeps the mixer supplied:
// its audio runs ahead of the clock by pcmLead, and putting the video back
// on the clock after the stall leaves the audio where it is, so the audio
// runs on without a step and the read point never reaches its end. The
// audio was handed over an AAC frame past the next picture and moved with
// the video, so a stall of more than two periods drained the mixer and
// stepped the audio by the stall.
func TestPlayerPCMRunsOnThroughAStall(t *testing.T) {
	audio, _ := fixtureAudio(t, aacfixture.Clip{Ext: ".ts", Channels: 2, Seconds: 2})
	for _, stall := range []time.Duration{60 * time.Millisecond, 100 * time.Millisecond} {
		t.Run(stall.String(), func(t *testing.T) {
			var rec pcmRecorder
			var mu sync.Mutex
			var handed int64 // PTS the audio handed over reaches
			drained := int64(-1)
			cfg := PlayerConfig{
				Clip: pictures(50), AudioClip: audio, Speed: 1,
				OutputFPSNum: 50, OutputFPSDen: 1, Timed: true, PCMCodec: testPCMCodec,
			}
			rec.wire(&cfg)
			video, pcm := cfg.RawVideoOutput, cfg.PCMOutput
			stalled := false
			cfg.RawVideoOutput = func(yuv []byte, w, h int, pts int64, kf bool) {
				video(yuv, w, h, pts, kf)
				mu.Lock()
				first := yuv[0] == 20 && !stalled
				stalled = stalled || first
				mu.Unlock()
				if first {
					time.Sleep(stall)
					mu.Lock()
					// What the read point, two periods behind the clock,
					// still has ahead of it.
					drained = handed - (clock.NowPTS() - clock.ReaderDelayGrains*1800)
					mu.Unlock()
				}
			}
			cfg.PCMOutput = func(p []float32, pts int64, channels int, start bool) {
				pcm(p, pts, channels, start)
				mu.Lock()
				handed = pts + int64(len(p)/channels)*90000/48000
				mu.Unlock()
			}
			p := NewPlayer(cfg)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			p.Start(ctx)
			defer func() { p.Stop(); p.Wait() }()
			require.Eventually(t, func() bool { return p.State() == StateHolding }, 20*time.Second, 10*time.Millisecond)

			rec.samples(t, 2) // contiguous PTS, one start
			mu.Lock()
			defer mu.Unlock()
			require.True(t, stalled)
			assert.Greater(t, drained, int64(0), "the read point ran past the audio during the stall")
		})
	}
}
