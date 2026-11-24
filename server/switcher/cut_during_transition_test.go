package switcher

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// CUT pressed during an AUTO cuts to the preview, which the transition
// set to the old program: a cut to the source on program, it leaves the
// transition running to its target.
func TestCutToProgramDuringTransitionLeavesItRunning(t *testing.T) {
	sw, _ := setupSwitcherWithTransition(t)
	defer sw.Close()
	ctx := context.Background()

	require.NoError(t, sw.StartTransition(ctx, "cam2", "mix", 60000, ""))
	st := sw.State()
	require.Equal(t, "cam1", st.PreviewSource, "the transition puts the old program on preview")

	require.NoError(t, sw.Cut(ctx, st.PreviewSource))
	require.True(t, sw.State().InTransition, "CUT aborted the transition")

	require.NoError(t, sw.SetTransitionPosition(ctx, 1.0))
	time.Sleep(20 * time.Millisecond)
	st = sw.State()
	require.False(t, st.InTransition)
	require.Equal(t, "cam2", st.ProgramSource)
}
