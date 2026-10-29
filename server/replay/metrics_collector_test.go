package replay

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/internal/metricstest"
	"github.com/zsiec/switchframe/server/metrics"
)

// The player state is one-hot, and each source's buffer is reported under
// its source label, an SRT input's by digest.
func TestMetricsCollector_Replay(t *testing.T) {
	m := NewManager(&mockRelay{}, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()
	require.NoError(t, m.AddSource("mxl:cam1"))
	require.NoError(t, m.AddSource("srt:secret-stream-key"))

	f := metricstest.Series(t, NewMetricsCollector(m))
	require.Equal(t, map[string]float64{"idle": 1, "loading": 0, "playing": 0, "paused": 0}, f["switchframe_replay_state"])
	require.Equal(t, map[string]float64{"mxl:cam1": 0, metrics.SourceLabel("srt:secret-stream-key"): 0},
		f["switchframe_replay_buffer_seconds"])
}
