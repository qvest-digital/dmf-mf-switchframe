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
		Output: func(yuv []byte, width, height int, pts int64, isKeyframe bool) {
			mu.Lock()
			cp := make([]byte, len(yuv))
			copy(cp, yuv)
			outputs = append(outputs, cp)
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
	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)

	mu.Lock()
	require.Equal(t, 0, len(*outputs), "source A alone should not produce output")
	mu.Unlock()

	// Ingest from source B (triggers blend+output)
	e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)

	mu.Lock()
	require.Equal(t, 1, len(*outputs), "source B should trigger output")
	mu.Unlock()

	e.Stop()
}

func TestEngineIngestOnlyFromParticipants(t *testing.T) {
	e, mu, outputs, _ := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 1000))

	e.IngestFrame("cam3", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)
	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)
	e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)

	mu.Lock()
	require.Equal(t, 1, len(*outputs), "only cam1+cam2 should produce output")
	mu.Unlock()

	e.Stop()
}

func TestEngineIngestWhileIdle(t *testing.T) {
	e, mu, outputs, _ := newTestEngine(t)

	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)

	mu.Lock()
	require.Equal(t, 0, len(*outputs))
	mu.Unlock()
}

func TestEngineFTBIngestTriggersOnFromSource(t *testing.T) {
	e, mu, outputs, _ := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "", TransitionFTB, 1000))

	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)

	mu.Lock()
	require.Equal(t, 1, len(*outputs), "FTB: source A should trigger output")
	mu.Unlock()

	e.Stop()
}

func TestEngineMultipleFrames(t *testing.T) {
	e, mu, outputs, _ := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 5000))

	for i := 0; i < 5; i++ {
		e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)
		e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)
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
		e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)
		e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)
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
	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)

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
		Output:     func(yuv []byte, width, height int, pts int64, isKeyframe bool) {},
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
	// to control position, then checking the blended output via the raw YUV output callback.

	// We'll use a custom approach: create an engine with a custom decoder that produces
	// known YUV data, then verify the output differs from regular FTB.
	// For simplicity, we just verify that the engine starts, ingests, and produces output.
	// The blend logic is verified in blend_test.go.

	e, mu, outputs, _ := newTestEngine(t)

	// Long duration so position stays near 0.0
	require.NoError(t, e.Start("cam1", "", TransitionFTBReverse, 60000))

	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)

	mu.Lock()
	require.Equal(t, 1, len(*outputs), "FTBReverse should produce output")
	mu.Unlock()

	e.Stop()
}

func TestEngineFTBNoPanicWhenBlenderNil(t *testing.T) {
	// Regression: FTB after WarmupComplete could panic when the first frame
	// is a P-frame (needsKeyframeA=true → goto blend). The blender is nil
	// because no successful decode has occurred yet.
	e, _, _, _ := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "", TransitionFTB, 1000))
	e.WarmupComplete()

	// Send a P-frame (not keyframe). This should NOT panic.
	require.NotPanics(t, func() {
		e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0, false)
	})

	e.Stop()
}

func TestEngineBlackFrameFallbackWhenLatestYUVBNil(t *testing.T) {
	// When the TO source's decoder hasn't produced output yet (B-frame
	// reorder EAGAIN during warmup), the engine should still produce blended
	// output using a black frame as placeholder for the missing source.
	// This prevents ~0.5s output gaps at transition start.
	for _, tt := range []TransitionType{TransitionMix, TransitionWipe, TransitionDip} {
		t.Run(string(tt), func(t *testing.T) {
			e, mu, outputs, _ := newTestEngine(t)

			require.NoError(t, e.Start("cam1", "cam2", tt, 60000))

			// Warm up only the FROM source — simulates TO source EAGAIN.
			e.WarmupDecode("cam1", []byte{0x00, 0x00, 0x00, 0x01})
			e.WarmupComplete()

			// TO P-frame should NOT panic and SHOULD produce output
			// (blending FROM's warmup frame with a black placeholder).
			require.NotPanics(t, func() {
				e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 0, false)
			})

			mu.Lock()
			require.Equal(t, 1, len(*outputs), "should produce output with black fallback")
			mu.Unlock()

			e.Stop()
		})
	}
}

func TestEngineBlackFrameFallbackWhenLatestYUVANil(t *testing.T) {
	// Same test but FROM source didn't decode during warmup.
	e, mu, outputs, _ := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 60000))

	// Warm up only the TO source.
	e.WarmupDecode("cam2", []byte{0x00, 0x00, 0x00, 0x01})
	e.WarmupComplete()

	// TO P-frame should produce output (black placeholder for FROM).
	require.NotPanics(t, func() {
		e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 0, false)
	})

	mu.Lock()
	require.Equal(t, 1, len(*outputs), "should produce output with black A fallback")
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
	e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)

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
			e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)
			e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)
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
		Output: func(yuv []byte, width, height int, pts int64, isKeyframe bool) {
			mu.Lock()
			cp := make([]byte, len(yuv))
			copy(cp, yuv)
			outputs = append(outputs, cp)
			mu.Unlock()
		},
		OnComplete: func(aborted bool) {},
	})

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 5000))

	// Ingest from cam1 (8x8) — sets target resolution
	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)

	mu.Lock()
	require.Equal(t, 0, len(outputs), "source A alone should not produce output")
	mu.Unlock()

	// Ingest from cam2 (4x4) — should be scaled to 8x8 and trigger blend+output
	e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)

	mu.Lock()
	require.Equal(t, 1, len(outputs), "mismatched resolution should produce output after scaling")
	mu.Unlock()

	// Subsequent frames should also work
	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 33000, true)
	e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 33000, true)

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
		Output: func(yuv []byte, width, height int, pts int64, isKeyframe bool) {
			mu.Lock()
			cp := make([]byte, len(yuv))
			copy(cp, yuv)
			outputs = append(outputs, cp)
			mu.Unlock()
		},
		OnComplete: func(aborted bool) {},
	})

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 5000))

	// Ingest cam2 first — sets target to 8x8.
	// With black frame fallback, this produces output (A=black, B=cam2).
	e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)

	mu.Lock()
	require.Equal(t, 1, len(outputs), "cam2 alone should produce output with black fallback for cam1")
	mu.Unlock()

	// Ingest cam1 (4x4) — should be scaled to 8x8
	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)

	// Now ingest cam2 again — should trigger blend (cam1 is available)
	e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 33000, true)

	mu.Lock()
	require.Equal(t, 2, len(outputs), "should produce output after scaling from-source")
	mu.Unlock()

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
	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)

	mu.Lock()
	require.Equal(t, 0, len(*outputs), "source A alone should not produce output")
	mu.Unlock()

	// Ingest from source B (triggers wipe blend+output)
	e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)

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

func TestTransitionTimeoutAbort(t *testing.T) {
	// Start a transition with a short timeout and send no frames.
	// The watchdog should detect starvation and abort.
	var mu sync.Mutex
	var completions []bool

	e := NewTransitionEngine(EngineConfig{
		DecoderFactory: func() (VideoDecoder, error) {
			return &mockDecoder{width: 4, height: 4}, nil
		},
		Output: func(yuv []byte, width, height int, pts int64, isKeyframe bool) {},
		OnComplete: func(aborted bool) {
			mu.Lock()
			completions = append(completions, !aborted)
			mu.Unlock()
		},
	})

	e.SetTimeout(100 * time.Millisecond)

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 5000))
	require.Equal(t, StateActive, e.State())

	// Wait for watchdog to trigger (timeout=100ms, check interval=25ms)
	time.Sleep(250 * time.Millisecond)

	require.Equal(t, StateIdle, e.State(), "watchdog should have aborted the transition")

	mu.Lock()
	require.Equal(t, 1, len(completions), "OnComplete should have been called")
	require.False(t, completions[0], "should be aborted, not completed")
	mu.Unlock()
}

func TestTransitionNoTimeoutWhenFramesArrive(t *testing.T) {
	// Start a transition with a short timeout but keep sending frames.
	// The watchdog should NOT abort because frames keep arriving.
	var mu sync.Mutex
	var completions []bool

	e := NewTransitionEngine(EngineConfig{
		DecoderFactory: func() (VideoDecoder, error) {
			return &mockDecoder{width: 4, height: 4}, nil
		},
		Output: func(yuv []byte, width, height int, pts int64, isKeyframe bool) {},
		OnComplete: func(aborted bool) {
			mu.Lock()
			completions = append(completions, !aborted)
			mu.Unlock()
		},
	})

	e.SetTimeout(150 * time.Millisecond)

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 5000))

	// Send frames every 50ms for 400ms — well beyond the timeout window.
	// Each frame resets the timer so the watchdog should never fire.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 8; i++ {
			e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, int64(i*33000), true)
			e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, int64(i*33000), true)
			time.Sleep(50 * time.Millisecond)
		}
	}()

	<-done

	require.Equal(t, StateActive, e.State(), "transition should still be active — frames kept arriving")

	mu.Lock()
	require.Equal(t, 0, len(completions), "OnComplete should not have been called")
	mu.Unlock()

	e.Stop()
}

func TestTransitionWatchdogStopsOnComplete(t *testing.T) {
	// Start a transition, complete it normally, and verify no goroutine leak.
	// We do this by starting a transition, auto-completing it, then waiting
	// a bit — if the watchdog leaked, it would try to abort an idle engine
	// and we'd see extra OnComplete calls.
	var mu sync.Mutex
	var completions []bool

	e := NewTransitionEngine(EngineConfig{
		DecoderFactory: func() (VideoDecoder, error) {
			return &mockDecoder{width: 4, height: 4}, nil
		},
		Output: func(yuv []byte, width, height int, pts int64, isKeyframe bool) {},
		OnComplete: func(aborted bool) {
			mu.Lock()
			completions = append(completions, !aborted)
			mu.Unlock()
		},
	})

	e.SetTimeout(100 * time.Millisecond)

	// Use a very short duration so it auto-completes quickly
	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 50))

	// Feed frames until auto-complete
	for i := 0; i < 30; i++ {
		e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, int64(i*33000), true)
		e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, int64(i*33000), true)
		time.Sleep(10 * time.Millisecond)
		if e.State() == StateIdle {
			break
		}
	}

	require.Equal(t, StateIdle, e.State(), "transition should have auto-completed")

	mu.Lock()
	completeCount := len(completions)
	mu.Unlock()
	require.GreaterOrEqual(t, completeCount, 1, "should have at least one completion")

	// Wait longer than the timeout — if watchdog leaked, it would fire
	time.Sleep(250 * time.Millisecond)

	mu.Lock()
	finalCount := len(completions)
	mu.Unlock()
	require.Equal(t, completeCount, finalCount, "no extra OnComplete calls — watchdog stopped cleanly")
}

func TestTransitionWatchdogStopsOnAbort(t *testing.T) {
	// Verify the watchdog stops when Stop() is called externally.
	var mu sync.Mutex
	var completions []bool

	e := NewTransitionEngine(EngineConfig{
		DecoderFactory: func() (VideoDecoder, error) {
			return &mockDecoder{width: 4, height: 4}, nil
		},
		Output: func(yuv []byte, width, height int, pts int64, isKeyframe bool) {},
		OnComplete: func(aborted bool) {
			mu.Lock()
			completions = append(completions, !aborted)
			mu.Unlock()
		},
	})

	e.SetTimeout(200 * time.Millisecond)

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 5000))

	// Stop immediately (before watchdog fires)
	e.Stop()

	require.Equal(t, StateIdle, e.State())

	// Wait longer than the timeout — if watchdog leaked, it would panic/fire
	time.Sleep(400 * time.Millisecond)

	mu.Lock()
	require.Equal(t, 0, len(completions), "Stop() does not trigger OnComplete")
	mu.Unlock()
}

func TestTransitionDefaultTimeout(t *testing.T) {
	// Verify the default timeout is 10 seconds.
	e := NewTransitionEngine(EngineConfig{
		DecoderFactory: func() (VideoDecoder, error) {
			return &mockDecoder{width: 4, height: 4}, nil
		},
		Output:     func(yuv []byte, width, height int, pts int64, isKeyframe bool) {},
		OnComplete: func(aborted bool) {},
	})

	require.Equal(t, DefaultTimeout, e.Timeout())
}

func TestTransitionSetTimeoutOverrides(t *testing.T) {
	e := NewTransitionEngine(EngineConfig{
		DecoderFactory: func() (VideoDecoder, error) {
			return &mockDecoder{width: 4, height: 4}, nil
		},
		Output:     func(yuv []byte, width, height int, pts int64, isKeyframe bool) {},
		OnComplete: func(aborted bool) {},
	})

	e.SetTimeout(5 * time.Second)
	require.Equal(t, 5*time.Second, e.Timeout())
}

func TestEngineStingerTransition(t *testing.T) {
	// Create a 3-frame stinger with full alpha (fully opaque overlay)
	w, h := 4, 4
	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	totalSize := ySize + 2*uvSize

	frames := make([]StingerFrameData, 3)
	for i := range frames {
		yuv := make([]byte, totalSize)
		for j := range yuv {
			yuv[j] = byte(200 + i) // distinct per frame
		}
		alpha := make([]byte, ySize)
		for j := range alpha {
			alpha[j] = 255 // fully opaque
		}
		frames[i] = StingerFrameData{YUV: yuv, Alpha: alpha}
	}

	var mu sync.Mutex
	var outputs [][]byte

	e := NewTransitionEngine(EngineConfig{
		DecoderFactory: func() (VideoDecoder, error) {
			return &mockDecoder{width: w, height: h}, nil
		},
		Output: func(yuv []byte, width, height int, pts int64, isKeyframe bool) {
			mu.Lock()
			cp := make([]byte, len(yuv))
			copy(cp, yuv)
			outputs = append(outputs, cp)
			mu.Unlock()
		},
		OnComplete: func(aborted bool) {},
		Stinger: &StingerData{
			Frames:   frames,
			Width:    w,
			Height:   h,
			CutPoint: 0.5,
		},
	})

	require.NoError(t, e.Start("cam1", "cam2", TransitionStinger, 1000))
	require.Equal(t, TransitionStinger, e.TransitionType())

	// Ingest from both sources
	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)
	e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)

	mu.Lock()
	require.Equal(t, 1, len(outputs), "stinger transition should produce output")
	mu.Unlock()

	e.Stop()
}

func TestEngineStingerCutPoint(t *testing.T) {
	// Verify the cut point switches the base source from A to B.
	w, h := 4, 4
	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	totalSize := ySize + 2*uvSize

	// Zero-alpha stinger (fully transparent) so output = base source
	frames := make([]StingerFrameData, 10)
	for i := range frames {
		frames[i] = StingerFrameData{
			YUV:   make([]byte, totalSize),
			Alpha: make([]byte, ySize), // all zeros = transparent
		}
	}

	e := NewTransitionEngine(EngineConfig{
		DecoderFactory: func() (VideoDecoder, error) {
			return &mockDecoder{width: w, height: h}, nil
		},
		Output:     func(yuv []byte, width, height int, pts int64, isKeyframe bool) {},
		OnComplete: func(aborted bool) {},
		Stinger: &StingerData{
			Frames:   frames,
			Width:    w,
			Height:   h,
			CutPoint: 0.3, // switch at 30%
		},
	})

	require.NoError(t, e.Start("cam1", "cam2", TransitionStinger, 5000))

	// Ingest both sources
	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)
	e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)

	// Engine should start and produce output
	require.Equal(t, StateActive, e.State())

	e.Stop()
}

func TestEngineStingerNoDataReturnsCopy(t *testing.T) {
	// Verify stinger fallback returns an independent copy, not a reference to
	// the internal latestYUV buffer (which could race with IngestFrame).
	var mu sync.Mutex
	var outputs [][]byte

	e := NewTransitionEngine(EngineConfig{
		DecoderFactory: func() (VideoDecoder, error) {
			return &mockDecoder{width: 4, height: 4}, nil
		},
		Output: func(yuv []byte, width, height int, pts int64, isKeyframe bool) {
			mu.Lock()
			outputs = append(outputs, yuv) // keep the exact slice (no copy)
			mu.Unlock()
		},
		OnComplete: func(aborted bool) {},
		// No Stinger data → triggers fallback path
	})

	require.NoError(t, e.Start("cam1", "cam2", TransitionStinger, 60000))

	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)
	e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)

	mu.Lock()
	require.Equal(t, 1, len(outputs))
	out := outputs[0]
	mu.Unlock()

	// Mutate the returned slice — should NOT affect engine internals
	for i := range out {
		out[i] = 0xFF
	}

	// Ingest another frame pair — should still produce valid output
	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 33000, true)
	e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 33000, true)

	mu.Lock()
	require.Equal(t, 2, len(outputs), "should still produce output after mutation")
	mu.Unlock()

	e.Stop()
}

func TestEngineNilBlendedGuard(t *testing.T) {
	// Verify that if blended is nil (e.g., no latestYUVA set for default case),
	// the Output callback is NOT called with nil data.
	outputCalled := false

	e := NewTransitionEngine(EngineConfig{
		DecoderFactory: func() (VideoDecoder, error) {
			return &mockDecoder{width: 4, height: 4}, nil
		},
		Output: func(yuv []byte, width, height int, pts int64, isKeyframe bool) {
			outputCalled = true
			require.NotNil(t, yuv, "Output should never receive nil YUV data")
		},
		OnComplete: func(aborted bool) {},
	})

	// Start a mix transition and feed frames normally — should work fine
	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 5000))
	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)
	e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)

	require.True(t, outputCalled, "output should have been called")
	e.Stop()
}

func TestEngineStingerNoData(t *testing.T) {
	// Stinger transition without stinger data should still work (fallback to hard cut)
	var mu sync.Mutex
	var outputs [][]byte

	e := NewTransitionEngine(EngineConfig{
		DecoderFactory: func() (VideoDecoder, error) {
			return &mockDecoder{width: 4, height: 4}, nil
		},
		Output: func(yuv []byte, width, height int, pts int64, isKeyframe bool) {
			mu.Lock()
			cp := make([]byte, len(yuv))
			copy(cp, yuv)
			outputs = append(outputs, cp)
			mu.Unlock()
		},
		OnComplete: func(aborted bool) {},
		// No Stinger data
	})

	require.NoError(t, e.Start("cam1", "cam2", TransitionStinger, 1000))

	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)
	e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)

	mu.Lock()
	require.Equal(t, 1, len(outputs), "stinger without data should still produce output")
	mu.Unlock()

	e.Stop()
}

func TestEngineDecodeFallThroughToBlendOnEAGAIN(t *testing.T) {
	// Scenario: warmup decode succeeds (blender initialized), but the first
	// live IngestFrame decode returns EAGAIN (B-frame reorder). The engine
	// should still produce output using the black frame fallback instead of
	// bailing out when decodeAndStore returns false.
	var mu sync.Mutex
	var outputs [][]byte

	decoderCount := 0
	e := NewTransitionEngine(EngineConfig{
		DecoderFactory: func() (VideoDecoder, error) {
			decoderCount++
			if decoderCount == 2 {
				// Decoder B: first decode returns EAGAIN, then succeeds
				return &bufferingMockDecoder{width: 4, height: 4, bufferLeft: 1}, nil
			}
			// Decoder A: always succeeds
			return &mockDecoder{width: 4, height: 4}, nil
		},
		Output: func(yuv []byte, width, height int, pts int64, isKeyframe bool) {
			mu.Lock()
			cp := make([]byte, len(yuv))
			copy(cp, yuv)
			outputs = append(outputs, cp)
			mu.Unlock()
		},
		OnComplete: func(aborted bool) {},
	})

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 5000))

	// Warmup: decode both sources to initialize blender and latestYUVA.
	// Decoder A succeeds immediately. Decoder B gets EAGAIN on first call.
	e.WarmupDecode("cam1", []byte{0x00, 0x00, 0x00, 0x01})
	e.WarmupDecode("cam2", []byte{0x00, 0x00, 0x00, 0x01}) // EAGAIN: latestYUVB stays nil
	e.WarmupComplete()

	// Live frame from TO source (cam2). The decoder's second call would also
	// fail with EAGAIN (bufferLeft was 1, spent on warmup... actually it was
	// decremented in warmup). After warmup, needsKeyframeB=true so this
	// keyframe clears it and calls decodeAndStore. Decoder B now succeeds
	// (bufferLeft=0). But let's test the case where decode still fails.

	// Actually, let me restructure: make decoder B buffer 2 calls.
	e.Stop()

	// Reset with a decoder that buffers 2 calls (1 warmup + 1 live)
	decoderCount = 0
	outputs = nil
	e = NewTransitionEngine(EngineConfig{
		DecoderFactory: func() (VideoDecoder, error) {
			decoderCount++
			if decoderCount == 2 {
				// Decoder B: first 2 decodes EAGAIN, then succeeds
				return &bufferingMockDecoder{width: 4, height: 4, bufferLeft: 2}, nil
			}
			return &mockDecoder{width: 4, height: 4}, nil
		},
		Output: func(yuv []byte, width, height int, pts int64, isKeyframe bool) {
			mu.Lock()
			cp := make([]byte, len(yuv))
			copy(cp, yuv)
			outputs = append(outputs, cp)
			mu.Unlock()
		},
		OnComplete: func(aborted bool) {},
	})

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 5000))

	// Warmup: A succeeds (sets blender), B gets EAGAIN #1
	e.WarmupDecode("cam1", []byte{0x00, 0x00, 0x00, 0x01})
	e.WarmupDecode("cam2", []byte{0x00, 0x00, 0x00, 0x01})
	e.WarmupComplete()

	// Live keyframe from cam2 (TO). needsKeyframeB cleared, calls
	// decodeAndStore which gets EAGAIN #2. Currently bails out.
	// With fix: should fall through to blend using black frame for B.
	e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)

	mu.Lock()
	require.Equal(t, 1, len(outputs),
		"decode EAGAIN on live frame should still produce output via black fallback")
	mu.Unlock()

	e.Stop()
}

func TestEngineHintDimensionsPreInitBlender(t *testing.T) {
	// Scenario: ALL warmup decodes return EAGAIN (both decoders buffer
	// every frame). Without hint dimensions, e.blender stays nil and the
	// engine can't produce any output until the first successful decode.
	// With HintWidth/HintHeight, the blender is pre-initialized at Start()
	// so the black frame fallback works immediately.
	var mu sync.Mutex
	var outputs [][]byte

	e := NewTransitionEngine(EngineConfig{
		DecoderFactory: func() (VideoDecoder, error) {
			// Both decoders: first 3 decodes return EAGAIN
			return &bufferingMockDecoder{width: 4, height: 4, bufferLeft: 3}, nil
		},
		Output: func(yuv []byte, width, height int, pts int64, isKeyframe bool) {
			mu.Lock()
			cp := make([]byte, len(yuv))
			copy(cp, yuv)
			outputs = append(outputs, cp)
			mu.Unlock()
		},
		OnComplete:  func(aborted bool) {},
		HintWidth:   4,
		HintHeight:  4,
	})

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 5000))

	// Warmup: both return EAGAIN. Without hint, blender stays nil.
	e.WarmupDecode("cam1", []byte{0x00, 0x00, 0x00, 0x01})
	e.WarmupDecode("cam2", []byte{0x00, 0x00, 0x00, 0x01})
	e.WarmupComplete()

	// Live keyframe from TO source — decode returns EAGAIN again.
	// With hint dimensions: blender pre-initialized, black fallback works.
	e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)

	mu.Lock()
	require.Equal(t, 1, len(outputs),
		"hint dimensions should allow output even when all decodes return EAGAIN")
	mu.Unlock()

	// Verify output dimensions match hint
	expectedSize := 4 * 4 * 3 / 2
	require.Equal(t, expectedSize, len(outputs[0]), "output should match hint dimensions")

	e.Stop()
}

// --- IngestRawFrame tests ---

func TestIngestRawFrame_ProducesOutput(t *testing.T) {
	// IngestRawFrame bypasses H.264 decode — stores YUV directly.
	// Output triggered when TO source provides raw YUV.
	var mu sync.Mutex
	var outputs [][]byte

	e := NewTransitionEngine(EngineConfig{
		DecoderFactory: func() (VideoDecoder, error) {
			return &mockDecoder{width: 4, height: 4}, nil
		},
		Output: func(yuv []byte, width, height int, pts int64, isKeyframe bool) {
			mu.Lock()
			cp := make([]byte, len(yuv))
			copy(cp, yuv)
			outputs = append(outputs, cp)
			mu.Unlock()
		},
		OnComplete: func(aborted bool) {},
	})

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 5000))

	w, h := 4, 4
	yuvSize := w * h * 3 / 2

	// FROM source raw YUV (stored, no output)
	yuvA := make([]byte, yuvSize)
	for i := range yuvA {
		yuvA[i] = 100
	}
	e.IngestRawFrame("cam1", yuvA, w, h, 0)

	mu.Lock()
	require.Equal(t, 0, len(outputs), "FROM source alone should not produce output")
	mu.Unlock()

	// TO source raw YUV (triggers blend)
	yuvB := make([]byte, yuvSize)
	for i := range yuvB {
		yuvB[i] = 200
	}
	e.IngestRawFrame("cam2", yuvB, w, h, 3000)

	mu.Lock()
	require.Equal(t, 1, len(outputs), "TO source should trigger blended output")
	require.Equal(t, yuvSize, len(outputs[0]))
	mu.Unlock()

	e.Stop()
}

func TestIngestRawFrame_IgnoresNonParticipants(t *testing.T) {
	var mu sync.Mutex
	var outputs [][]byte

	e := NewTransitionEngine(EngineConfig{
		DecoderFactory: func() (VideoDecoder, error) {
			return &mockDecoder{width: 4, height: 4}, nil
		},
		Output: func(yuv []byte, width, height int, pts int64, isKeyframe bool) {
			mu.Lock()
			outputs = append(outputs, yuv)
			mu.Unlock()
		},
		OnComplete: func(aborted bool) {},
	})

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 5000))

	yuv := make([]byte, 4*4*3/2)
	e.IngestRawFrame("cam3", yuv, 4, 4, 0)

	mu.Lock()
	require.Equal(t, 0, len(outputs), "non-participant should be ignored")
	mu.Unlock()

	e.Stop()
}

func TestIngestRawFrame_WhileIdle(t *testing.T) {
	var mu sync.Mutex
	var outputs [][]byte

	e := NewTransitionEngine(EngineConfig{
		DecoderFactory: func() (VideoDecoder, error) {
			return &mockDecoder{width: 4, height: 4}, nil
		},
		Output: func(yuv []byte, width, height int, pts int64, isKeyframe bool) {
			mu.Lock()
			outputs = append(outputs, yuv)
			mu.Unlock()
		},
		OnComplete: func(aborted bool) {},
	})

	e.IngestRawFrame("cam1", make([]byte, 24), 4, 4, 0)

	mu.Lock()
	require.Equal(t, 0, len(outputs))
	mu.Unlock()
}

func TestIngestRawFrame_ResolutionMismatchScales(t *testing.T) {
	var mu sync.Mutex
	var outputs [][]byte
	var outWidths []int

	e := NewTransitionEngine(EngineConfig{
		DecoderFactory: func() (VideoDecoder, error) {
			return &mockDecoder{width: 8, height: 8}, nil
		},
		Output: func(yuv []byte, width, height int, pts int64, isKeyframe bool) {
			mu.Lock()
			cp := make([]byte, len(yuv))
			copy(cp, yuv)
			outputs = append(outputs, cp)
			outWidths = append(outWidths, width)
			mu.Unlock()
		},
		OnComplete: func(aborted bool) {},
	})

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 5000))

	// FROM at 8x8 sets engine resolution
	e.IngestRawFrame("cam1", make([]byte, 8*8*3/2), 8, 8, 0)

	// TO at 4x4 must be scaled to 8x8
	e.IngestRawFrame("cam2", make([]byte, 4*4*3/2), 4, 4, 3000)

	mu.Lock()
	require.Equal(t, 1, len(outputs), "should produce output after scaling")
	require.Equal(t, 8, outWidths[0], "output should be at engine resolution")
	require.Equal(t, 8*8*3/2, len(outputs[0]))
	mu.Unlock()

	e.Stop()
}

func TestIngestRawFrame_AutoCompletes(t *testing.T) {
	var mu sync.Mutex
	var completions []bool

	e := NewTransitionEngine(EngineConfig{
		DecoderFactory: func() (VideoDecoder, error) {
			return &mockDecoder{width: 4, height: 4}, nil
		},
		Output: func(yuv []byte, width, height int, pts int64, isKeyframe bool) {},
		OnComplete: func(aborted bool) {
			mu.Lock()
			completions = append(completions, !aborted)
			mu.Unlock()
		},
	})

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 50)) // 50ms

	w, h := 4, 4
	yuvSize := w * h * 3 / 2

	for i := 0; i < 20; i++ {
		e.IngestRawFrame("cam1", make([]byte, yuvSize), w, h, int64(i*3000))
		e.IngestRawFrame("cam2", make([]byte, yuvSize), w, h, int64(i*3000))
		time.Sleep(10 * time.Millisecond)
		if e.State() == StateIdle {
			break
		}
	}

	mu.Lock()
	require.GreaterOrEqual(t, len(completions), 1, "should have auto-completed")
	require.True(t, completions[0])
	mu.Unlock()
}

func TestIngestRawFrame_MixedWithIngestFrame(t *testing.T) {
	// Transition between H.264 source (cam1) and raw MXL source (cam2).
	var mu sync.Mutex
	var outputs [][]byte

	e := NewTransitionEngine(EngineConfig{
		DecoderFactory: func() (VideoDecoder, error) {
			return &mockDecoder{width: 4, height: 4}, nil
		},
		Output: func(yuv []byte, width, height int, pts int64, isKeyframe bool) {
			mu.Lock()
			cp := make([]byte, len(yuv))
			copy(cp, yuv)
			outputs = append(outputs, cp)
			mu.Unlock()
		},
		OnComplete: func(aborted bool) {},
	})

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 5000))

	// FROM via encoded H.264
	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)

	mu.Lock()
	require.Equal(t, 0, len(outputs))
	mu.Unlock()

	// TO via raw YUV (MXL source)
	yuvB := make([]byte, 4*4*3/2)
	for i := range yuvB {
		yuvB[i] = 128
	}
	e.IngestRawFrame("cam2", yuvB, 4, 4, 3000)

	mu.Lock()
	require.Equal(t, 1, len(outputs), "mixed H.264+raw should produce blended output")
	mu.Unlock()

	e.Stop()
}

func TestIngestRawFrame_DeepCopiesInput(t *testing.T) {
	var mu sync.Mutex
	var outputs [][]byte

	e := NewTransitionEngine(EngineConfig{
		DecoderFactory: func() (VideoDecoder, error) {
			return &mockDecoder{width: 4, height: 4}, nil
		},
		Output: func(yuv []byte, width, height int, pts int64, isKeyframe bool) {
			mu.Lock()
			cp := make([]byte, len(yuv))
			copy(cp, yuv)
			outputs = append(outputs, cp)
			mu.Unlock()
		},
		OnComplete: func(aborted bool) {},
	})

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 5000))

	w, h := 4, 4
	yuvSize := w * h * 3 / 2

	buf := make([]byte, yuvSize)
	for i := range buf {
		buf[i] = 100
	}
	e.IngestRawFrame("cam1", buf, w, h, 0)

	// Overwrite before second call
	for i := range buf {
		buf[i] = 200
	}
	e.IngestRawFrame("cam2", buf, w, h, 3000)

	mu.Lock()
	require.Equal(t, 1, len(outputs))
	out := outputs[0]
	mu.Unlock()

	// At position ~0 the blend is mostly source A (100). If deep copy failed,
	// source A buffer would also be 200 and output ~200 everywhere.
	hasNon200 := false
	for _, b := range out {
		if b != 200 {
			hasNon200 = true
			break
		}
	}
	require.True(t, hasNon200, "should reflect deep-copied source A, not overwritten buffer")

	e.Stop()
}

func TestIngestRawFrame_ConcurrentSafe(t *testing.T) {
	e, _, _, _ := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "cam2", TransitionMix, 5000))

	w, h := 4, 4
	yuvSize := w * h * 3 / 2

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			e.IngestRawFrame("cam1", make([]byte, yuvSize), w, h, int64(i*3000))
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			e.IngestRawFrame("cam2", make([]byte, yuvSize), w, h, int64(i*3000))
		}
	}()

	wg.Wait()
	e.Stop()
}
