package srt

import (
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	descConnected = prometheus.NewDesc("switchframe_srt_connected",
		"1 while the SRT connection is up. connection is the input's source key, or output:<output id> for an output.",
		[]string{"connection", "direction"}, nil)
	descRTT = prometheus.NewDesc("switchframe_srt_rtt_seconds",
		"Smoothed round-trip time.", []string{"connection", "direction"}, nil)
	descReconnects = prometheus.NewDesc("switchframe_srt_reconnects_total",
		"Times the connection was lost.", []string{"connection", "direction"}, nil)
	descLatency = prometheus.NewDesc("switchframe_srt_latency_seconds",
		"Latency negotiated with the peer.", []string{"connection", "direction"}, nil)

	descRecvPackets = prometheus.NewDesc("switchframe_srt_received_packets_total",
		"Packets received.", []string{"connection"}, nil)
	descRecvLost = prometheus.NewDesc("switchframe_srt_received_lost_packets_total",
		"Packets the receiver detected as lost.", []string{"connection"}, nil)
	descRecvDropped = prometheus.NewDesc("switchframe_srt_received_dropped_packets_total",
		"Packets dropped for arriving too late to play: what the input actually lost.", []string{"connection"}, nil)
	descRecvRetrans = prometheus.NewDesc("switchframe_srt_received_retransmitted_packets_total",
		"Retransmitted packets received.", []string{"connection"}, nil)
	descRecvBuffer = prometheus.NewDesc("switchframe_srt_receive_buffer_seconds",
		"Media held in the receive buffer.", []string{"connection"}, nil)
	descRecvRate = prometheus.NewDesc("switchframe_srt_receive_rate_bytes",
		"Receive rate, bytes per second, as SRT measured it over its last window.", []string{"connection"}, nil)

	descSendPackets = prometheus.NewDesc("switchframe_srt_sent_packets_total",
		"Packets sent.", []string{"connection"}, nil)
	descSendLost = prometheus.NewDesc("switchframe_srt_sent_lost_packets_total",
		"Sent packets the receiver reported lost.", []string{"connection"}, nil)
	descSendDropped = prometheus.NewDesc("switchframe_srt_sent_dropped_packets_total",
		"Packets the sender dropped as too late to send: what the receiver never got.", []string{"connection"}, nil)
	descSendBuffer = prometheus.NewDesc("switchframe_srt_send_buffer_seconds",
		"Media waiting in the send buffer.", []string{"connection"}, nil)
	descSendRate = prometheus.NewDesc("switchframe_srt_send_rate_bytes",
		"Send rate, bytes per second, as SRT measured it over its last window.", []string{"connection"}, nil)
)

// MetricsCollector exposes every tracked SRT connection's statistics, input
// and output, as Prometheus metrics read at scrape time.
type MetricsCollector struct {
	sm *StatsManager
}

// NewMetricsCollector returns a collector over sm, to register on the
// registry /metrics serves.
func NewMetricsCollector(sm *StatsManager) *MetricsCollector {
	return &MetricsCollector{sm: sm}
}

// Describe implements prometheus.Collector.
func (c *MetricsCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{
		descConnected, descRTT, descReconnects, descLatency,
		descRecvPackets, descRecvLost, descRecvDropped, descRecvRetrans, descRecvBuffer, descRecvRate,
		descSendPackets, descSendLost, descSendDropped, descSendBuffer, descSendRate,
	} {
		ch <- d
	}
}

// connectionLabel names a connection by what it belongs to: an input's
// source key, or output:<id> for an output, without the adapter kind the
// stats key also carries.
func connectionLabel(key string) string {
	if rest, ok := strings.CutPrefix(key, "output:dest:"); ok {
		id, _, _ := strings.Cut(rest, ":")
		return "output:" + id
	}
	if strings.HasPrefix(key, "output:") {
		return "output:srt"
	}
	return key
}

// Collect implements prometheus.Collector.
func (c *MetricsCollector) Collect(ch chan<- prometheus.Metric) {
	c.sm.mu.RLock()
	snaps := make(map[string]ConnStatsSnapshot, len(c.sm.stats))
	for key, cs := range c.sm.stats {
		snaps[connectionLabel(key)] = cs.Snapshot()
	}
	c.sm.mu.RUnlock()

	gauge := func(d *prometheus.Desc, v float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v, labels...)
	}
	counter := func(d *prometheus.Desc, v int64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.CounterValue, float64(v), labels...)
	}
	for conn, s := range snaps {
		dir := string(s.Direction)
		connected := 0.0
		if s.Connected {
			connected = 1
		}
		gauge(descConnected, connected, conn, dir)
		gauge(descRTT, s.RTTMs/1e3, conn, dir)
		counter(descReconnects, int64(s.ReconnectCount), conn, dir)
		if s.NegotiatedLatencyMs > 0 {
			gauge(descLatency, float64(s.NegotiatedLatencyMs)/1e3, conn, dir)
		}
		if s.Direction == DirectionSend {
			counter(descSendPackets, s.PacketsSent, conn)
			counter(descSendLost, s.PacketsSendLost, conn)
			counter(descSendDropped, s.PacketsSendDrop, conn)
			gauge(descSendBuffer, s.SendBufMs/1e3, conn)
			gauge(descSendRate, s.SendRateMbps*1e6/8, conn)
			continue
		}
		counter(descRecvPackets, s.PacketsReceived, conn)
		counter(descRecvLost, s.PacketsLost, conn)
		counter(descRecvDropped, s.PacketsDropped, conn)
		counter(descRecvRetrans, s.PacketsRetransmitted, conn)
		gauge(descRecvBuffer, s.RecvBufMs/1e3, conn)
		gauge(descRecvRate, s.RecvRateMbps*1e6/8, conn)
	}
}
