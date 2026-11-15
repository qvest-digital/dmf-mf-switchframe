package playout

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Close stops the watchdog, whose stall callback holds the manager, and
// closes the as-run logger: left running, each held its goroutine, and the
// watchdog the manager, its channels and all the app wired to them, for
// as long as the process ran. A stall the watchdog reports after Close
// recovers nothing: recovery restarts the channel.
func TestCloseStopsTheWatchdogAndAStallAfterItRecoversNothing(t *testing.T) {
	m := NewManager(ManagerConfig{AssetProvider: fakeProvider(500), AllowedLocalPrefixes: []string{"/"}})
	var recovered []error
	wd := NewWatchdog(WatchdogConfig{
		StallThreshold: time.Nanosecond,
		CheckInterval:  time.Hour,
		OnStall:        func(id int) { recovered = append(recovered, m.RecoverChannel(id)) },
	})
	wd.Start()
	m.SetWatchdog(wd)
	asRun, err := NewAsRunLogger(AsRunLoggerConfig{Dir: t.TempDir()})
	require.NoError(t, err)
	m.SetAsRunLogger(asRun)
	require.NoError(t, m.LoadPlaylist(1, testPlaylist(clipItem("a"))))

	m.Close()
	require.Nil(t, wd.done, "the watchdog runs on after Close")
	asRun.mu.Lock()
	open := asRun.writeCh != nil
	asRun.mu.Unlock()
	require.False(t, open, "the as-run logger is open after Close")

	// A stall the watchdog found as Close ran.
	wd.RegisterChannel(1)
	time.Sleep(time.Millisecond)
	wd.check()
	require.Equal(t, []error{errManagerClosed}, recovered)
	require.Equal(t, "stopped", m.ChannelStates()[0].State, "the stall restarted the channel")
}
