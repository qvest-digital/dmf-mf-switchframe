//go:build cgo && !noffmpeg

package output

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
)

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
