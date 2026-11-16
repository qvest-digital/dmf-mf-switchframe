package audio

import (
	"fmt"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/metrics"
)

// pushLeads returns the count and sum of the push leads reported for key.
func pushLeads(t *testing.T, pm *metrics.Metrics, key string) (uint64, float64) {
	t.Helper()
	var out dto.Metric
	h := pm.AudioSourcePushLead.WithLabelValues(metrics.SourceLabel(key)).(prometheus.Histogram)
	require.NoError(t, h.Write(&out))
	return out.GetHistogram().GetSampleCount(), out.GetHistogram().GetSampleSum()
}

// A live source's push reports how far the end of its audio is ahead of
// the end of the program's last read, less the source's delay, so a soak
// shows how close a source's audio came to running dry before a break,
// at every program rate. A source that is not live, such as a clip, and
// a push before the first read report nothing, and a removed source's
// series goes with it.
func TestIngestPCMReportsALiveSourcesLead(t *testing.T) {
	for _, r := range programRates {
		t.Run(fmt.Sprintf("%d_%d", r.Num, r.Den), func(t *testing.T) {
			sr := clock.Rate{Num: 48000, Den: 1}
			pm := metrics.NewMetrics(prometheus.NewRegistry())
			m := NewMixer(MixerConfig{SampleRate: 48000, Channels: 2})
			t.Cleanup(func() { _ = m.Close() })
			m.SetMetrics(pm)
			const live, delayed, clip = "srt:cam", "mxl:cam", "clip:a"
			for _, key := range []string{live, delayed, clip} {
				m.AddChannel(key)
			}
			m.SetLiveInput(live)
			m.SetLiveInput(delayed)
			m.SetSourceDelay(delayed, 100)
			const n = 480
			push := func(key string, end uint64) {
				m.IngestPCM(key, make([]float32, 2*n), clock.IndexPTS(sr, end-n), 2)
			}

			start := clock.PTSIndex(sr, clock.NowPTS())
			push(live, start)
			count, _ := pushLeads(t, pm, live)
			require.Zero(t, count, "a push before the first read reported a lead")

			period := clock.TimestampToIndex(sr, clock.IndexToTimestamp(r, 1))
			m.ProduceSamples(start, int(period), 0)
			readEnd := start + period
			lead := 2*period + 7 // samples
			for _, key := range []string{live, delayed, clip} {
				push(key, readEnd+lead)
			}

			// A 90 kHz stamp is up to half a tick off the sample.
			const tick = 1.0 / 180000
			want := float64(lead) / 48000
			count, sum := pushLeads(t, pm, live)
			require.Equal(t, uint64(1), count)
			require.InDelta(t, want, sum, tick)
			count, sum = pushLeads(t, pm, delayed)
			require.Equal(t, uint64(1), count)
			require.InDelta(t, want+0.1, sum, tick, "the source's delay is not counted against its lead")
			count, _ = pushLeads(t, pm, clip)
			require.Zero(t, count, "a source that is not live reported a lead")

			m.RemoveChannel(live)
			require.False(t, pm.AudioSourcePushLead.DeleteLabelValues(metrics.SourceLabel(live)),
				"a removed source's series stayed")
		})
	}
}
