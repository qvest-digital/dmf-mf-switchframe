package clip

import (
	"log/slog"
	"strconv"
	"time"

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
	c.m.starts.starts.Describe(ch)
	c.m.starts.delay.Describe(ch)
}

// Collect implements prometheus.Collector.
func (c *MetricsCollector) Collect(ch chan<- prometheus.Metric) {
	gauge := func(d *prometheus.Desc, v float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v, labels...)
	}
	c.m.starts.starts.Collect(ch)
	c.m.starts.delay.Collect(ch)
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

// startDelayBuckets span a tick at the fastest pipeline rate to the time
// a high-bitrate clip takes to open a decoder and decode its first
// picture.
var startDelayBuckets = []float64{0.001, 0.005, 0.01, 0.02, 0.04, 0.1, 0.25, 0.5, 1, 2.5, 5}

// startStats counts how clip players started and how long their first
// frame and audio took.
type startStats struct {
	starts *prometheus.CounterVec
	delay  *prometheus.HistogramVec
}

func newStartStats() *startStats {
	return &startStats{
		starts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "switchframe_clip_player_starts_total",
			Help: "Clip player starts, by whether the first picture was cued: ready (decoded before the start), waited (still being decoded at the start) or missed (not cued).",
		}, []string{"player", "preroll"}),
		delay: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "switchframe_clip_player_start_delay_seconds",
			Help:    "Time from a clip player's start to its first video frame (media=video) and its first audio (media=audio).",
			Buckets: startDelayBuckets,
		}, []string{"player", "media"}),
	}
}

// noteStart records a player's first video frame or first audio after its
// start, delay after it, and logs a first frame later than one output
// period at fpsNum/fpsDen, or audio later than two.
func (m *Manager) noteStart(playerID int, media string, delay time.Duration, preroll string, fpsNum, fpsDen int) {
	id := strconv.Itoa(playerID)
	if media == "video" {
		m.starts.starts.WithLabelValues(id, preroll).Inc()
	}
	m.starts.delay.WithLabelValues(id, media).Observe(delay.Seconds())
	period := 40 * time.Millisecond
	if fpsNum > 0 && fpsDen > 0 {
		period = time.Second * time.Duration(fpsDen) / time.Duration(fpsNum)
	}
	switch {
	case media == "video" && delay > period:
		slog.Warn("clip: late start", "player", playerID, "preroll", preroll, "first_frame_ms", delay.Milliseconds())
	case media == "video":
		slog.Info("clip: started", "player", playerID, "preroll", preroll, "first_frame_ms", delay.Milliseconds())
	case delay > 2*period:
		slog.Warn("clip: late start audio", "player", playerID, "preroll", preroll, "first_audio_ms", delay.Milliseconds())
	}
}
