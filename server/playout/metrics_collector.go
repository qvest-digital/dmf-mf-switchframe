package playout

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
)

// channelStates are the values switchframe_playout_channel_state is
// one-hot over.
var channelStates = []ChannelState{StateStopped, StateCued, StateOnAir, StateCompleted}

var (
	descChannelState = prometheus.NewDesc("switchframe_playout_channel_state",
		"1 for the playout channel's state, 0 for the others.", []string{"channel", "state"}, nil)
	descItemIndex = prometheus.NewDesc("switchframe_playout_item_index",
		"Position of the item on air in its playlist, from 0. Absent while the playlist is empty.", []string{"channel"}, nil)
	descItemCount = prometheus.NewDesc("switchframe_playout_items",
		"Items in the channel's playlist.", []string{"channel"}, nil)
	descItemRemaining = prometheus.NewDesc("switchframe_playout_item_remaining_seconds",
		"Time left of the item on air.", []string{"channel"}, nil)
	descPodsArmed = prometheus.NewDesc("switchframe_playout_pods_armed",
		"Ad pods armed on the channel.", []string{"channel"}, nil)
	descPodsCompleted = prometheus.NewDesc("switchframe_playout_pods_completed",
		"Ad pods on the channel's board that completed: rejoined or dumped out. Deleting a pod from the board lowers it.", []string{"channel"}, nil)
	descStalled = prometheus.NewDesc("switchframe_playout_channel_stalled",
		"1 while the watchdog finds a running channel delivering no frames.", []string{"channel"}, nil)
	descSinceFrame = prometheus.NewDesc("switchframe_playout_last_frame_age_seconds",
		"Time since a watched channel delivered its last frame.", []string{"channel"}, nil)

	descCacheUsed = prometheus.NewDesc("switchframe_playout_cache_used_bytes",
		"Bytes the playout asset cache holds.", nil, nil)
	descCacheMax = prometheus.NewDesc("switchframe_playout_cache_max_bytes",
		"Bytes the playout asset cache may hold.", nil, nil)
	descCacheItems = prometheus.NewDesc("switchframe_playout_cache_items",
		"Assets in the playout cache, by state: ready, pending (queued for a fetch), fetching (or transcoding) and error.", []string{"state"}, nil)
)

// MetricsCollector exposes the playout channels, their watchdog and the
// asset cache as Prometheus metrics read at scrape time. The cache may be
// nil.
type MetricsCollector struct {
	m     *Manager
	cache *Cache
}

// NewMetricsCollector returns a collector over m and cache, to register on
// the registry /metrics serves.
func NewMetricsCollector(m *Manager, cache *Cache) *MetricsCollector {
	return &MetricsCollector{m: m, cache: cache}
}

// Describe implements prometheus.Collector.
func (c *MetricsCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{
		descChannelState, descItemIndex, descItemCount, descItemRemaining, descPodsArmed,
		descPodsCompleted, descStalled, descSinceFrame, descCacheUsed, descCacheMax, descCacheItems,
	} {
		ch <- d
	}
}

// Collect implements prometheus.Collector.
func (c *MetricsCollector) Collect(ch chan<- prometheus.Metric) {
	gauge := func(d *prometheus.Desc, v float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v, labels...)
	}
	for _, st := range c.m.ChannelStates() {
		id := strconv.Itoa(st.ID)
		for _, s := range channelStates {
			v := 0.0
			if s.String() == st.State {
				v = 1
			}
			gauge(descChannelState, v, id, s.String())
		}
		if st.ItemCount > 0 {
			gauge(descItemIndex, float64(st.ItemIndex), id)
		}
		gauge(descItemCount, float64(st.ItemCount), id)
		if st.State == StateOnAir.String() {
			gauge(descItemRemaining, float64(st.ItemRemainMs)/1e3, id)
		}
		if st.Pods != nil {
			gauge(descPodsArmed, float64(st.Pods.PodsArmed), id)
			gauge(descPodsCompleted, float64(st.Pods.PodsCompleted), id)
		}
	}
	for _, h := range c.m.HealthSummary() {
		id := strconv.Itoa(h.ChannelID)
		stalled := 0.0
		if h.Stalled {
			stalled = 1
		}
		gauge(descStalled, stalled, id)
		gauge(descSinceFrame, float64(h.LastFrameMs)/1e3, id)
	}
	if c.cache != nil {
		cs := c.cache.Status()
		gauge(descCacheUsed, float64(cs.UsedBytes))
		gauge(descCacheMax, float64(cs.MaxBytes))
		gauge(descCacheItems, float64(cs.ItemCount-cs.PendingCount-cs.FetchingCount-cs.ErrorCount), "ready")
		gauge(descCacheItems, float64(cs.PendingCount), "pending")
		gauge(descCacheItems, float64(cs.FetchingCount), "fetching")
		gauge(descCacheItems, float64(cs.ErrorCount), "error")
	}
}
