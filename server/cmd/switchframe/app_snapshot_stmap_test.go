package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/internal"
	"github.com/zsiec/switchframe/server/stmap"
)

// stmapOnlyApp is an App with only an ST map registry; the apply steps
// that need the switcher or the mixer do nothing.
type stmapOnlyApp struct{ *App }

func (stmapOnlyApp) SetFTB(bool) error                 { return nil }
func (stmapOnlyApp) SetMasterLevel(float64) error      { return nil }
func (stmapOnlyApp) SetLimiterThreshold(float64) error { return nil }
func (stmapOnlyApp) SetLimiterCeiling(float64) error   { return nil }

// A snapshot takes the peer's ST map assignments as a whole: one the peer
// does not hold goes, here, on the real registry, and the program map with
// it when the peer has none.
func TestApplyFullSnapshot_STMapAssignmentsOnTheApp(t *testing.T) {
	reg := stmap.NewRegistry()
	for _, name := range []string{"barrel", "fisheye"} {
		m := stmap.Identity(4, 4)
		m.Name = name
		require.NoError(t, reg.Store(m))
	}
	require.NoError(t, reg.AssignSource("cam1", "barrel"))
	require.NoError(t, reg.AssignSource("cam2", "fisheye"))
	require.NoError(t, reg.AssignProgram("fisheye"))
	app := &App{stmapRegistry: reg}
	require.ElementsMatch(t, []string{"cam1", "cam2"}, app.STMapSources())

	snap := internal.FullStateSnapshot{STMaps: &internal.STMapSnapshot{Sources: map[string]string{"cam1": "barrel"}}}
	require.Empty(t, internal.ApplyFullSnapshot(context.Background(), snap, stmapOnlyApp{app}))

	state := reg.State()
	require.Equal(t, map[string]string{"cam1": "barrel"}, state.Sources)
	require.Nil(t, state.Program)

	// Nothing assigned: nothing to remove, and no error.
	require.Empty(t, internal.ApplyFullSnapshot(context.Background(), internal.FullStateSnapshot{}, stmapOnlyApp{app}))
	require.Empty(t, reg.State().Sources)
	require.NoError(t, app.RemoveSTMapProgram())
}
