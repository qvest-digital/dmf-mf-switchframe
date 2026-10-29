package htmlgfx

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/internal/metricstest"
)

// The counter is what tells a running engine that the sidecar paints and its
// frames reach a layer, rather than only that the sidecar connected: frames
// for a slot with no layer are not counted.
func TestMetricsCollector_CountsFramesThatReachALayer(t *testing.T) {
	mgr, mock, comp := setupManagerWithMock(t)
	defer mock.close()
	defer func() { _ = mgr.Close() }()

	_, err := mgr.AllocateBrowser(3)
	require.NoError(t, err)

	frame := func(id uint8) Frame {
		return Frame{BrowserID: id, Width: 2, Height: 2, Pixels: make([]byte, 2*2*4)}
	}
	require.NoError(t, mock.sendFrame(frame(0)))
	require.NoError(t, mock.sendFrame(frame(1))) // slot 1 has no layer
	require.NoError(t, mock.sendFrame(frame(0)))

	require.Eventually(t, func() bool { return mgr.FramesDelivered() == 2 }, 2*time.Second, 5*time.Millisecond)
	require.Len(t, comp.getOverlays(), 2)

	f := metricstest.Series(t, NewMetricsCollector(mgr))
	require.Equal(t, map[string]float64{"": 2}, f["switchframe_htmlgfx_frames_delivered_total"])
}

// A frame the compositor refuses never reached program, so counting it would
// report a sidecar whose output is lost as one that paints.
func TestMetricsCollector_SkipsFramesTheCompositorRefuses(t *testing.T) {
	mgr, mock, comp := setupManagerWithMock(t)
	defer mock.close()
	defer func() { _ = mgr.Close() }()

	_, err := mgr.AllocateBrowser(3)
	require.NoError(t, err)
	frame := Frame{BrowserID: 0, Width: 2, Height: 2, Pixels: make([]byte, 2*2*4)}

	comp.mu.Lock()
	comp.err = errors.New("layer gone")
	comp.mu.Unlock()
	require.NoError(t, mock.sendFrame(frame))
	require.NoError(t, mock.sendFrame(frame))
	require.Eventually(t, func() bool { return len(comp.getOverlays()) == 2 }, 2*time.Second, 5*time.Millisecond)

	comp.mu.Lock()
	comp.err = nil
	comp.mu.Unlock()
	require.NoError(t, mock.sendFrame(frame))

	// Frames are handled in order, so once the third is offered every
	// refused frame has already been counted or skipped.
	require.Eventually(t, func() bool {
		return len(comp.getOverlays()) == 3 && mgr.FramesDelivered() >= 1
	}, 2*time.Second, 5*time.Millisecond)
	require.Equal(t, uint64(1), mgr.FramesDelivered())
}
