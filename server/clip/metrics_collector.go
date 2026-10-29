package clip

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
)

// playerStates are the values switchframe_clip_player_state is one-hot over.
var playerStates = []PlayerState{StateEmpty, StateLoaded, StatePlaying, StatePaused, StateHolding}

var (
	descPlayerState = prometheus.NewDesc("switchframe_clip_player_state",
		"1 for the clip player's state, 0 for the others.", []string{"player", "state"}, nil)
	descPlayerPosition = prometheus.NewDesc("switchframe_clip_player_position_ratio",
		"How far a loaded clip has played, 0 to 1.", []string{"player"}, nil)
	descPlayerSpeed = prometheus.NewDesc("switchframe_clip_player_speed",
		"Playback speed of a playing clip; 1 is real time.", []string{"player"}, nil)
)

// MetricsCollector exposes the clip players as Prometheus metrics read at
// scrape time. Clip names are left out: they are operator text.
type MetricsCollector struct {
	m *Manager
}

// NewMetricsCollector returns a collector over m, to register on the
// registry /metrics serves.
func NewMetricsCollector(m *Manager) *MetricsCollector {
	return &MetricsCollector{m: m}
}

// Describe implements prometheus.Collector.
func (c *MetricsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- descPlayerState
	ch <- descPlayerPosition
	ch <- descPlayerSpeed
}

// Collect implements prometheus.Collector.
func (c *MetricsCollector) Collect(ch chan<- prometheus.Metric) {
	gauge := func(d *prometheus.Desc, v float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v, labels...)
	}
	for _, p := range c.m.PlayerStates() {
		id := strconv.Itoa(p.ID)
		for _, s := range playerStates {
			v := 0.0
			if s == p.State {
				v = 1
			}
			gauge(descPlayerState, v, id, string(s))
		}
		if p.State != StateEmpty {
			gauge(descPlayerPosition, p.Position, id)
		}
		if p.State == StatePlaying {
			gauge(descPlayerSpeed, p.Speed, id)
		}
	}
}
