package switcher

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"

	"github.com/zsiec/switchframe/server/clock"
)

// heldMixer is a sync mixer that holds the arrival-stamped sources in hold
// by their value in 90 kHz ticks.
type heldMixer struct {
	hold map[string]int64
}

func (m *heldMixer) ProduceSamples(uint64, int, int64) []float32 { return nil }
func (m *heldMixer) AdvanceCrossfade()                           {}
func (m *heldMixer) ArrivalHold(key string) int64                { return m.hold[key] }

// The mixer places the audio of a source without a media clock a hold of
// whole program periods after its arrival where its audio would otherwise
// run dry. The source's video is stamped at its arrival as well, and goes
// into the frame store the same hold later, so the two stay together to
// the tick at every program rate. A source whose producer stamps media
// time is not held.
func TestArrivalStampedVideoTakesItsAudiosHold(t *testing.T) {
	now := clock.TAINow()
	clock.SetTAISource(func() int64 { return now })
	t.Cleanup(func() { clock.SetTAISource(nil) })
	for _, r := range []clock.Rate{
		{Num: 24000, Den: 1001}, {Num: 24, Den: 1}, {Num: 25, Den: 1}, {Num: 30000, Den: 1001},
		{Num: 30, Den: 1}, {Num: 50, Den: 1}, {Num: 60000, Den: 1001}, {Num: 60, Den: 1},
		{Num: 100, Den: 1}, {Num: 120000, Den: 1001}, {Num: 120, Den: 1},
		{Num: 240000, Den: 1001}, {Num: 240, Den: 1},
	} {
		t.Run(fmt.Sprintf("%d/%d", r.Num, r.Den), func(t *testing.T) {
			sw := newTestSwitcherDirect(distribution.NewRelay())
			defer sw.closeDirect()
			period := (90000*r.Den + r.Num - 1) / r.Num
			m := &heldMixer{hold: map[string]int64{"cam1": 2 * period, "mxl:cam2": 3 * period}}
			sw.SetSyncMixer(m)
			sw.RegisterSource("cam1", distribution.NewRelay())
			sw.RegisterMXLSource("mxl:cam2")

			sw.IngestRawVideo("cam1", &ProcessingFrame{PTS: 1})
			sw.IngestRawVideo("mxl:cam2", &ProcessingFrame{PTS: clock.NowPTS() - 900})

			ss := sw.frameSync.source("cam1")
			ss.mu.Lock()
			got := ss.ring[len(ss.ring)-1].PTS
			ss.mu.Unlock()
			require.Equal(t, clock.NowPTS()+2*period, got, "the relay frame is placed its audio's hold after its arrival")

			ss = sw.frameSync.source("mxl:cam2")
			ss.mu.Lock()
			got = ss.ring[len(ss.ring)-1].PTS
			ss.mu.Unlock()
			require.Equal(t, clock.NowPTS()-900, got, "a media-timed frame keeps its PTS")
		})
	}
}
