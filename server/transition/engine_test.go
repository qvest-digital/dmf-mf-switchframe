package transition

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newTestEngine(t *testing.T) (*TransitionEngine, *sync.Mutex, *[][]byte, *[]bool) {
	t.Helper()
	var mu sync.Mutex
	var outputs [][]byte
	var completions []bool // true=completed, false=aborted

	e := NewTransitionEngine(EngineConfig{
		DecoderFactory: func() (VideoDecoder, error) {
			return &mockDecoder{width: 4, height: 4}, nil
		},
		EncoderFactory: func(w, h, bitrate int, fps float32) (VideoEncoder, error) {
			return &mockEncoder{}, nil
		},
		Output: func(data []byte, isKeyframe bool) {
			mu.Lock()
			outputs = append(outputs, data)
			mu.Unlock()
		},
		OnComplete: func(aborted bool) {
			mu.Lock()
			completions = append(completions, !aborted)
			mu.Unlock()
		},
	})
	return e, &mu, &outputs, &completions
}

func TestEngineStartStop(t *testing.T) {
	e, _, _, _ := newTestEngine(t)

	require.Equal(t, StateIdle, e.State())

	err := e.Start("cam1", "cam2", TransitionMix, 1000)
	require.NoError(t, err)
	require.Equal(t, StateActive, e.State())

	e.Stop()
	require.Equal(t, StateIdle, e.State())
}

func TestEngineCannotStartWhileActive(t *testing.T) {
	e, _, _, _ := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 1000))
	err := e.Start("cam1", "cam3", TransitionMix, 1000)
	require.Error(t, err)
	require.Contains(t, err.Error(), "already active")

	e.Stop()
}

func TestEngineAutoPositionAdvances(t *testing.T) {
	e, _, _, _ := newTestEngine(t)

	err := e.Start("cam1", "cam2", TransitionMix, 200) // 200ms
	require.NoError(t, err)

	pos := e.Position()
	require.Less(t, pos, 0.1)

	time.Sleep(110 * time.Millisecond)
	pos = e.Position()
	require.InDelta(t, 0.5, pos, 0.2)

	e.Stop()
}

func TestEngineManualPosition(t *testing.T) {
	e, _, _, _ := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 1000))

	e.SetPosition(0.3)
	require.InDelta(t, 0.3, e.Position(), 0.01)

	e.SetPosition(0.7)
	require.InDelta(t, 0.7, e.Position(), 0.01)

	e.Stop()
}

func TestEngineManualPositionCompletes(t *testing.T) {
	e, mu, _, completions := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 1000))

	e.SetPosition(1.0)

	time.Sleep(10 * time.Millisecond)

	mu.Lock()
	require.Equal(t, 1, len(*completions))
	require.True(t, (*completions)[0], "should be completed, not aborted")
	mu.Unlock()

	require.Equal(t, StateIdle, e.State())
}

func TestEngineManualPositionAborts(t *testing.T) {
	e, mu, _, completions := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 1000))
	e.SetPosition(0.5)

	e.SetPosition(0.0)

	time.Sleep(10 * time.Millisecond)

	mu.Lock()
	require.Equal(t, 1, len(*completions))
	require.False(t, (*completions)[0], "should be aborted, not completed")
	mu.Unlock()

	require.Equal(t, StateIdle, e.State())
}

func TestEngineStopCleansUp(t *testing.T) {
	e, _, _, _ := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 1000))
	e.Stop()

	require.Equal(t, StateIdle, e.State())
	require.InDelta(t, 0.0, e.Position(), 0.01)
}

func TestEngineFTBStart(t *testing.T) {
	e, _, _, _ := newTestEngine(t)

	err := e.Start("cam1", "", TransitionFTB, 1000)
	require.NoError(t, err)
	require.Equal(t, StateActive, e.State())

	e.Stop()
}

func TestEngineTransitionType(t *testing.T) {
	e, _, _, _ := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "cam2", TransitionDip, 1000))
	require.Equal(t, TransitionDip, e.TransitionType())

	e.Stop()
}
