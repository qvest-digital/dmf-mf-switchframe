package transition

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newTestEngine(t *testing.T) (*Engine, *sync.Mutex, *[][]byte, *[]bool) {
	t.Helper()
	var mu sync.Mutex
	var outputs [][]byte
	var completions []bool // true=completed, false=aborted

	e := NewEngine(EngineConfig{
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

	err := e.Start("cam1", "cam2", Mix, 1000)
	require.NoError(t, err)
	require.Equal(t, StateActive, e.State())

	e.Stop()
	require.Equal(t, StateIdle, e.State())
}

func TestEngineCannotStartWhileActive(t *testing.T) {
	e, _, _, _ := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "cam2", Mix, 1000))
	err := e.Start("cam1", "cam3", Mix, 1000)
	require.Error(t, err)
	require.Contains(t, err.Error(), "already active")

	e.Stop()
}

func TestEngineAutoPositionAdvances(t *testing.T) {
	e, _, _, _ := newTestEngine(t)

	err := e.Start("cam1", "cam2", Mix, 200) // 200ms
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

	require.NoError(t, e.Start("cam1", "cam2", Mix, 1000))

	e.SetPosition(0.3)
	require.InDelta(t, 0.3, e.Position(), 0.01)

	e.SetPosition(0.7)
	require.InDelta(t, 0.7, e.Position(), 0.01)

	e.Stop()
}

func TestEngineManualPositionCompletes(t *testing.T) {
	e, mu, _, completions := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "cam2", Mix, 1000))

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

	require.NoError(t, e.Start("cam1", "cam2", Mix, 1000))
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

	require.NoError(t, e.Start("cam1", "cam2", Mix, 1000))
	e.Stop()

	require.Equal(t, StateIdle, e.State())
	require.InDelta(t, 0.0, e.Position(), 0.01)
}

func TestEngineFTBStart(t *testing.T) {
	e, _, _, _ := newTestEngine(t)

	err := e.Start("cam1", "", FTB, 1000)
	require.NoError(t, err)
	require.Equal(t, StateActive, e.State())

	e.Stop()
}

func TestEngineType(t *testing.T) {
	e, _, _, _ := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "cam2", Dip, 1000))
	require.Equal(t, Dip, e.TransitionType())

	e.Stop()
}

func TestEngineIngestProducesOutput(t *testing.T) {
	e, mu, outputs, _ := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "cam2", Mix, 1000))

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

	require.NoError(t, e.Start("cam1", "cam2", Mix, 1000))

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

	require.NoError(t, e.Start("cam1", "", FTB, 1000))

	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)

	mu.Lock()
	require.Equal(t, 1, len(*outputs), "FTB: source A should trigger output")
	mu.Unlock()

	e.Stop()
}

func TestEngineMultipleFrames(t *testing.T) {
	e, mu, outputs, _ := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "cam2", Mix, 5000))

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

	require.NoError(t, e.Start("cam1", "cam2", Mix, 50)) // 50ms

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

	err := e.Start("cam1", "", FTBReverse, 1000)
	require.NoError(t, err)
	require.Equal(t, StateActive, e.State())
	require.Equal(t, FTBReverse, e.TransitionType())

	e.Stop()
}

func TestEngineFTBReverseIngestTriggersOnFromSource(t *testing.T) {
	e, mu, outputs, _ := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "", FTBReverse, 1000))

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
	e := NewEngine(EngineConfig{
		DecoderFactory: func() (VideoDecoder, error) {
			decoderCount++
			return &mockDecoder{width: 4, height: 4}, nil
		},
		Output:     func(yuv []byte, width, height int, pts int64, isKeyframe bool) {},
		OnComplete: func(aborted bool) {},
	})

	require.NoError(t, e.Start("cam1", "", FTBReverse, 1000))
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
	require.NoError(t, e.Start("cam1", "", FTBReverse, 60000))

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

	require.NoError(t, e.Start("cam1", "", FTB, 1000))
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
	for _, tt := range []Type{Mix, Wipe, Dip} {
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

func TestEngineSkipsBlendWhenSourceANil(t *testing.T) {
	// When FROM source hasn't decoded yet (latestYUVA nil), blend should
	// be skipped for non-FTB transitions to avoid a flash-to-black.
	e, mu, outputs, _ := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "cam2", Mix, 60000))

	// Warm up only the TO source.
	e.WarmupDecode("cam2", []byte{0x00, 0x00, 0x00, 0x01})
	e.WarmupComplete()

	// TO P-frame should NOT produce output (source A is nil).
	require.NotPanics(t, func() {
		e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 0, false)
	})

	mu.Lock()
	require.Equal(t, 0, len(*outputs), "should skip blend when source A is nil")
	mu.Unlock()

	e.Stop()
}

func TestEngineWarmupPopulatesState(t *testing.T) {
	e, mu, outputs, _ := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "cam2", Mix, 5000))

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

	require.NoError(t, e.Start("cam1", "cam2", Mix, 5000))

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

	require.NoError(t, e.Start("cam1", "cam2", Mix, 5000))

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
	e := NewEngine(EngineConfig{
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

	require.NoError(t, e.Start("cam1", "cam2", Mix, 5000))

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
	e := NewEngine(EngineConfig{
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

	require.NoError(t, e.Start("cam1", "cam2", Mix, 5000))

	// Ingest cam2 first — sets target to 8x8.
	// Source A is nil so blend is skipped (no black flash).
	e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)

	mu.Lock()
	require.Equal(t, 0, len(outputs), "cam2 alone should skip blend (source A nil)")
	mu.Unlock()

	// Ingest cam1 (4x4) — should be scaled to 8x8. Stored as source A.
	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)

	// Now ingest cam2 again — both sources available, blend produces output.
	e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 33000, true)

	mu.Lock()
	require.Equal(t, 1, len(outputs), "should produce output after scaling from-source")
	mu.Unlock()

	e.Stop()
}

func TestEngineWipeStart(t *testing.T) {
	e, _, _, _ := newTestEngine(t)

	e.config.WipeDirection = WipeHLeft
	err := e.Start("cam1", "cam2", Wipe, 1000)
	require.NoError(t, err)
	require.Equal(t, StateActive, e.State())
	require.Equal(t, Wipe, e.TransitionType())

	e.Stop()
}

func TestEngineWipeIngestProducesOutput(t *testing.T) {
	e, mu, outputs, _ := newTestEngine(t)

	e.config.WipeDirection = WipeVTop
	require.NoError(t, e.Start("cam1", "cam2", Wipe, 5000))

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

	require.NoError(t, e.Start("cam1", "cam2", Mix, 60000))

	e.SetPosition(0.5) // move past zero first
	e.SetPosition(1.5) // should be clamped — triggers completion since >= 0.999

	// After completion the engine returns to idle, so Position() returns 0.
	// The key assertion is that the engine didn't panic and state is idle
	// (completion was triggered, not a hang).
	require.Equal(t, StateIdle, e.State(), "pos 1.5 should trigger completion")
}

func TestPositionClampedBelowZero(t *testing.T) {
	e, _, _, _ := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "cam2", Mix, 60000))

	// SetPosition(-0.5) without moving past zero first — should not abort,
	// just clamp to 0.0 and stay active.
	e.SetPosition(-0.5)

	require.Equal(t, StateActive, e.State(), "negative pos without prior movement should stay active")
	require.InDelta(t, 0.0, e.Position(), 0.01, "position should be clamped to 0")
}

func TestCompletionThreshold(t *testing.T) {
	e, mu, _, completions := newTestEngine(t)

	require.NoError(t, e.Start("cam1", "cam2", Mix, 60000))

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

	require.NoError(t, e.Start("cam1", "cam2", Mix, 60000))

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

	e := NewEngine(EngineConfig{
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

	require.NoError(t, e.Start("cam1", "cam2", Mix, 5000))
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

	e := NewEngine(EngineConfig{
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

	require.NoError(t, e.Start("cam1", "cam2", Mix, 5000))

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

	e := NewEngine(EngineConfig{
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
	require.NoError(t, e.Start("cam1", "cam2", Mix, 50))

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

	e := NewEngine(EngineConfig{
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

	require.NoError(t, e.Start("cam1", "cam2", Mix, 5000))

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
	e := NewEngine(EngineConfig{
		DecoderFactory: func() (VideoDecoder, error) {
			return &mockDecoder{width: 4, height: 4}, nil
		},
		Output:     func(yuv []byte, width, height int, pts int64, isKeyframe bool) {},
		OnComplete: func(aborted bool) {},
	})

	require.Equal(t, DefaultTimeout, e.Timeout())
}

func TestTransitionSetTimeoutOverrides(t *testing.T) {
	e := NewEngine(EngineConfig{
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

	e := NewEngine(EngineConfig{
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

	require.NoError(t, e.Start("cam1", "cam2", Stinger, 1000))
	require.Equal(t, Stinger, e.TransitionType())

	// Ingest from both sources. Only TO (cam2) triggers blend output;
	// FROM (cam1) stores YUV but does not trigger redundant blend.
	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)
	e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)

	mu.Lock()
	require.Equal(t, 1, len(outputs), "stinger should produce one output per TO frame")
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

	e := NewEngine(EngineConfig{
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

	require.NoError(t, e.Start("cam1", "cam2", Stinger, 5000))

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

	e := NewEngine(EngineConfig{
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

	require.NoError(t, e.Start("cam1", "cam2", Stinger, 60000))

	// Only TO (cam2) triggers blend; FROM (cam1) stores YUV only.
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

	e := NewEngine(EngineConfig{
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
	require.NoError(t, e.Start("cam1", "cam2", Mix, 5000))
	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)
	e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)

	require.True(t, outputCalled, "output should have been called")
	e.Stop()
}

func TestEngineStingerNoData(t *testing.T) {
	// Stinger transition without stinger data should still work (fallback to hard cut)
	var mu sync.Mutex
	var outputs [][]byte

	e := NewEngine(EngineConfig{
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

	require.NoError(t, e.Start("cam1", "cam2", Stinger, 1000))

	// Only TO (cam2) triggers blend output.
	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)
	e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)

	mu.Lock()
	require.Equal(t, 1, len(outputs), "stinger without data should produce output for TO frame")
	mu.Unlock()

	e.Stop()
}

func TestEngineStinger_OutputRateMatchesDissolve(t *testing.T) {
	// Stinger should produce the same output rate as dissolve: one output per
	// TO-source frame. The FROM source stores its YUV for blending but should
	// NOT trigger a redundant blend output. If stinger triggers on BOTH
	// isFrom+isTo, the output rate is 2x (one per source frame), causing:
	//  - 2x encoder load
	//  - 2x PTS advancement (persistent A/V desync)
	//  - frame drops in the video processing channel
	w, h := 4, 4
	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	totalSize := ySize + 2*uvSize

	// Stinger with 10 frames
	frames := make([]StingerFrameData, 10)
	for i := range frames {
		frames[i] = StingerFrameData{
			YUV:   make([]byte, totalSize),
			Alpha: make([]byte, ySize),
		}
	}

	countOutputs := func(tt Type) int {
		var mu sync.Mutex
		var count int

		cfg := EngineConfig{
			Output: func(yuv []byte, width, height int, pts int64, isKeyframe bool) {
				mu.Lock()
				count++
				mu.Unlock()
			},
			OnComplete: func(aborted bool) {},
		}
		if tt == Stinger {
			cfg.Stinger = &StingerData{
				Frames:   frames,
				Width:    w,
				Height:   h,
				CutPoint: 0.5,
			}
		}
		e := NewEngine(cfg)
		require.NoError(t, e.Start("cam1", "cam2", tt, 60000)) // long duration

		// Send 10 frame pairs (one from each source per pair)
		yuvA := make([]byte, totalSize)
		yuvB := make([]byte, totalSize)
		for i := 0; i < 10; i++ {
			yuvA[0] = byte(i)
			yuvB[0] = byte(100 + i)
			e.IngestRawFrame("cam1", yuvA, w, h, int64(i*3000))
			e.IngestRawFrame("cam2", yuvB, w, h, int64(i*3000))
		}

		e.Stop()
		mu.Lock()
		defer mu.Unlock()
		return count
	}

	dissolveOutputs := countOutputs(Mix)
	stingerOutputs := countOutputs(Stinger)

	require.Equal(t, 10, dissolveOutputs, "dissolve should produce one output per TO frame")
	require.Equal(t, dissolveOutputs, stingerOutputs,
		"stinger should produce same output rate as dissolve (got %d stinger vs %d dissolve)",
		stingerOutputs, dissolveOutputs)
}

func TestEngineDecodeFallThroughToBlendOnEAGAIN(t *testing.T) {
	// Scenario: warmup decode succeeds (blender initialized), but the first
	// live IngestFrame decode returns EAGAIN (B-frame reorder). The engine
	// should still produce output using the black frame fallback instead of
	// bailing out when decodeAndStore returns false.
	var mu sync.Mutex
	var outputs [][]byte

	decoderCount := 0
	e := NewEngine(EngineConfig{
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

	require.NoError(t, e.Start("cam1", "cam2", Mix, 5000))

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
	e = NewEngine(EngineConfig{
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

	require.NoError(t, e.Start("cam1", "cam2", Mix, 5000))

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

	e := NewEngine(EngineConfig{
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
		OnComplete: func(aborted bool) {},
		HintWidth:  4,
		HintHeight: 4,
	})

	require.NoError(t, e.Start("cam1", "cam2", Mix, 5000))

	// Warmup: both return EAGAIN. Without hint, blender stays nil.
	e.WarmupDecode("cam1", []byte{0x00, 0x00, 0x00, 0x01})
	e.WarmupDecode("cam2", []byte{0x00, 0x00, 0x00, 0x01})
	e.WarmupComplete()

	// Live keyframe from TO source — decode returns EAGAIN again.
	// With hint dimensions: blender pre-initialized, but source A is nil
	// so blend is skipped (no black flash).
	e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)

	mu.Lock()
	require.Equal(t, 0, len(outputs),
		"hint dimensions pre-init blender but blend skipped when source A nil")
	mu.Unlock()

	// Feed enough frames to get past EAGAIN and produce real output.
	for i := 0; i < 4; i++ {
		e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, int64(i*33000), true)
		e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, int64(i*33000), true)
	}

	mu.Lock()
	gotOutput := len(outputs) > 0
	mu.Unlock()
	require.True(t, gotOutput, "should eventually produce output once both sources decode")

	e.Stop()
}

// --- IngestRawFrame tests ---

func TestIngestRawFrame_ProducesOutput(t *testing.T) {
	// IngestRawFrame bypasses H.264 decode — stores YUV directly.
	// Output triggered when TO source provides raw YUV.
	var mu sync.Mutex
	var outputs [][]byte

	e := NewEngine(EngineConfig{
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

	require.NoError(t, e.Start("cam1", "cam2", Mix, 5000))

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

	e := NewEngine(EngineConfig{
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

	require.NoError(t, e.Start("cam1", "cam2", Mix, 5000))

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

	e := NewEngine(EngineConfig{
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

	e := NewEngine(EngineConfig{
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

	require.NoError(t, e.Start("cam1", "cam2", Mix, 5000))

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

	e := NewEngine(EngineConfig{
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

	require.NoError(t, e.Start("cam1", "cam2", Mix, 50)) // 50ms

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

	e := NewEngine(EngineConfig{
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

	require.NoError(t, e.Start("cam1", "cam2", Mix, 5000))

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

	e := NewEngine(EngineConfig{
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

	require.NoError(t, e.Start("cam1", "cam2", Mix, 5000))

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

	require.NoError(t, e.Start("cam1", "cam2", Mix, 5000))

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

func TestEngineTimingInstrumentation(t *testing.T) {
	e, mu, outputs, _ := newTestEngine(t)

	// Start a Mix transition with a long duration so it stays active
	require.NoError(t, e.Start("cam1", "cam2", Mix, 5000))

	// Ingest several frames from both sources
	for i := 0; i < 5; i++ {
		e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, int64(i*3000), true)
		e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, int64(i*3000), true)
	}

	// Verify output was produced (ensures blend path was exercised)
	mu.Lock()
	require.Greater(t, len(*outputs), 0, "should have produced blended output")
	mu.Unlock()

	// Get timing snapshot
	timing := e.Timing()

	// frames_ingested should count all IngestFrame calls (10 total: 5 from each source)
	framesIngested, ok := timing["frames_ingested"].(int64)
	require.True(t, ok, "frames_ingested should be int64")
	require.Equal(t, int64(10), framesIngested)

	// frames_blended should be > 0 (only TO source triggers blend for Mix)
	framesBlended, ok := timing["frames_blended"].(int64)
	require.True(t, ok, "frames_blended should be int64")
	require.Greater(t, framesBlended, int64(0), "should have blended frames")

	// decode_last_ms should be >= 0 (mock decoders are fast but measurable)
	decodeLast, ok := timing["decode_last_ms"].(float64)
	require.True(t, ok, "decode_last_ms should be float64")
	require.GreaterOrEqual(t, decodeLast, 0.0, "decode_last_ms should be non-negative")

	// decode_max_ms should be >= decode_last_ms (or equal)
	decodeMax, ok := timing["decode_max_ms"].(float64)
	require.True(t, ok, "decode_max_ms should be float64")
	require.GreaterOrEqual(t, decodeMax, 0.0, "decode_max_ms should be non-negative")

	// ingest_last_ms should be >= 0
	ingestLast, ok := timing["ingest_last_ms"].(float64)
	require.True(t, ok, "ingest_last_ms should be float64")
	require.GreaterOrEqual(t, ingestLast, 0.0, "ingest_last_ms should be non-negative")

	// ingest_max_ms >= ingest_last_ms
	ingestMax, ok := timing["ingest_max_ms"].(float64)
	require.True(t, ok, "ingest_max_ms should be float64")
	require.GreaterOrEqual(t, ingestMax, 0.0, "ingest_max_ms should be non-negative")

	// blend_last_ms should be >= 0
	blendLast, ok := timing["blend_last_ms"].(float64)
	require.True(t, ok, "blend_last_ms should be float64")
	require.GreaterOrEqual(t, blendLast, 0.0, "blend_last_ms should be non-negative")

	// blend_max_ms >= 0
	blendMax, ok := timing["blend_max_ms"].(float64)
	require.True(t, ok, "blend_max_ms should be float64")
	require.GreaterOrEqual(t, blendMax, 0.0, "blend_max_ms should be non-negative")

	e.Stop()
}

func TestEngineTimingInstrumentation_RawFrames(t *testing.T) {
	e, mu, outputs, _ := newTestEngine(t)

	w, h := 4, 4
	yuvSize := w * h * 3 / 2

	require.NoError(t, e.Start("cam1", "cam2", Mix, 5000))

	// Ingest raw frames (skip decode path)
	for i := 0; i < 5; i++ {
		e.IngestRawFrame("cam1", make([]byte, yuvSize), w, h, int64(i*3000))
		e.IngestRawFrame("cam2", make([]byte, yuvSize), w, h, int64(i*3000))
	}

	mu.Lock()
	require.Greater(t, len(*outputs), 0, "should have produced blended output")
	mu.Unlock()

	timing := e.Timing()

	// Raw frames still count as ingested
	framesIngested, ok := timing["frames_ingested"].(int64)
	require.True(t, ok)
	require.Equal(t, int64(10), framesIngested)

	// Raw frames still trigger blending
	framesBlended, ok := timing["frames_blended"].(int64)
	require.True(t, ok)
	require.Greater(t, framesBlended, int64(0))

	// decode_last_ms should be 0 for raw frames (no decode step)
	decodeLast, ok := timing["decode_last_ms"].(float64)
	require.True(t, ok)
	require.Equal(t, 0.0, decodeLast, "raw frames should not update decode timing")

	// ingest and blend should still be tracked
	ingestLast, ok := timing["ingest_last_ms"].(float64)
	require.True(t, ok)
	require.GreaterOrEqual(t, ingestLast, 0.0)

	blendLast, ok := timing["blend_last_ms"].(float64)
	require.True(t, ok)
	require.GreaterOrEqual(t, blendLast, 0.0)

	e.Stop()
}

func TestEngineTimingInstrumentation_ZeroBeforeIngest(t *testing.T) {
	e, _, _, _ := newTestEngine(t)

	// Before any transitions, all timing values should be zero
	timing := e.Timing()

	require.Equal(t, float64(0), timing["decode_last_ms"])
	require.Equal(t, float64(0), timing["decode_max_ms"])
	require.Equal(t, float64(0), timing["blend_last_ms"])
	require.Equal(t, float64(0), timing["blend_max_ms"])
	require.Equal(t, float64(0), timing["ingest_last_ms"])
	require.Equal(t, float64(0), timing["ingest_max_ms"])
	require.Equal(t, int64(0), timing["frames_ingested"])
	require.Equal(t, int64(0), timing["frames_blended"])
}

func TestEngineParallelIngest(t *testing.T) {
	// Verify that two sources can ingest frames simultaneously without
	// deadlock. With the old lock scope (decode+blend under one lock),
	// source B's IngestFrame would block while source A was decoding.
	// The reduced lock scope allows both decoders to run concurrently.
	const iterations = 100

	var mu sync.Mutex
	var outputs [][]byte

	e := NewEngine(EngineConfig{
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

	require.NoError(t, e.Start("cam1", "cam2", Mix, 60000))

	// Prime source A so latestYUVA is non-nil before parallel ingest.
	// The engine skips blends when source A hasn't delivered a frame yet
	// (to avoid a black flash), so without this the test is racy: all
	// cam2 frames can arrive before cam1's first frame is stored.
	e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 1; i <= iterations; i++ {
			e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, int64(i*3000), true)
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, int64(i*3000), true)
		}
	}()

	wg.Wait()

	mu.Lock()
	outputCount := len(outputs)
	mu.Unlock()

	require.Greater(t, outputCount, 0, "parallel ingest should produce blended output")
	require.LessOrEqual(t, outputCount, iterations, "at most one output per TO-source frame")

	timing := e.Timing()
	framesIngested := timing["frames_ingested"].(int64)
	require.Equal(t, int64(2*iterations+1), framesIngested,
		"all frames from both sources (plus priming frame) should be counted")

	ingestMaxMs, ok := timing["ingest_max_ms"].(float64)
	require.True(t, ok, "ingest_max_ms should be float64")
	require.Less(t, ingestMaxMs, 50.0, "max ingest time should be < 50ms even on slow CI")

	e.Stop()
}

// --- Easing integration tests ---

func TestEngineEasingDefault(t *testing.T) {
	// nil Easing should default to smoothstep behavior.
	e := NewEngine(EngineConfig{
		Output:     func(yuv []byte, width, height int, pts int64, isKeyframe bool) {},
		OnComplete: func(aborted bool) {},
	})

	err := e.Start("cam1", "cam2", Mix, 1000)
	require.NoError(t, err)
	defer e.Stop()

	// Easing() should report smoothstep when config.Easing is nil.
	require.Equal(t, EasingSmoothstep, e.Easing())

	// At ~25% elapsed, smoothstep(0.25) = 0.25^2*(3-2*0.25) = 0.15625.
	// Wait 250ms for a 1000ms transition, then check position.
	time.Sleep(250 * time.Millisecond)
	pos := e.Position()
	// The actual elapsed time has jitter, so we verify the position is NOT
	// close to the linear value of ~0.25. Smoothstep bends downward early,
	// so the eased position should be less than the raw time fraction.
	// With timing jitter the raw t might be 0.25±0.05, but smoothstep(0.3)=0.216
	// and smoothstep(0.2)=0.104 — both well below 0.3. We just verify < 0.35.
	require.Less(t, pos, 0.35, "smoothstep should produce a position below the linear value near t=0.25")
}

func TestEngineEasingLinear(t *testing.T) {
	e := NewEngine(EngineConfig{
		Easing:     NewEasingCurve(EasingLinear),
		Output:     func(yuv []byte, width, height int, pts int64, isKeyframe bool) {},
		OnComplete: func(aborted bool) {},
	})

	err := e.Start("cam1", "cam2", Mix, 100)
	require.NoError(t, err)
	defer e.Stop()

	require.Equal(t, EasingLinear, e.Easing())

	// Sleep roughly half the duration and check that Position is near 0.5.
	time.Sleep(50 * time.Millisecond)
	pos := e.Position()
	require.InDelta(t, 0.5, pos, 0.15, "linear easing at ~50%% elapsed should be near 0.5")
}

func TestEngineEasingCustom(t *testing.T) {
	ec, err := NewCustomEasingCurve(0.42, 0, 0.58, 1.0) // ease-in-out
	require.NoError(t, err)

	e := NewEngine(EngineConfig{
		Easing:     ec,
		Output:     func(yuv []byte, width, height int, pts int64, isKeyframe bool) {},
		OnComplete: func(aborted bool) {},
	})

	err = e.Start("cam1", "cam2", Mix, 500)
	require.NoError(t, err)
	defer e.Stop()

	require.Equal(t, EasingCustom, e.Easing())
}

func TestEngineEasingIgnoredForManual(t *testing.T) {
	e := NewEngine(EngineConfig{
		Easing:     NewEasingCurve(EasingLinear),
		Output:     func(yuv []byte, width, height int, pts int64, isKeyframe bool) {},
		OnComplete: func(aborted bool) {},
	})

	err := e.Start("cam1", "cam2", Mix, 2000)
	require.NoError(t, err)
	defer e.Stop()

	// SetPosition switches to manual mode — easing is bypassed.
	e.SetPosition(0.5)
	require.Equal(t, 0.5, e.Position(), "manual position should be exactly 0.5, not eased")

	e.SetPosition(0.75)
	require.Equal(t, 0.75, e.Position(), "manual position should be exactly 0.75, not eased")
}

func TestIngestRawFrame_SkipsBlendWhenSourceANil(t *testing.T) {
	// When a non-FTB transition starts, the "to" source frame may arrive
	// before the "from" source frame. Previously, a black frame was
	// substituted for source A, causing a visible flash-to-black on the
	// first blend. The correct behavior is to skip the blend entirely
	// until source A has a real frame.
	var mu sync.Mutex
	var outputs [][]byte

	e := NewEngine(EngineConfig{
		HintWidth:  4,
		HintHeight: 4,
		Output: func(yuv []byte, width, height int, pts int64, isKeyframe bool) {
			mu.Lock()
			cp := make([]byte, len(yuv))
			copy(cp, yuv)
			outputs = append(outputs, cp)
			mu.Unlock()
		},
		OnComplete: func(aborted bool) {},
	})

	require.NoError(t, e.Start("cam1", "cam2", Mix, 5000))

	w, h := 4, 4
	yuvSize := w * h * 3 / 2

	// Send ONLY the TO source frame (cam2) — no FROM source frame yet.
	// This simulates the race where "to" arrives before "from".
	yuvB := make([]byte, yuvSize)
	for i := range yuvB {
		yuvB[i] = 200
	}
	e.IngestRawFrame("cam2", yuvB, w, h, 3000)

	mu.Lock()
	outputCount := len(outputs)
	mu.Unlock()

	// Should produce NO output — not a black flash.
	require.Equal(t, 0, outputCount,
		"should skip blend when source A is nil, not substitute black")

	// Now send the FROM source frame — next TO frame should produce output.
	yuvA := make([]byte, yuvSize)
	for i := range yuvA {
		yuvA[i] = 100
	}
	e.IngestRawFrame("cam1", yuvA, w, h, 3000)

	mu.Lock()
	outputCount = len(outputs)
	mu.Unlock()
	require.Equal(t, 0, outputCount,
		"FROM source alone should not trigger blend")

	// Second TO frame — now both sources have data, blend should produce output.
	e.IngestRawFrame("cam2", yuvB, w, h, 6000)

	mu.Lock()
	outputCount = len(outputs)
	mu.Unlock()
	require.Equal(t, 1, outputCount,
		"blend should produce output once both sources have frames")

	e.Stop()
}

func TestIngestRawFrame_FTBStillUsesBlackForSourceB(t *testing.T) {
	// FTB transitions use only source A (fading to black). Source B being
	// nil is expected and should use getBlackFrame() — NOT skip the blend.
	var mu sync.Mutex
	var outputs [][]byte

	e := NewEngine(EngineConfig{
		HintWidth:  4,
		HintHeight: 4,
		Output: func(yuv []byte, width, height int, pts int64, isKeyframe bool) {
			mu.Lock()
			cp := make([]byte, len(yuv))
			copy(cp, yuv)
			outputs = append(outputs, cp)
			mu.Unlock()
		},
		OnComplete: func(aborted bool) {},
	})

	require.NoError(t, e.Start("cam1", "cam2", FTB, 5000))

	w, h := 4, 4
	yuvSize := w * h * 3 / 2

	// FTB: only FROM source (cam1) triggers blend. Source B is irrelevant.
	yuvA := make([]byte, yuvSize)
	for i := range yuvA {
		yuvA[i] = 200
	}
	e.IngestRawFrame("cam1", yuvA, w, h, 3000)

	mu.Lock()
	outputCount := len(outputs)
	mu.Unlock()

	// FTB should produce output from first FROM frame (blending with black).
	require.Equal(t, 1, outputCount,
		"FTB should produce output from first FROM frame, using black for B")

	e.Stop()
}

// racyDecoder has a shared non-atomic field written by Close and read by
// Decode. The race detector will flag concurrent access.
type racyDecoder struct {
	width, height int
	delay         time.Duration
	// shared is written by Close and read by Decode — triggers -race.
	shared int
}

func newRacyDecoder(w, h int, delay time.Duration) *racyDecoder {
	return &racyDecoder{width: w, height: h, delay: delay}
}

func (d *racyDecoder) Decode(data []byte) ([]byte, int, int, error) {
	if d.delay > 0 {
		time.Sleep(d.delay)
	}
	// Read shared state (races with Close's write)
	_ = d.shared
	return make([]byte, d.width*d.height*3/2), d.width, d.height, nil
}

func (d *racyDecoder) Close() {
	// Write shared state (races with Decode's read)
	d.shared = 1
}

func TestEngineConcurrentAbortAndIngest(t *testing.T) {
	// This test verifies that Abort() does not close decoders while
	// IngestFrame is using them in Phase 2 (the lock-free decode phase).
	// Run with -race to detect data races.
	for i := 0; i < 20; i++ {
		e := NewEngine(EngineConfig{
			DecoderFactory: func() (VideoDecoder, error) {
				return newRacyDecoder(4, 4, 5*time.Millisecond), nil
			},
			Output:     func([]byte, int, int, int64, bool) {},
			OnComplete: func(bool) {},
		})

		require.NoError(t, e.Start("cam1", "cam2", Mix, 5000))

		var wg sync.WaitGroup

		// Goroutine 1: rapid ingestion
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				e.IngestFrame("cam1", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)
				e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01}, 0, true)
			}
		}()

		// Goroutine 2: abort after a brief delay
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(2 * time.Millisecond)
			e.Abort()
		}()

		wg.Wait()
		e.Stop()
	}
}

// slowMockDecoder blocks on a channel during Decode to simulate slow decoding.
// This makes the cleanup() unlock window visible for race testing.
type slowMockDecoder struct {
	width, height int
	blockCh       chan struct{} // decode blocks until this is closed
}

func (d *slowMockDecoder) Decode(data []byte) ([]byte, int, int, error) {
	<-d.blockCh
	return make([]byte, d.width*d.height*3/2), d.width, d.height, nil
}

func (d *slowMockDecoder) Close() {}

func TestCleanupBlocksConcurrentStart(t *testing.T) {
	blockCh := make(chan struct{})

	e := NewEngine(EngineConfig{
		DecoderFactory: func() (VideoDecoder, error) {
			return &slowMockDecoder{width: 4, height: 4, blockCh: blockCh}, nil
		},
		Output:     func(yuv []byte, width, height int, pts int64, isKeyframe bool) {},
		OnComplete: func(aborted bool) {},
	})

	// Start the first transition.
	err := e.Start("cam1", "cam2", Mix, 5000)
	require.NoError(t, err)

	// Kick off a slow in-flight decode by ingesting a frame.
	// IngestFrame will Add(1) to decodeWG then block in Decode().
	go e.IngestFrame("cam2", []byte{0x00, 0x00, 0x00, 0x01, 0x65}, 1000, true)

	// Give IngestFrame time to enter Phase 2 (blocked in Decode).
	time.Sleep(10 * time.Millisecond)

	// Start Stop() in a goroutine — it will call cleanup() which sets
	// state=Idle, cleaning=true, unlocks, then blocks on decodeWG.Wait().
	stopDone := make(chan struct{})
	go func() {
		e.Stop()
		close(stopDone)
	}()

	// Give cleanup time to reach the unlock+Wait window.
	time.Sleep(10 * time.Millisecond)

	// Attempt Start() during the cleanup window. With the fix, this must
	// fail because cleaning==true even though state==Idle.
	err = e.Start("cam3", "cam4", Mix, 1000)
	require.Error(t, err, "Start() must fail during cleanup window")
	require.ErrorIs(t, err, ErrActive)

	// Unblock the slow decode so cleanup can finish.
	close(blockCh)

	// Wait for Stop() to complete.
	select {
	case <-stopDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop() did not complete in time")
	}

	// Engine should be idle and usable after cleanup completes.
	require.Equal(t, StateIdle, e.State())

	// Start should succeed now.
	err = e.Start("cam5", "cam6", Mix, 1000)
	require.NoError(t, err, "Start() should succeed after cleanup completes")
	e.Stop()
}

func TestEngineSkipBlend(t *testing.T) {
	// SkipBlend mode: engine tracks position but does NOT auto-complete.
	// The caller (GPU transition path) calls ForceComplete after the GPU
	// blend frame is produced, ensuring the last frame reaches the encoder
	// before the transition state changes.
	var outputCalled bool
	var completions []bool
	var mu sync.Mutex

	e := NewEngine(EngineConfig{
		SkipBlend: true,
		Output: func(yuv []byte, width, height int, pts int64, isKeyframe bool) {
			mu.Lock()
			outputCalled = true
			mu.Unlock()
		},
		OnComplete: func(aborted bool) {
			mu.Lock()
			completions = append(completions, !aborted)
			mu.Unlock()
		},
		HintWidth:  4,
		HintHeight: 4,
	})

	err := e.Start("cam1", "cam2", Mix, 10) // 10ms — completes quickly
	require.NoError(t, err)

	// Feed frames to drive position tracking.
	frame := make([]byte, 4*4*3/2)
	for i := 0; i < 5; i++ {
		e.IngestRawFrame("cam1", frame, 4, 4, int64(i*3000))
		e.IngestRawFrame("cam2", frame, 4, 4, int64(i*3000))
		time.Sleep(5 * time.Millisecond)
	}

	// SkipBlend does NOT auto-complete — verify no completion yet.
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	require.False(t, outputCalled, "Output should never be called in SkipBlend mode")
	require.Empty(t, completions, "SkipBlend should NOT auto-complete")
	mu.Unlock()

	// Position should be >= 1.0 after 50ms of a 10ms transition.
	require.GreaterOrEqual(t, e.Position(), 1.0)

	// Caller drives completion after GPU blend.
	e.ForceComplete()

	mu.Lock()
	require.NotEmpty(t, completions, "ForceComplete should trigger OnComplete")
	require.True(t, completions[0], "ForceComplete should report success")
	mu.Unlock()
}

func TestEngineWipeDirectionAccessor(t *testing.T) {
	e := NewEngine(EngineConfig{
		WipeDirection: WipeHRight,
		HintWidth:     4,
		HintHeight:    4,
	})

	err := e.Start("cam1", "cam2", Wipe, 1000)
	require.NoError(t, err)
	require.Equal(t, WipeHRight, e.WipeDirection())
	e.Stop()

	// After stop, WipeDirection returns whatever was set.
	require.Equal(t, WipeHRight, e.WipeDirection())
}
