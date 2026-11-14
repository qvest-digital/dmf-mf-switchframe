package clip

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/clock"
)

// programRates are the output rates the clip timing is checked at;
// 120000/1001 and 240000/1001 catch a period rounded to whole ticks.
var programRates = []clock.Rate{
	{Num: 24000, Den: 1001}, {Num: 24, Den: 1}, {Num: 25, Den: 1},
	{Num: 30000, Den: 1001}, {Num: 30, Den: 1}, {Num: 50, Den: 1},
	{Num: 60000, Den: 1001}, {Num: 60, Den: 1}, {Num: 100, Den: 1},
	{Num: 120000, Den: 1001}, {Num: 120, Den: 1},
	{Num: 240000, Den: 1001}, {Num: 240, Den: 1},
}

// sampleRates are the AAC rates the clip timing is checked at; 1024
// samples at 44.1 kHz are 2089.8 ticks, not a whole number.
var sampleRates = []int{44100, 48000, 96000}

// rateAudio returns n AAC frames at sampleRate, each carrying its index.
func rateAudio(n, sampleRate int) []BufferedAudioFrame {
	audio := make([]BufferedAudioFrame, n)
	for i := range audio {
		audio[i] = BufferedAudioFrame{
			data:       []byte{byte(i >> 8), byte(i)},
			pts:        int64(i) * 1024 * 90000 / int64(sampleRate),
			sampleRate: sampleRate,
			channels:   2,
		}
	}
	return audio
}

type ptsRec struct {
	idx int
	pts int64
}

// After a seek far into a looping clip, the audio is placed on the video
// at the picture's share of it, and its pass runs on from where the
// clip's first AAC frame would have been: every frame sits where the
// stream puts it from there, and the next pass starts within half an AAC
// frame of where the video's does. A pass start summed from frame
// durations cut to whole ticks lies 0.8 ticks a frame late at 44.1 kHz,
// more than an AAC frame by the seek point here, and moves the loop seam
// with it.
func TestTimedAudioPassAfterSeek(t *testing.T) {
	const n = 4000 // AAC frames in the clip
	for _, sr := range sampleRates {
		for _, r := range programRates {
			t.Run(fmt.Sprintf("%dHz/%d_%d", sr, r.Num, r.Den), func(t *testing.T) {
				// The video's pass is an AAC frame shorter than the audio's.
				v := int((n - 1) * 1024 * r.Num / (int64(sr) * r.Den))
				var out []ptsRec
				p := NewPlayer(PlayerConfig{
					AudioClip: rateAudio(n, sr),
					Loop:      true,
					Timed:     true,
					AudioOutput: func(data []byte, pts int64, _, _ int) {
						out = append(out, ptsRec{int(data[0])<<8 | int(data[1]), pts})
					},
				})
				p.rate = r
				p.totalOutputFrames = v
				p.audioOut = true
				p.audioResync = true

				pic := v * 9 / 10
				idx := pic * n / v
				until0 := int64(900000)
				wrap := 0
				for j := 0; j <= v && wrap == 0; j++ {
					p.emitTimedAudio(1, pic+j, v, until0+int64(j)*90000*r.Den/r.Num)
					for k := 1; k < len(out) && wrap == 0; k++ {
						if out[k].idx == 0 {
							wrap = k
						}
					}
				}
				require.Equal(t, idx, out[0].idx, "the audio restarts at the picture's share")
				require.Equal(t, until0, out[0].pts, "the audio restarts on the video")
				require.NotZero(t, wrap, "the audio wraps to its next pass")

				start := until0 - int64(idx)*1024*90000/int64(sr)
				frame := int64(1024 * 90000 / sr)
				end := start + int64(v)*90000*r.Den/r.Num
				require.InDelta(t, end, out[wrap].pts, float64(frame/2+2),
					"the next pass starts at the video's, %d ticks after the seek", end-until0)
				for _, f := range out[:wrap] {
					require.Equal(t, start+int64(f.idx)*1024*90000/int64(sr), f.pts,
						"AAC frame %d lies where the pass started at %d puts it", f.idx, start)
				}
			})
		}
	}
}

// Untimed, the audio PTS advance by the exact duration of each AAC frame,
// so the audio of a long clip at 44.1 kHz does not fall 0.8 ticks a frame
// behind its video.
func TestUntimedAudioPTSAtSampleRate(t *testing.T) {
	const n = 10000
	for _, sr := range sampleRates {
		t.Run(fmt.Sprintf("%dHz", sr), func(t *testing.T) {
			var out []ptsRec
			p := NewPlayer(PlayerConfig{
				AudioClip: rateAudio(n, sr),
				AudioOutput: func(data []byte, pts int64, _, _ int) {
					out = append(out, ptsRec{int(data[0])<<8 | int(data[1]), pts})
				},
			})
			p.totalOutputFrames = n
			for i := 1; i <= n; i++ {
				p.emitAudioForVideoFrame(i, n)
			}
			require.Len(t, out, n)
			last := int64(n-1) * 1024 * 90000 / int64(sr)
			require.Equal(t, last, out[n-1].pts, "the last AAC frame, %d s into the clip", last/90000)
			for k, f := range out {
				require.Equal(t, int64(k)*1024*90000/int64(sr), f.pts, "AAC frame %d", k)
			}
		})
	}
}
