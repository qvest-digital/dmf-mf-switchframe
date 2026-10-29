package srt

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
)

func gatherSRT(t *testing.T, sm *StatsManager) map[string]map[string]float64 {
	t.Helper()
	reg := prometheus.NewPedanticRegistry()
	require.NoError(t, reg.Register(NewMetricsCollector(sm)))
	families, err := reg.Gather()
	require.NoError(t, err)
	out := map[string]map[string]float64{}
	for _, f := range families {
		series := map[string]float64{}
		for _, m := range f.GetMetric() {
			key := ""
			for i, l := range m.GetLabel() {
				if i > 0 {
					key += "/"
				}
				key += l.GetValue()
			}
			if m.Counter != nil {
				series[key] = m.GetCounter().GetValue()
			} else {
				series[key] = m.GetGauge().GetValue()
			}
		}
		out[f.GetName()] = series
	}
	return out
}

// An input is named by its source key and an output by its id, and each
// reports only its own direction's counters.
func TestMetricsCollector_Connections(t *testing.T) {
	sm := NewStatsManager()
	in := sm.Create("srt:cam1", ModeCaller, "cam1", 120)
	in.SetConnected("198.51.100.1:9000", 200)
	in.Update(StatsUpdate{RTTMs: 12, PacketsReceived: 1000, PacketsLost: 7, PacketsDropped: 2, RecvBufMs: 150})
	out := sm.Create("output:dest:41a7ba81:srt-caller", "send", "srt-caller:srt-caller", 120)
	out.UpdateSend(SendStatsUpdate{RTTMs: 30, PacketsSent: 500, PacketsSendDrop: 3, SendRateMbps: 8})
	sm.Create("output:srt-listener", "send", "srt-listener:srt-listener", 120)

	f := gatherSRT(t, sm)
	require.Equal(t, map[string]float64{
		"srt:cam1/recv": 1, "output:41a7ba81/send": 0, "output:srt/send": 0,
	}, f["switchframe_srt_connected"])
	require.Equal(t, map[string]float64{"srt:cam1": 2}, f["switchframe_srt_received_dropped_packets_total"])
	require.Equal(t, map[string]float64{"srt:cam1": 7}, f["switchframe_srt_received_lost_packets_total"])
	require.InDelta(t, 0.15, f["switchframe_srt_receive_buffer_seconds"]["srt:cam1"], 1e-9)
	require.InDelta(t, 0.2, f["switchframe_srt_latency_seconds"]["srt:cam1/recv"], 1e-9)
	require.Equal(t, float64(3), f["switchframe_srt_sent_dropped_packets_total"]["output:41a7ba81"])
	require.Equal(t, float64(1e6), f["switchframe_srt_send_rate_bytes"]["output:41a7ba81"])
	require.NotContains(t, f["switchframe_srt_received_packets_total"], "output:41a7ba81")
	require.NotContains(t, f["switchframe_srt_sent_packets_total"], "srt:cam1")

	for name, series := range f {
		for labels := range series {
			require.NotContains(t, labels, "198.51.100.1", "%s exports the peer address", name)
		}
	}
}

func TestMetricsCollector_Lint(t *testing.T) {
	sm := NewStatsManager()
	sm.Create("srt:cam1", ModeCaller, "cam1", 120)
	sm.Create("output:dest:1:srt-caller", "send", "x", 120)
	problems, err := testutil.CollectAndLint(NewMetricsCollector(sm))
	require.NoError(t, err)
	require.Empty(t, problems)
}
