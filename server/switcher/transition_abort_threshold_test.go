package switcher

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSwitcher_TransitionAbortThresholdDefaultsTo03(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	require.InDelta(t, 0.3, sw.TransitionAbortThreshold(), 1e-9)
	require.InDelta(t, 0.3, sw.State().TransitionAbortThreshold, 1e-9)
}

func TestSwitcher_SetTransitionAbortThresholdValidatesRange(t *testing.T) {
	programRelay := newTestRelay()
	sw := newTestSwitcher(t, programRelay)
	defer sw.Close()

	require.True(t, errors.Is(sw.SetTransitionAbortThreshold(0.04), ErrInvalidTransitionAbortThreshold))
	require.True(t, errors.Is(sw.SetTransitionAbortThreshold(0.51), ErrInvalidTransitionAbortThreshold))
	require.True(t, errors.Is(sw.SetTransitionAbortThreshold(0), ErrInvalidTransitionAbortThreshold))
	require.True(t, errors.Is(sw.SetTransitionAbortThreshold(-0.1), ErrInvalidTransitionAbortThreshold))

	require.NoError(t, sw.SetTransitionAbortThreshold(0.05))
	require.InDelta(t, 0.05, sw.TransitionAbortThreshold(), 1e-9)
	require.InDelta(t, 0.05, sw.State().TransitionAbortThreshold, 1e-9)

	require.NoError(t, sw.SetTransitionAbortThreshold(0.5))
	require.InDelta(t, 0.5, sw.TransitionAbortThreshold(), 1e-9)

	require.NoError(t, sw.SetTransitionAbortThreshold(0.2))
	require.InDelta(t, 0.2, sw.TransitionAbortThreshold(), 1e-9)
}

func TestSwitcher_TBarPullbackBelowThresholdAborts(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	audioTrans := &mockAudioTransHandler{}
	sw.SetAudioTransition(audioTrans)

	// Default threshold 0.3; 2× = 0.6. Max-forward 0.7 satisfies that.
	err := sw.StartTransition(context.Background(), "cam2", "mix", 60000, "")
	require.NoError(t, err)
	require.True(t, sw.State().InTransition)

	// Drive forward past 2× threshold.
	require.NoError(t, sw.SetTransitionPosition(context.Background(), 0.7))
	require.True(t, sw.State().InTransition, "engine still active at 0.7")

	// Pull back below threshold — should trigger abort.
	require.NoError(t, sw.SetTransitionPosition(context.Background(), 0.2))
	time.Sleep(20 * time.Millisecond)

	state := sw.State()
	require.False(t, state.InTransition, "pullback below threshold should abort the transition")
	require.Equal(t, "cam1", state.ProgramSource, "program reverts to from-source on abort")
	require.Equal(t, "cam1", state.PreviewSource, "preview remains swapped to from-source (BUG-015)")

	audioTrans.mu.Lock()
	require.Equal(t, 1, audioTrans.abortCount, "audio handler must see a single abort")
	audioTrans.mu.Unlock()
}

func TestSwitcher_TBarSmoothReverseNoOvershoot(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	err := sw.StartTransition(context.Background(), "cam2", "mix", 60000, "")
	require.NoError(t, err)

	require.NoError(t, sw.SetTransitionPosition(context.Background(), 0.7))
	// 0.3 is AT threshold, not below — engine must remain active.
	require.NoError(t, sw.SetTransitionPosition(context.Background(), 0.3))

	state := sw.State()
	require.True(t, state.InTransition, "position at threshold (not below) keeps transition active")
	require.InDelta(t, 0.3, state.TransitionPosition, 1e-9, "engine reflects T-bar position 0.3")
}

func TestSwitcher_TBarReverseRestoresProgramAndPreviewAfterAbort(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	require.NoError(t, sw.SetTransitionAbortThreshold(0.2))

	err := sw.StartTransition(context.Background(), "cam2", "mix", 60000, "")
	require.NoError(t, err)

	require.Equal(t, "cam1", sw.State().PreviewSource)

	require.NoError(t, sw.SetTransitionPosition(context.Background(), 0.5))
	require.True(t, sw.State().InTransition)

	// Pull back to 0.1 (below 0.2); max-forward 0.5 ≥ 2×0.2=0.4. Abort fires.
	require.NoError(t, sw.SetTransitionPosition(context.Background(), 0.1))
	time.Sleep(20 * time.Millisecond)

	state := sw.State()
	require.False(t, state.InTransition)
	require.Equal(t, "cam1", state.ProgramSource)
	require.Equal(t, "cam1", state.PreviewSource)
	require.Equal(t, string(TallyProgram), state.TallyState["cam1"], "cam1 tally is program (not preview+program)")
	// cam2 had destination-tally=program during transition; after abort it drops back to idle.
	require.NotEqual(t, string(TallyProgram), state.TallyState["cam2"], "cam2 no longer tagged program after abort")
}

// Jitter under 2×threshold should not trigger threshold-based abort — the
// engine still aborts at hard 0 (existing behavior), but the threshold path
// must not fire for tiny forward excursions.
func TestSwitcher_TBarJitterBelowMaxForwardGateNoThresholdAbort(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	// Default threshold 0.3; 2× = 0.6. Max-forward 0.5 is below the gate.
	err := sw.StartTransition(context.Background(), "cam2", "mix", 60000, "")
	require.NoError(t, err)

	require.NoError(t, sw.SetTransitionPosition(context.Background(), 0.5))
	// 0.1 < 0.3 but max-forward 0.5 < 0.6 so threshold abort must NOT fire.
	require.NoError(t, sw.SetTransitionPosition(context.Background(), 0.1))
	// Engine is still active; position reflects the pullback.
	state := sw.State()
	require.True(t, state.InTransition, "threshold abort must not trigger below the 2× max-forward gate")
	require.InDelta(t, 0.1, state.TransitionPosition, 1e-9)
}

func TestSwitcher_TBarTinyJitterDoesNotAbort(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	err := sw.StartTransition(context.Background(), "cam2", "mix", 60000, "")
	require.NoError(t, err)

	// 0.0 → 0.29 → 0.0: trivial jitter never crosses 2×threshold gate.
	// Engine's own hard-0 abort still fires at 0 (existing behavior) —
	// this test pins that threshold path is inert for jitter.
	require.NoError(t, sw.SetTransitionPosition(context.Background(), 0.29))
	// No abort yet.
	require.True(t, sw.State().InTransition)
}

func TestSwitcher_TBarForwardAfterAbortRequiresNewStartTransition(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	err := sw.StartTransition(context.Background(), "cam2", "mix", 60000, "")
	require.NoError(t, err)
	require.NoError(t, sw.SetTransitionPosition(context.Background(), 0.7))
	require.NoError(t, sw.SetTransitionPosition(context.Background(), 0.1))
	time.Sleep(20 * time.Millisecond)
	require.False(t, sw.State().InTransition, "precondition: threshold abort fired")

	// After abort, further T-bar motion must return ErrNoTransition —
	// the aborted engine is gone, requires a new StartTransition.
	err = sw.SetTransitionPosition(context.Background(), 0.5)
	require.True(t, errors.Is(err, ErrNoTransition), "forward motion post-abort must not spontaneously resume: %v", err)
	require.False(t, sw.State().InTransition)
}

// Starting a fresh transition resets max-forward tracking — abort threshold
// must compare against the NEW transition, not carry state from a prior one.
func TestSwitcher_MaxForwardResetsOnNewTransition(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()

	// First transition, drive forward then abort via threshold.
	require.NoError(t, sw.StartTransition(context.Background(), "cam2", "mix", 60000, ""))
	require.NoError(t, sw.SetTransitionPosition(context.Background(), 0.7))
	require.NoError(t, sw.SetTransitionPosition(context.Background(), 0.1))
	time.Sleep(20 * time.Millisecond)
	require.False(t, sw.State().InTransition)

	// Second transition. max-forward must start at 0 again: 0.5 → 0.1
	// should NOT fire threshold abort (0.5 < 0.6).
	require.NoError(t, sw.StartTransition(context.Background(), "cam2", "mix", 60000, ""))
	require.NoError(t, sw.SetTransitionPosition(context.Background(), 0.5))
	require.NoError(t, sw.SetTransitionPosition(context.Background(), 0.1))
	require.True(t, sw.State().InTransition, "new transition must not inherit prior max-forward")
}
