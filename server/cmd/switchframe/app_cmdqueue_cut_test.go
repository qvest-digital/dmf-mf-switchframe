package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"

	"github.com/zsiec/switchframe/server/cmdqueue"
	"github.com/zsiec/switchframe/server/switcher"
)

// A queued cut to the source on program reports that it set nothing, so
// the queue does not place it in the order on the program; one to another
// source does.
func TestQueuedCutToProgramSetsNothing(t *testing.T) {
	sw := switcher.NewTestSwitcher(t, distribution.NewRelay())
	for _, key := range []string{"cam1", "cam2"} {
		sw.RegisterSource(key, distribution.NewRelay())
	}
	require.NoError(t, sw.Cut(context.Background(), "cam1"))
	d := cmdqueue.NewDispatcher()
	(&App{sw: sw}).registerCoreActions(d)
	cut := func(src string) cmdqueue.Command {
		p, _ := json.Marshal(map[string]string{"source": src})
		return cmdqueue.Command{Action: "cut", Payload: p}
	}
	require.ErrorIs(t, d.Dispatch(cut("cam1")), cmdqueue.ErrUnchanged)
	require.NoError(t, d.Dispatch(cut("cam2")))
	require.Equal(t, "cam2", sw.State().ProgramSource)
}
