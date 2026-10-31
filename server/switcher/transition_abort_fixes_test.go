package switcher

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/audio"
)

// --- Finding #65: FadeToBlack must reject a press during an active FTB leg ---
//
// A double-press while the fade is still running (StateFTBTransitioning) or
// while reversing (StateFTBReversing) must not spawn a second concurrent
// transition engine over the live one.

func TestSwitcherFTBRejectsWhileFTBForwardInFlight(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()
	require.NoError(t, sw.SetFTBDuration(5000))

	require.NoError(t, sw.FadeToBlack(context.Background()))

	sw.configMu.Lock()
	firstEngine := sw.transEngine
	require.Equal(t, StateFTBTransitioning, sw.state)
	sw.configMu.Unlock()

	// Double-press while the forward fade is still running.
	err := sw.FadeToBlack(context.Background())
	require.Error(t, err, "FTB double-press during forward fade must be rejected")

	sw.configMu.Lock()
	require.Same(t, firstEngine, sw.transEngine,
		"the live FTB engine must not be replaced by a second engine")
	require.Equal(t, StateFTBTransitioning, sw.state)
	sw.configMu.Unlock()
}

func TestSwitcherFTBRejectsWhileFTBReversing(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	// Forward FTB, complete it (screen black).
	require.NoError(t, sw.FadeToBlack(context.Background()))
	require.NoError(t, sw.SetTransitionPosition(context.Background(), 1.0))
	require.Eventually(t, func() bool {
		state := sw.State()
		return state.FTBActive && !state.InTransition
	}, 2*time.Second, 5*time.Millisecond, "FTB should complete")

	// Toggle off with a slow reverse so it is still in flight.
	require.NoError(t, sw.SetFTBDuration(5000))
	require.NoError(t, sw.FadeToBlack(context.Background()))

	sw.configMu.Lock()
	reverseEngine := sw.transEngine
	require.Equal(t, StateFTBReversing, sw.state)
	sw.configMu.Unlock()

	// Press again while reversing — must be rejected, not start a parallel engine.
	err := sw.FadeToBlack(context.Background())
	require.Error(t, err, "FTB press during reverse fade must be rejected")

	sw.configMu.Lock()
	require.Same(t, reverseEngine, sw.transEngine,
		"the live reverse FTB engine must not be replaced")
	require.Equal(t, StateFTBReversing, sw.state)
	sw.configMu.Unlock()
}

// --- Finding #83: Cut racing the unlocked engine-creation window ---
//
// StartTransition/FadeToBlack drop configMu while creating the engine and
// starting the audio crossfade, then publish the engine unconditionally. A
// Cut landing in that window aborts a nil engine and returns to idle; the
// publish must detect this and NOT install the engine (otherwise hotState is
// stuck in Transitioning with a dead completion handler and all program
// frames are dropped).

// gatedAudioTransHandler blocks inside OnTransitionStart until released,
// modeling a Cut landing in StartTransition's unlocked setup window.
type gatedAudioTransHandler struct {
	mockAudioTransHandler
	entered chan struct{} // closed when OnTransitionStart is entered
	release chan struct{} // closing this lets OnTransitionStart return
}

func newGatedAudioTransHandler() *gatedAudioTransHandler {
	return &gatedAudioTransHandler{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (g *gatedAudioTransHandler) OnTransitionStart(oldSource, newSource string, mode audio.TransitionMode, durationMs int) uint64 {
	close(g.entered)
	<-g.release
	return g.mockAudioTransHandler.OnTransitionStart(oldSource, newSource, mode, durationMs)
}

func TestCutDuringStartTransitionWindowDoesNotPublishStaleEngine(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	gate := newGatedAudioTransHandler()
	sw.SetAudioTransition(gate)

	startErr := make(chan error, 1)
	go func() {
		startErr <- sw.StartTransition(context.Background(), "cam2", "mix", 60000, "")
	}()

	// Wait until StartTransition is inside the unlocked window.
	select {
	case <-gate.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("OnTransitionStart never entered")
	}

	// Cut lands inside the window: aborts a nil engine, returns to idle.
	require.NoError(t, sw.Cut(context.Background(), "cam2"))

	// Let StartTransition proceed to its publish phase.
	close(gate.release)

	select {
	case err := <-startErr:
		require.Error(t, err, "StartTransition pre-empted by Cut must report failure")
	case <-time.After(2 * time.Second):
		t.Fatal("StartTransition did not return")
	}

	// The pre-empted engine must not have been published anywhere.
	hs := sw.hotState.Load()
	require.Equal(t, StateIdle, hs.state,
		"hotState must stay Idle after Cut pre-empts the transition setup")
	require.Nil(t, hs.transEngine, "hotState must not carry the dead engine")

	sw.configMu.Lock()
	require.Equal(t, StateIdle, sw.state)
	require.Nil(t, sw.transEngine)
	sw.configMu.Unlock()

	require.Equal(t, "cam2", sw.State().ProgramSource, "the Cut wins")
}

func TestCutDuringFadeToBlackWindowDoesNotPublishStaleEngine(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	gate := newGatedAudioTransHandler()
	sw.SetAudioTransition(gate)

	ftbErr := make(chan error, 1)
	go func() {
		ftbErr <- sw.FadeToBlack(context.Background())
	}()

	select {
	case <-gate.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("OnTransitionStart never entered")
	}

	// Cut aborts the (nil-engine) FTB and returns to idle.
	require.NoError(t, sw.Cut(context.Background(), "cam2"))

	close(gate.release)

	select {
	case err := <-ftbErr:
		require.Error(t, err, "FadeToBlack pre-empted by Cut must report failure")
	case <-time.After(2 * time.Second):
		t.Fatal("FadeToBlack did not return")
	}

	hs := sw.hotState.Load()
	require.Equal(t, StateIdle, hs.state,
		"hotState must stay Idle after Cut pre-empts the FTB setup")
	require.Nil(t, hs.transEngine, "hotState must not carry the dead FTB engine")

	sw.configMu.Lock()
	require.Equal(t, StateIdle, sw.state)
	require.Nil(t, sw.transEngine)
	sw.configMu.Unlock()
}
