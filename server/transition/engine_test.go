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
		Output: func(data []byte, isKeyframe bool, pts int64) {
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
	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0)

	mu.Lock()
	require.Equal(t, 0, len(*outputs), "source A alone should not produce output")
	mu.Unlock()

	// Ingest from source B (triggers blend+output)
	e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 0)

	mu.Lock()
	require.Equal(t, 1, len(*outputs), "source B should trigger output")
	mu.Unlock()

	e.Stop()
}

func TestEngineIngestOnlyFromParticipants(t *testing.T) {
	e, mu, outputs, _ := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 1000))

	e.IngestFrame("cam3", []byte{0x00, 0x00, 0x00, 0x01}, 0)
	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0)
	e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 0)

	mu.Lock()
	require.Equal(t, 1, len(*outputs), "only cam1+cam2 should produce output")
	mu.Unlock()

	e.Stop()
}

func TestEngineIngestWhileIdle(t *testing.T) {
	e, mu, outputs, _ := newTestEngine(t)

	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0)

	mu.Lock()
	require.Equal(t, 0, len(*outputs))
	mu.Unlock()
}

func TestEngineFTBIngestTriggersOnFromSource(t *testing.T) {
	e, mu, outputs, _ := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "", TransitionFTB, 1000))

	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0)

	mu.Lock()
	require.Equal(t, 1, len(*outputs), "FTB: source A should trigger output")
	mu.Unlock()

	e.Stop()
}

func TestEngineMultipleFrames(t *testing.T) {
	e, mu, outputs, _ := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 5000))

	for i := 0; i < 5; i++ {
		e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0)
		e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 0)
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
		e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0)
		e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 0)
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
	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0)

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
		Output:     func(data []byte, isKeyframe bool, pts int64) {},
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

	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0)

	mu.Lock()
	require.Equal(t, 1, len(*outputs), "FTBReverse should produce output")
	mu.Unlock()

	e.Stop()
}

func TestEngineWarmupPopulatesState(t *testing.T) {
	e, mu, outputs, _ := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 5000))

	// Warmup both sources — no output produced
	e.WarmupDecode("cam1", []byte{0x00, 0x00, 0x00, 0x01})
	e.WarmupDecode("cam2", []byte{0x00, 0x00, 0x00, 0x01})

	mu.Lock()
	require.Equal(t, 0, len(*outputs), "warmup should produce no output")
	mu.Unlock()

	// First live IngestFrame from toSource should produce output immediately
	// because latestYUVA is already populated from warmup.
	e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 0)

	mu.Lock()
	require.Equal(t, 1, len(*outputs), "first live ingest after warmup should produce output")
	mu.Unlock()

	e.Stop()
}

func TestEngineWarmupNoOutput(t *testing.T) {
	e, mu, outputs, _ := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 5000))

	// Feed many warmup frames — none should produce output
	for i := 0; i < 24; i++ {
		e.WarmupDecode("cam1", []byte{0x00, 0x00, 0x00, 0x01})
		e.WarmupDecode("cam2", []byte{0x00, 0x00, 0x00, 0x01})
	}

	mu.Lock()
	require.Equal(t, 0, len(*outputs), "warmup should never produce output")
	mu.Unlock()

	e.Stop()
}

func TestEngineWarmupWhileIdle(t *testing.T) {
	e, mu, outputs, _ := newTestEngine(t)

	// Engine is idle — warmup should be a no-op
	e.WarmupDecode("cam1", []byte{0x00, 0x00, 0x00, 0x01})

	mu.Lock()
	require.Equal(t, 0, len(*outputs))
	mu.Unlock()
}

func TestEngineWarmupConcurrentWithIngest(t *testing.T) {
	e, _, _, _ := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 5000))

	var wg sync.WaitGroup
	wg.Add(2)

	// Concurrent warmup
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			e.WarmupDecode("cam1", []byte{0x00, 0x00, 0x00, 0x01})
			e.WarmupDecode("cam2", []byte{0x00, 0x00, 0x00, 0x01})
		}
	}()

	// Concurrent ingest
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0)
			e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 0)
		}
	}()

	wg.Wait()
	e.Stop()
}

func TestEngineResolutionMismatchScales(t *testing.T) {
	// Simulate a common church setup: cam1 at 1920x1080, cam2 at 1280x720.
	// Previously the engine dropped mismatched frames. Now it should scale
	// the 720p frame to 1080p and produce blended output.
	var mu sync.Mutex
	var outputs [][]byte

	// Track which decoder is which by call order: first = cam1 (1080p), second = cam2 (720p)
	decoderCount := 0
	e := NewTransitionEngine(EngineConfig{
		DecoderFactory: func() (VideoDecoder, error) {
			decoderCount++
			if decoderCount == 1 {
				// Decoder A (cam1): 8x8 — will set the target resolution
				return &mockDecoder{width: 8, height: 8}, nil
			}
			// Decoder B (cam2): 4x4 — different resolution, must be scaled
			return &mockDecoder{width: 4, height: 4}, nil
		},
		EncoderFactory: func(w, h, bitrate int, fps float32) (VideoEncoder, error) {
			return &mockEncoder{}, nil
		},
		Output: func(data []byte, isKeyframe bool, pts int64) {
			mu.Lock()
			outputs = append(outputs, data)
			mu.Unlock()
		},
		OnComplete: func(aborted bool) {},
	})

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 5000))

	// Ingest from cam1 (8x8) — sets target resolution
	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0)

	mu.Lock()
	require.Equal(t, 0, len(outputs), "source A alone should not produce output")
	mu.Unlock()

	// Ingest from cam2 (4x4) — should be scaled to 8x8 and trigger blend+output
	e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 0)

	mu.Lock()
	require.Equal(t, 1, len(outputs), "mismatched resolution should produce output after scaling")
	mu.Unlock()

	// Subsequent frames should also work
	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 33000)
	e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 33000)

	mu.Lock()
	require.Equal(t, 2, len(outputs), "multiple frames with scaling should keep producing output")
	mu.Unlock()

	e.Stop()
}

func TestEngineResolutionMismatchScalesFromSource(t *testing.T) {
	// Test the reverse case: the "from" source (cam1) has a different
	// resolution than the "to" source (cam2) which sets the target.
	var mu sync.Mutex
	var outputs [][]byte

	decoderCount := 0
	e := NewTransitionEngine(EngineConfig{
		DecoderFactory: func() (VideoDecoder, error) {
			decoderCount++
			if decoderCount == 1 {
				// Decoder A (cam1): 4x4 — different from target
				return &mockDecoder{width: 4, height: 4}, nil
			}
			// Decoder B (cam2): 8x8 — will set the target resolution (decoded first here)
			return &mockDecoder{width: 8, height: 8}, nil
		},
		EncoderFactory: func(w, h, bitrate int, fps float32) (VideoEncoder, error) {
			return &mockEncoder{}, nil
		},
		Output: func(data []byte, isKeyframe bool, pts int64) {
			mu.Lock()
			outputs = append(outputs, data)
			mu.Unlock()
		},
		OnComplete: func(aborted bool) {},
	})

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 5000))

	// Ingest cam2 first — sets target to 8x8
	e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 0)

	mu.Lock()
	require.Equal(t, 0, len(outputs), "cam2 alone (no cam1 yet) should not produce output")
	mu.Unlock()

	// Ingest cam1 (4x4) — should be scaled to 8x8
	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0)

	// Now ingest cam2 again — should trigger blend (cam1 is available)
	e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 33000)

	mu.Lock()
	require.Equal(t, 1, len(outputs), "should produce output after scaling from-source")
	mu.Unlock()

	e.Stop()
}

func TestEngineUsesConfigBitrateFPS(t *testing.T) {
	// Verify the encoder factory receives the config's Bitrate and FPS
	// instead of hardcoded 4Mbps/30fps.
	var capturedBitrate int
	var capturedFPS float32

	e := NewTransitionEngine(EngineConfig{
		DecoderFactory: func() (VideoDecoder, error) {
			return &mockDecoder{width: 4, height: 4}, nil
		},
		EncoderFactory: func(w, h, bitrate int, fps float32) (VideoEncoder, error) {
			capturedBitrate = bitrate
			capturedFPS = fps
			return &mockEncoder{}, nil
		},
		Output:     func(data []byte, isKeyframe bool, pts int64) {},
		OnComplete: func(aborted bool) {},
		Bitrate:    8_000_000,
		FPS:        60.0,
	})

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 5000))

	// First ingest triggers lazy encoder init
	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0)

	require.Equal(t, 8_000_000, capturedBitrate, "encoder should receive config bitrate")
	require.InDelta(t, 60.0, capturedFPS, 0.01, "encoder should receive config fps")

	e.Stop()
}

func TestEngineWipeStart(t *testing.T) {
	e, _, _, _ := newTestEngine(t)

	e.config.WipeDirection = WipeHLeft
	err := e.Start("cam1", "cam2", TransitionWipe, 1000)
	require.NoError(t, err)
	require.Equal(t, StateActive, e.State())
	require.Equal(t, TransitionWipe, e.TransitionType())

	e.Stop()
}

func TestEngineWipeIngestProducesOutput(t *testing.T) {
	e, mu, outputs, _ := newTestEngine(t)

	e.config.WipeDirection = WipeVTop
	require.NoError(t, e.Start("cam1", "cam2", TransitionWipe, 5000))

	// Ingest from source A (stored but doesn't trigger output)
	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0)

	mu.Lock()
	require.Equal(t, 0, len(*outputs), "source A alone should not produce output")
	mu.Unlock()

	// Ingest from source B (triggers wipe blend+output)
	e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 0)

	mu.Lock()
	require.Equal(t, 1, len(*outputs), "source B should trigger wipe output")
	mu.Unlock()

	e.Stop()
}

func TestPositionClampedAboveOne(t *testing.T) {
	e, _, _, _ := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 60000))

	e.SetPosition(0.5) // move past zero first
	e.SetPosition(1.5) // should be clamped — triggers completion since >= 0.999

	// After completion the engine returns to idle, so Position() returns 0.
	// The key assertion is that the engine didn't panic and state is idle
	// (completion was triggered, not a hang).
	require.Equal(t, StateIdle, e.State(), "pos 1.5 should trigger completion")
}

func TestPositionClampedBelowZero(t *testing.T) {
	e, _, _, _ := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 60000))

	// SetPosition(-0.5) without moving past zero first — should not abort,
	// just clamp to 0.0 and stay active.
	e.SetPosition(-0.5)

	require.Equal(t, StateActive, e.State(), "negative pos without prior movement should stay active")
	require.InDelta(t, 0.0, e.Position(), 0.01, "position should be clamped to 0")
}

func TestCompletionThreshold(t *testing.T) {
	e, mu, _, completions := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 60000))

	e.SetPosition(0.5) // move past zero
	e.SetPosition(0.999)

	time.Sleep(10 * time.Millisecond)

	mu.Lock()
	require.Equal(t, 1, len(*completions), "0.999 should trigger completion")
	require.True(t, (*completions)[0], "should be completed, not aborted")
	mu.Unlock()

	require.Equal(t, StateIdle, e.State())
}

func TestCompletionThresholdBelowThreshold(t *testing.T) {
	e, mu, _, completions := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 60000))

	e.SetPosition(0.5) // move past zero
	e.SetPosition(0.99)

	time.Sleep(10 * time.Millisecond)

	mu.Lock()
	require.Equal(t, 0, len(*completions), "0.99 should NOT trigger completion")
	mu.Unlock()

	require.Equal(t, StateActive, e.State())
	require.InDelta(t, 0.99, e.Position(), 0.01)

	e.Stop()
}

func TestEngineDefaultBitrateFPSWhenZero(t *testing.T) {
	// Verify that zero Bitrate/FPS falls back to defaults.
	var capturedBitrate int
	var capturedFPS float32

	e := NewTransitionEngine(EngineConfig{
		DecoderFactory: func() (VideoDecoder, error) {
			return &mockDecoder{width: 4, height: 4}, nil
		},
		EncoderFactory: func(w, h, bitrate int, fps float32) (VideoEncoder, error) {
			capturedBitrate = bitrate
			capturedFPS = fps
			return &mockEncoder{}, nil
		},
		Output:     func(data []byte, isKeyframe bool, pts int64) {},
		OnComplete: func(aborted bool) {},
		// Bitrate and FPS intentionally left at zero
	})

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 5000))
	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0)

	require.Equal(t, DefaultBitrate, capturedBitrate, "should fall back to default bitrate")
	require.InDelta(t, DefaultFPS, capturedFPS, 0.01, "should fall back to default fps")

	e.Stop()
}
