package output

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

func gatherOutput(t *testing.T, c prometheus.Collector) map[string]map[string]float64 {
	t.Helper()
	reg := prometheus.NewPedanticRegistry()
	require.NoError(t, reg.Register(c))
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
			series[key] = seriesValue(m)
		}
		out[f.GetName()] = series
	}
	return out
}

func seriesValue(m *dto.Metric) float64 {
	if m.Counter != nil {
		return m.GetCounter().GetValue()
	}
	return m.GetGauge().GetValue()
}

// A destination reports its kind and name, never its address or URL, and
// a one-hot state, so a stopped output does not leave an active series.
func TestMetricsCollector_Destinations(t *testing.T) {
	m := NewManager()
	defer func() { _ = m.Close() }()
	id, err := m.AddDestination(DestinationConfig{
		Type: "rtmp", URL: "rtmp://ingest.example.com/live/secret-key", Name: "stream",
	})
	require.NoError(t, err)

	f := gatherOutput(t, NewMetricsCollector(m))
	require.Equal(t, map[string]float64{"rtmp/stream/" + id: 1}, f["switchframe_output_info"])
	require.Equal(t, map[string]float64{
		id + "/starting": 0, id + "/active": 0, id + "/reconnecting": 0, id + "/stopped": 1, id + "/error": 0,
	}, f["switchframe_output_state"])
	require.Equal(t, map[string]float64{id: 0}, f["switchframe_output_bytes_total"])
	require.NotContains(t, f, "switchframe_output_clients", "an RTMP output has no clients")
	require.Equal(t, map[string]float64{id: 0}, f["switchframe_output_media_seconds_total"])
	require.Equal(t, map[string]float64{"video/" + id: 0, "audio/" + id: 0}, f["switchframe_output_dropped_frames_total"])

	for name, series := range f {
		for labels := range series {
			require.NotContains(t, labels, "secret-key", "%s exports the stream key", name)
			require.NotContains(t, labels, "ingest.example.com", "%s exports the address", name)
		}
	}
}

// statusFrameAdapter is a FrameAdapter that only reports a status.
type statusFrameAdapter struct{ st AdapterStatus }

func (a statusFrameAdapter) ID() string                         { return "status" }
func (a statusFrameAdapter) WriteVideo(*media.VideoFrame) error { return nil }
func (a statusFrameAdapter) WriteAudio(*media.AudioFrame) error { return nil }
func (a statusFrameAdapter) Start(context.Context) error        { return nil }
func (a statusFrameAdapter) Close() error                       { return nil }
func (a statusFrameAdapter) Status() AdapterStatus              { return a.st }

// An RTMP output's media time and dropped frames come from its adapter,
// video and audio drops apart.
func TestMetricsCollector_RTMPMediaAndDrops(t *testing.T) {
	m := NewManager()
	defer func() { _ = m.Close() }()
	id, err := m.AddDestination(DestinationConfig{Type: "rtmp", URL: "rtmp://example.com/live/key", Name: "stream"})
	require.NoError(t, err)
	m.mu.Lock()
	d := m.destinations[id]
	d.mu.Lock()
	d.frameAdapter = statusFrameAdapter{st: AdapterStatus{
		State: StateActive, MediaTicks: 3 * 90000, VideoDropped: 7, AudioDropped: 2,
	}}
	d.mu.Unlock()
	m.mu.Unlock()

	f := gatherOutput(t, NewMetricsCollector(m))
	require.Equal(t, map[string]float64{id: 3}, f["switchframe_output_media_seconds_total"])
	require.Equal(t, map[string]float64{"video/" + id: 7, "audio/" + id: 2}, f["switchframe_output_dropped_frames_total"])
}

// A program timeline restart the RTMP connection carries over is counted
// per output, so a restart is visible where the media time it shifts is.
func TestMetricsCollector_RTMPTimelineRebases(t *testing.T) {
	m := NewManager()
	defer func() { _ = m.Close() }()
	id, err := m.AddDestination(DestinationConfig{Type: "rtmp", URL: "rtmp://example.com/live/key", Name: "stream"})
	require.NoError(t, err)

	r, err := NewRTMPDirect(RTMPCallerConfig{URL: "file:" + filepath.Join(t.TempDir(), "out.flv")})
	require.NoError(t, err)
	require.NoError(t, r.Start(context.Background()))
	const base = int64(900000)
	require.NoError(t, r.WriteVideo(&media.VideoFrame{PTS: base, DTS: base, IsKeyframe: true,
		SPS:      []byte{0x67, 0x42, 0xc0, 0x1e},
		PPS:      []byte{0x68, 0xce, 0x38, 0x80},
		WireData: []byte{0x00, 0x00, 0x00, 0x04, 0x65, 0x88, 0x84, 0x00}}))
	require.Eventually(t, r.initialized.Load, 2*time.Second, 5*time.Millisecond)
	for _, pts := range []int64{base + 1800, base + 3600, base + 900, base + 2700, base + 1800} {
		require.NoError(t, r.WriteVideo(&media.VideoFrame{PTS: pts, DTS: pts,
			WireData: []byte{0x00, 0x00, 0x00, 0x04, 0x41, 0x9a, 0x02, 0x00}}))
		time.Sleep(time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	require.NoError(t, r.Close())

	m.mu.Lock()
	d := m.destinations[id]
	d.mu.Lock()
	d.frameAdapter = statusFrameAdapter{st: r.Status()}
	d.mu.Unlock()
	m.mu.Unlock()

	f := gatherOutput(t, NewMetricsCollector(m))
	require.Equal(t, map[string]float64{id: 2}, f["switchframe_output_timeline_rebases_total"])
}

// The recorder's series follow it: active and growing while it runs, its
// size and duration gone once it stops.
func TestMetricsCollector_Recording(t *testing.T) {
	m := NewManager()
	defer func() { _ = m.Close() }()
	c := NewMetricsCollector(m)

	f := gatherOutput(t, c)
	require.Equal(t, map[string]float64{"": 0}, f["switchframe_recording_active"])
	require.Equal(t, map[string]float64{"": 0}, f["switchframe_recording_failed"])
	require.NotContains(t, f, "switchframe_recording_current_bytes")

	require.NoError(t, m.StartRecording(RecorderConfig{Dir: t.TempDir()}))
	m.DirectWriteVideo(makeKeyframe(90000))
	require.Eventually(t, func() bool {
		return gatherOutput(t, c)["switchframe_recording_current_bytes"][""] > 0
	}, 2*time.Second, 20*time.Millisecond)
	require.Equal(t, map[string]float64{"": 1}, gatherOutput(t, c)["switchframe_recording_active"])

	require.NoError(t, m.StopRecording())
	f = gatherOutput(t, c)
	require.Equal(t, map[string]float64{"": 0}, f["switchframe_recording_active"])
	require.NotContains(t, f, "switchframe_recording_current_bytes")
}

func TestMetricsCollector_Lint(t *testing.T) {
	m := NewManager()
	defer func() { _ = m.Close() }()
	_, err := m.AddDestination(DestinationConfig{Type: "srt-listener", Port: 9000, Name: "contrib"})
	require.NoError(t, err)
	require.NoError(t, m.StartRecording(RecorderConfig{Dir: t.TempDir()}))
	problems, err := testutil.CollectAndLint(NewMetricsCollector(m))
	require.NoError(t, err)
	require.Empty(t, problems)
}
