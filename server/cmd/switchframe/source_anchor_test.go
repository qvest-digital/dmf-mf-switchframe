package main

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/clock"
)

var programRates = []clock.Rate{
	{Num: 24000, Den: 1001}, {Num: 24, Den: 1}, {Num: 25, Den: 1},
	{Num: 30000, Den: 1001}, {Num: 30, Den: 1}, {Num: 50, Den: 1},
	{Num: 60000, Den: 1001}, {Num: 60, Den: 1}, {Num: 100, Den: 1},
	{Num: 120, Den: 1}, {Num: 240, Den: 1},
}

// A playout channel's first picture and its first audio sample carry one
// PTS. A tick shows the newest picture stamped at or before its read
// point and plays the audio stamped from its read point on, so the two
// air together only when that PTS is a read point: a run placed at the
// clock between two frame starts had its picture shown on the next one
// and its audio played from the stamp, ahead of the picture by the
// distance, up to a period. At any program rate, sample rate and phase
// of the start within a period, the tick that shows the first picture
// plays the first sample on its read point.
func TestPlayoutRunStartsOnTheProgramGrid(t *testing.T) {
	var tai atomic.Int64
	scriptTAI(t, &tai)
	for _, r := range programRates {
		for _, sr := range []int64{44100, 48000, 96000} {
			t.Run(fmt.Sprintf("%d_%d/%d", r.Num, r.Den, sr), func(t *testing.T) {
				for _, phase := range []int64{0, 1, 3, 5, 7} { // eighths of a period
					base := clock.TimestampToIndex(r, clock.TAINow()) + 10
					start := clock.IndexToTimestamp(r, base)
					period := clock.IndexToTimestamp(r, base+1) - start
					tai.Store(start + phase*period/8)

					var a gridAnchor
					a.rate = func() clock.Rate { return r }
					const src = 123_456
					pts := a.PTS(src)
					require.Equal(t, pts, a.PTS(src), "the anchor moved")

					// The tick showing the picture: the first read point at
					// or after its PTS.
					k := clock.PTSIndex(r, pts)
					for clock.IndexPTS(r, k) < pts {
						k++
					}
					read := clock.TimestampToIndex(clock.Rate{Num: sr, Den: 1}, clock.IndexToTimestamp(r, k))

					m := audio.NewMixer(audio.MixerConfig{SampleRate: int(sr), Channels: 2})
					m.AddChannel(pcmTestKey)
					m.SetActive(pcmTestKey, true)
					const n = 4096
					pcm := make([]float32, 2*n)
					for i := range n {
						pcm[2*i], pcm[2*i+1] = float32(i+1)/n, float32(i+1)/n
					}
					m.IngestPCM(pcmTestKey, pcm, pts, 2)
					m.PlaceChannel(pcmTestKey)
					out := m.ProduceSamples(read, 64, 0)
					_ = m.Close()
					got := int(out[0]*n+0.5) - 1
					require.LessOrEqualf(t, got*got, 1,
						"phase %d/8: the tick showing the first picture plays sample %d first", phase, got)
				}
			})
		}
	}
}

const pcmTestKey = "playout:grid"

// A change of the program rate puts the playout channels' frames on
// another grid. A run placed on the old one keeps its offset unless it is
// placed again: its next picture would sit between two frame starts of the
// new rate, and its audio ahead of the picture. Every rate change from
// every rate places the next timestamp on the new grid.
func TestPlayoutAnchorMovesOntoTheGridOfANewRate(t *testing.T) {
	var tai atomic.Int64
	scriptTAI(t, &tai)
	for _, from := range programRates {
		for _, to := range programRates {
			if from == to {
				continue
			}
			t.Run(fmt.Sprintf("%d_%d_to_%d_%d", from.Num, from.Den, to.Num, to.Den), func(t *testing.T) {
				rate := from
				a := gridAnchor{rate: func() clock.Rate { return rate }}
				base := clock.TimestampToIndex(from, clock.TAINow()) + 10
				tai.Store(clock.IndexToTimestamp(from, base) + 1_000_000)
				const src = 900_000
				a.PTS(src)

				rate = to
				// One old period later, in source and in clock time, plus a
				// third of a new period off the new grid.
				step := int64(90000) * from.Den / from.Num
				tai.Store(clock.TimestampOf(clock.PTS(tai.Load())+step) + clock.IndexToTimestamp(to, 1)/3)
				pts := a.PTS(src + step)
				require.Equal(t, clock.IndexPTS(to, clock.PTSIndex(to, pts)), pts,
					"the timestamp after the rate change is on the new grid")
			})
		}
	}
}
