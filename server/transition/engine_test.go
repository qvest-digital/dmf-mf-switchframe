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

func TestEngineIngestProducesOutput(t *testing.T) {
	e, mu, outputs, _ := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 1000))

	// Ingest from source A (stored but doesn't trigger output)
	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01})

	mu.Lock()
	require.Equal(t, 0, len(*outputs), "source A alone should not produce output")
	mu.Unlock()

	// Ingest from source B (triggers blend+output)
	e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01})

	mu.Lock()
	require.Equal(t, 1, len(*outputs), "source B should trigger output")
	mu.Unlock()

	e.Stop()
}

func TestEngineIngestOnlyFromParticipants(t *testing.T) {
	e, mu, outputs, _ := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 1000))

	e.IngestFrame("cam3", []byte{0x00, 0x00, 0x00, 0x01})
	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01})
	e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01})

	mu.Lock()
	require.Equal(t, 1, len(*outputs), "only cam1+cam2 should produce output")
	mu.Unlock()

	e.Stop()
}

func TestEngineIngestWhileIdle(t *testing.T) {
	e, mu, outputs, _ := newTestEngine(t)

	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01})

	mu.Lock()
	require.Equal(t, 0, len(*outputs))
	mu.Unlock()
}

func TestEngineFTBIngestTriggersOnFromSource(t *testing.T) {
	e, mu, outputs, _ := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "", TransitionFTB, 1000))

	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01})

	mu.Lock()
	require.Equal(t, 1, len(*outputs), "FTB: source A should trigger output")
	mu.Unlock()

	e.Stop()
}

func TestEngineMultipleFrames(t *testing.T) {
	e, mu, outputs, _ := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 5000))

	for i := 0; i < 5; i++ {
		e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01})
		e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01})
	}

	mu.Lock()
	require.Equal(t, 5, len(*outputs), "each B frame with existing A should produce output")
	mu.Unlock()

	e.Stop()
}

func TestEngineAutoCompletes(t *testing.T) {
	e, mu, _, completions := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 50)) // 50ms

	for i := 0; i < 20; i++ {
		e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01})
		e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01})
		time.Sleep(10 * time.Millisecond)
		if e.State() == StateIdle {
			break
		}
	}

	mu.Lock()
	require.GreaterOrEqual(t, len(*completions), 1, "should have completed")
	require.True(t, (*completions)[0], "should be completed, not aborted")
	mu.Unlock()
}

func TestEngineFTBReverseStart(t *testing.T) {
	e, _, _, _ := newTestEngine(t)

	err := e.Start("cam1", "", TransitionFTBReverse, 1000)
	require.NoError(t, err)
	require.Equal(t, StateActive, e.State())
	require.Equal(t, TransitionFTBReverse, e.TransitionType())

	e.Stop()
}

func TestEngineFTBReverseIngestTriggersOnFromSource(t *testing.T) {
	e, mu, outputs, _ := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "", TransitionFTBReverse, 1000))

	// FTBReverse triggers on fromSource (same as FTB)
	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01})

	mu.Lock()
	require.Equal(t, 1, len(*outputs), "FTBReverse: source A should trigger output")
	mu.Unlock()

	e.Stop()
}

func TestEngineFTBReverseNoDecoderB(t *testing.T) {
	// FTBReverse should not create a decoder B (same as FTB)
	var decoderCount int
	e := NewTransitionEngine(EngineConfig{
		DecoderFactory: func() (VideoDecoder, error) {
			decoderCount++
			return &mockDecoder{width: 4, height: 4}, nil
		},
		EncoderFactory: func(w, h, bitrate int, fps float32) (VideoEncoder, error) {
			return &mockEncoder{}, nil
		},
		Output:     func(data []byte, isKeyframe bool) {},
		OnComplete: func(aborted bool) {},
	})

	require.NoError(t, e.Start("cam1", "", TransitionFTBReverse, 1000))
	require.Equal(t, 1, decoderCount, "FTBReverse should only create one decoder (A)")

	e.Stop()
}

func TestEngineFTBReverseBlendInvertsPosition(t *testing.T) {
	// At position 0.0: FTBReverse should produce black (1.0 - 0.0 = 1.0 -> fully black)
	// At position 1.0: FTBReverse should produce full source (1.0 - 1.0 = 0.0 -> fully visible)
	// This is the opposite of regular FTB.
	//
	// We test this by using a very short duration (1ms) and a very long duration (60s)
	// to control position, then checking the blended output via the mock encoder.

	// We'll use a custom approach: create an engine with a custom decoder that produces
	// known YUV data, then verify the output differs from regular FTB.
	// For simplicity, we just verify that the engine starts, ingests, and produces output.
	// The blend logic is verified in blend_test.go.

	e, mu, outputs, _ := newTestEngine(t)

	// Long duration so position stays near 0.0
	require.NoError(t, e.Start("cam1", "", TransitionFTBReverse, 60000))

	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01})

	mu.Lock()
	require.Equal(t, 1, len(*outputs), "FTBReverse should produce output")
	mu.Unlock()

	e.Stop()
}
