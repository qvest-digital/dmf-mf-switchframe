package graphics

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/internal/metricstest"
)

// Every layer reports whether it is on program, with no name or template:
// both are free text from the client.
func TestMetricsCollector_Layers(t *testing.T) {
	c := NewCompositor()
	defer c.Close()
	id1, err := c.AddLayer()
	require.NoError(t, err)
	id2, err := c.AddLayer()
	require.NoError(t, err)
	require.NoError(t, c.SetOverlay(id1, make([]byte, 4*4*4), 4, 4, "secret-template"))
	require.NoError(t, c.On(id1))

	f := metricstest.Series(t, NewMetricsCollector(c))
	require.Equal(t, map[string]float64{itoa(id1): 1, itoa(id2): 0}, f["switchframe_graphics_layer_on_air"])
	require.Equal(t, map[string]float64{"": 1}, f["switchframe_graphics_layers_on_air"])
}

func itoa(i int) string { return strconv.Itoa(i) }
