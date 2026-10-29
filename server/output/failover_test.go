package output

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// pairEngine is one engine of a pair whose SRT callers connect to nothing.
func pairEngine(t *testing.T, label string) *Manager {
	t.Helper()
	m := NewManager()
	t.Cleanup(func() { _ = m.Close() })
	m.SetEngineLabel(label)
	m.SetSRTWiring(
		func(context.Context, SRTCallerConfig) (srtConn, error) { return &mockSRTConn{}, nil },
		func(ctx context.Context, _ SRTListenerConfig, _ *SRTListener) error {
			<-ctx.Done()
			return nil
		},
	)
	return m
}

// viewOf is what an engine sees of peer through a health poll sent now.
func viewOf(peer *Manager, leads bool) PeerView {
	all, armed := peer.PairDestinationIDs()
	return PeerView{
		Known:        true,
		Label:        peer.EngineLabel(),
		Sending:      peer.SendingDestinationIDs(),
		Armed:        armed,
		Destinations: all,
		SeenPolledAt: time.Now(),
		Uptime:       time.Hour,
		LocalLeads:   leads,
		Grace:        time.Hour,
	}
}

var downPeer = PeerView{Known: true, Down: true, Label: "a"}

const pairID = "0a1b2c3d"

var callerConfig = DestinationConfig{Type: "srt-caller", Address: "192.0.2.1", Port: 9000}

func requireState(t *testing.T, m *Manager, want, engine string) {
	t.Helper()
	st, err := m.GetDestination(pairID)
	require.NoError(t, err)
	require.Equal(t, want, st.State, "engine %s", m.EngineLabel())
	require.Equal(t, engine, st.Config.Engine, "engine %s: sending engine", m.EngineLabel())
}

// addBoth creates the destination on both engines under one ID, as the UI
// does, and starts it on both.
func addBoth(t *testing.T, a, b *Manager, start bool) {
	t.Helper()
	for _, m := range []*Manager{a, b} {
		id, err := m.AddDestinationWithID(pairID, callerConfig)
		require.NoError(t, err)
		require.Equal(t, pairID, id)
		if start {
			require.NoError(t, m.StartDestination(pairID))
		}
	}
}

// Both engines store one config, and the pair sends from engine A alone; a
// default of the local label had each engine send its own copy.
func TestDestinationPair_OneSenderOneStandby(t *testing.T) {
	a, b := pairEngine(t, "a"), pairEngine(t, "b")
	addBoth(t, a, b, true)

	requireState(t, a, string(StateActive), "a")
	requireState(t, b, "standby", "a")
	require.Equal(t, []string{pairID}, a.SendingDestinationIDs())
	require.Empty(t, b.SendingDestinationIDs())

	// A second start on the standby is not an error either.
	require.NoError(t, b.StartDestination(pairID))
}

func TestDestinationPair_StopAndDeleteOnBoth(t *testing.T) {
	a, b := pairEngine(t, "a"), pairEngine(t, "b")
	addBoth(t, a, b, true)

	for _, m := range []*Manager{a, b} {
		require.NoError(t, m.StopDestination(pairID))
	}
	requireState(t, a, "stopped", "a")
	requireState(t, b, "stopped", "a")
	for _, m := range []*Manager{a, b} {
		require.ErrorIs(t, m.StopDestination(pairID), ErrDestinationStopped)
	}

	// A disarmed standby stays down when the sender goes away.
	b.ReconcileDestinations(downPeer)
	requireState(t, b, "stopped", "a")

	for _, m := range []*Manager{a, b} {
		require.NoError(t, m.RemoveDestination(pairID))
		require.Empty(t, m.ListDestinations())
	}
}

func TestDestinationPair_IDValidation(t *testing.T) {
	m := pairEngine(t, "a")
	_, err := m.AddDestinationWithID(pairID, callerConfig)
	require.NoError(t, err)
	_, err = m.AddDestinationWithID(pairID, callerConfig)
	require.ErrorIs(t, err, ErrDestinationExists)
	for _, bad := range []string{"0A1B2C3D", "0a1b2c3", "0a1b2c3d4", "../../x", "0a1b2c3g"} {
		_, err := m.AddDestinationWithID(bad, callerConfig)
		require.ErrorIs(t, err, ErrInvalidDestinationID, bad)
	}
}

// The standby sends once the sender is down, and only then.
func TestDestinationPair_FailoverWhenSenderDown(t *testing.T) {
	a, b := pairEngine(t, "a"), pairEngine(t, "b")
	addBoth(t, a, b, true)

	b.ReconcileDestinations(viewOf(a, false))
	requireState(t, b, "standby", "a")

	b.ReconcileDestinations(downPeer)
	requireState(t, b, string(StateActive), "b")
	require.Equal(t, []string{pairID}, b.SendingDestinationIDs())
}

// Engine A comes back without its state and takes the destinations over
// from B's snapshot: B sends, so A stays on standby.
func TestDestinationPair_ReturningSenderStaysStandby(t *testing.T) {
	b := pairEngine(t, "b")
	_, err := b.AddDestinationWithID(pairID, callerConfig)
	require.NoError(t, err)
	require.NoError(t, b.ApplyDestinationState(pairID, "b", true))
	requireState(t, b, string(StateActive), "b")

	a := pairEngine(t, "a")
	a.EnablePeerFailover()
	cfg := callerConfig
	cfg.Engine = "b"
	_, err = a.AddDestinationWithID(pairID, cfg)
	require.NoError(t, err)
	require.NoError(t, a.ApplyDestinationState(pairID, "b", true))
	a.ReconcileDestinations(viewOf(b, true))

	requireState(t, a, "standby", "b")
	requireState(t, b, string(StateActive), "b")
}

// A destination --outputs-config declares is armed on the returning engine
// as its own; once a poll shows the peer sending it, the returning engine
// hands it over instead of sending a second copy.
func TestDestinationPair_ReturningOwnerYieldsToPeerThatSends(t *testing.T) {
	b := pairEngine(t, "b")
	_, err := b.AddDestinationWithID(pairID, callerConfig)
	require.NoError(t, err)
	require.NoError(t, b.ApplyDestinationState(pairID, "b", true))

	a := pairEngine(t, "a")
	a.EnablePeerFailover()
	_, err = a.AddDestinationWithID(pairID, callerConfig)
	require.NoError(t, err)
	require.NoError(t, a.armDestination(pairID))

	a.ReconcileDestinations(PeerView{})
	requireState(t, a, "standby", "a") // no view of the peer yet

	a.ReconcileDestinations(viewOf(b, true))
	requireState(t, a, "standby", "b")

	// Without a peer sending it, the owner starts it.
	c := pairEngine(t, "a")
	c.EnablePeerFailover()
	_, err = c.AddDestinationWithID(pairID, callerConfig)
	require.NoError(t, err)
	require.NoError(t, c.armDestination(pairID))
	c.ReconcileDestinations(PeerView{Known: true, Label: "b", Grace: time.Hour})
	requireState(t, c, string(StateActive), "a")
}

// After a partition both engines send; the leader keeps sending, the other
// stands by.
func TestDestinationPair_PartitionHealsToOneSender(t *testing.T) {
	a, b := pairEngine(t, "a"), pairEngine(t, "b")
	addBoth(t, a, b, true)
	b.ReconcileDestinations(downPeer)
	requireState(t, a, string(StateActive), "a")
	requireState(t, b, string(StateActive), "b")

	va, vb := viewOf(b, true), viewOf(a, false)
	a.ReconcileDestinations(va)
	b.ReconcileDestinations(vb)

	requireState(t, a, string(StateActive), "a")
	requireState(t, b, "standby", "a")
}

// A reachable peer that owns an armed destination but does not send it, as
// after it lost its state, gives it up after the grace period.
func TestDestinationPair_TakeoverFromPeerNotSending(t *testing.T) {
	b := pairEngine(t, "b")
	_, err := b.AddDestinationWithID(pairID, callerConfig)
	require.NoError(t, err)
	require.NoError(t, b.StartDestination(pairID))

	v := PeerView{Known: true, Label: "a", Grace: 50 * time.Millisecond, SeenPolledAt: time.Now()}
	b.ReconcileDestinations(v)
	requireState(t, b, "standby", "a")
	time.Sleep(60 * time.Millisecond)
	v.SeenPolledAt = time.Now()
	b.ReconcileDestinations(v)
	requireState(t, b, string(StateActive), "b")
}

// PUT /engine sent to both engines moves the sender, and it stays moved
// through the failover ticks that follow.
func TestDestinationPair_ManualSwitchOver(t *testing.T) {
	a, b := pairEngine(t, "a"), pairEngine(t, "b")
	addBoth(t, a, b, true)

	for _, m := range []*Manager{a, b} {
		require.NoError(t, m.ReassignDestinationEngine(pairID, "b"))
	}
	requireState(t, a, "standby", "b")
	requireState(t, b, string(StateActive), "b")

	a.ReconcileDestinations(viewOf(b, true))
	b.ReconcileDestinations(viewOf(a, false))
	requireState(t, a, "standby", "b")
	requireState(t, b, string(StateActive), "b")

	// And back to the leader.
	for _, m := range []*Manager{a, b} {
		require.NoError(t, m.ReassignDestinationEngine(pairID, "a"))
	}
	a.ReconcileDestinations(viewOf(b, true))
	b.ReconcileDestinations(viewOf(a, false))
	requireState(t, a, string(StateActive), "a")
	requireState(t, b, "standby", "a")
}

// The failover tick runs every second, the peer poll every five: the first
// tick after a switch-over to the engine that does not lead still sees the
// old sender in its last poll. Yielding to it left both engines on standby,
// each naming the other, and both took over after the grace period.
func TestDestinationPair_SwitchOverIgnoresPollFromBefore(t *testing.T) {
	a, b := pairEngine(t, "a"), pairEngine(t, "b")
	addBoth(t, a, b, true)
	staleOfA := viewOf(a, false) // B's last poll: A sends
	staleOfB := viewOf(b, true)  // A's last poll: B does not send
	for _, m := range []*Manager{a, b} {
		require.NoError(t, m.ReassignDestinationEngine(pairID, "b"))
	}

	b.ReconcileDestinations(staleOfA)
	a.ReconcileDestinations(staleOfB)
	requireState(t, b, string(StateActive), "b")
	requireState(t, a, "standby", "b")

	// Fresh polls past the grace period: still one sender.
	va, vb := viewOf(a, false), viewOf(b, true)
	va.Grace, vb.Grace = 20*time.Millisecond, 20*time.Millisecond
	for range 2 {
		a.ReconcileDestinations(vb)
		b.ReconcileDestinations(va)
		time.Sleep(30 * time.Millisecond)
		va.SeenPolledAt, vb.SeenPolledAt = time.Now(), time.Now()
	}
	requireState(t, b, string(StateActive), "b")
	requireState(t, a, "standby", "b")
	require.Empty(t, a.SendingDestinationIDs())
}

// A stop the standby missed: the peer process had the destination armed
// and no longer does, so the standby disarms it instead of taking it over
// as an owner that lost its state.
func TestDestinationPair_StopMissedByStandby(t *testing.T) {
	a, b := pairEngine(t, "a"), pairEngine(t, "b")
	addBoth(t, a, b, true)
	b.ReconcileDestinations(viewOf(a, false))

	require.NoError(t, a.StopDestination(pairID)) // B misses it
	v := viewOf(a, false)
	v.Grace = 20 * time.Millisecond
	b.ReconcileDestinations(v)
	time.Sleep(30 * time.Millisecond)
	v.SeenPolledAt = time.Now()
	b.ReconcileDestinations(v)

	requireState(t, b, "stopped", "a")
	require.Empty(t, b.SendingDestinationIDs())
}

// A stop the standby missed before any poll showed it the destination
// armed on the peer: the peer still holds it, stopped, so it did not lose
// its state, and the standby must not start it.
func TestDestinationPair_StopMissedBeforeStandbySawItArmed(t *testing.T) {
	a, b := pairEngine(t, "a"), pairEngine(t, "b")
	addBoth(t, a, b, true)

	require.NoError(t, a.StopDestination(pairID)) // B misses it
	v := viewOf(a, false)
	v.Grace = 20 * time.Millisecond
	b.ReconcileDestinations(v)
	time.Sleep(30 * time.Millisecond)
	v.SeenPolledAt = time.Now()
	b.ReconcileDestinations(v)

	requireState(t, b, "stopped", "a")
	require.Empty(t, b.SendingDestinationIDs())

	// Within the skew of B's own start the stopped peer may be one whose
	// start has not landed yet: B neither disarms nor takes it over.
	a, b = pairEngine(t, "a"), pairEngine(t, "b")
	addBoth(t, a, b, true)
	require.NoError(t, a.StopDestination(pairID))
	v = viewOf(a, false)
	v.Grace, v.Skew = 20*time.Millisecond, time.Hour
	b.ReconcileDestinations(v)
	time.Sleep(30 * time.Millisecond)
	v.SeenPolledAt = time.Now()
	b.ReconcileDestinations(v)
	requireState(t, b, "standby", "a")
}

// The UI sends a switch-over to both engines, and the legs may land apart:
// a poll the new sender sent after its own move but before the old sender
// moved still shows the old sender sending. Yielding to it left both
// engines on standby.
func TestDestinationPair_SwitchOverLegsLandApart(t *testing.T) {
	a, b := pairEngine(t, "a"), pairEngine(t, "b")
	addBoth(t, a, b, true)

	require.NoError(t, b.ReassignDestinationEngine(pairID, "b"))
	between := viewOf(a, false) // A has not moved yet
	between.Skew = time.Second
	require.NoError(t, a.ReassignDestinationEngine(pairID, "b"))

	b.ReconcileDestinations(between)
	requireState(t, b, string(StateActive), "b")
	requireState(t, a, "standby", "b")

	// A peer still sending once the skew has passed is answered.
	time.Sleep(10 * time.Millisecond)
	later := between
	later.SeenPolledAt, later.Skew = time.Now(), 5*time.Millisecond
	b.ReconcileDestinations(later)
	requireState(t, b, "standby", "a")
}

// A delete the standby missed deletes it on the standby too.
func TestDestinationPair_DeleteMissedByStandby(t *testing.T) {
	a, b := pairEngine(t, "a"), pairEngine(t, "b")
	addBoth(t, a, b, true)
	b.ReconcileDestinations(viewOf(a, false))

	require.NoError(t, a.RemoveDestination(pairID)) // B misses it
	b.ReconcileDestinations(viewOf(a, false))

	_, err := b.GetDestination(pairID)
	require.ErrorIs(t, err, ErrDestinationNotFound)
}

// A poll taken before a stop and a new start on both engines shows the peer
// disarmed, but answers the stop, not the start: the standby stays armed.
func TestDestinationPair_RestartNotUndoneByPollFromBefore(t *testing.T) {
	a, b := pairEngine(t, "a"), pairEngine(t, "b")
	addBoth(t, a, b, true)
	b.ReconcileDestinations(viewOf(a, false))
	for _, m := range []*Manager{a, b} {
		require.NoError(t, m.StopDestination(pairID))
	}
	between := viewOf(a, false)
	for _, m := range []*Manager{a, b} {
		require.NoError(t, m.StartDestination(pairID))
	}

	b.ReconcileDestinations(between)
	requireState(t, b, "standby", "a")
	requireState(t, a, string(StateActive), "a")
}

// A peer that restarted without its state never had the destination armed
// in this process: the standby takes it over after the grace period.
func TestDestinationPair_TakeoverFromRestartedPeer(t *testing.T) {
	a, b := pairEngine(t, "a"), pairEngine(t, "b")
	addBoth(t, a, b, true)
	b.ReconcileDestinations(viewOf(a, false))

	fresh := pairEngine(t, "a") // A restarted, empty
	v := viewOf(fresh, false)
	v.Uptime = time.Second
	v.Grace = 20 * time.Millisecond
	b.ReconcileDestinations(v)
	requireState(t, b, "standby", "a")
	time.Sleep(30 * time.Millisecond)
	v.SeenPolledAt = time.Now()
	b.ReconcileDestinations(v)
	requireState(t, b, string(StateActive), "b")
}

// An engine back within a poll of its peer's takeover applies a snapshot the
// peer took before it: it names itself the sender, and its last poll does
// not show the peer sending yet. A fresh read of the peer right before the
// start does, and keeps it on standby.
func TestDestinationPair_ReturningEngineReadsPeerBeforeStart(t *testing.T) {
	b := pairEngine(t, "b")
	_, err := b.AddDestinationWithID(pairID, callerConfig)
	require.NoError(t, err)
	require.NoError(t, b.ApplyDestinationState(pairID, "a", true))
	staleOfB := viewOf(b, false) // before B's takeover
	b.ReconcileDestinations(downPeer)
	requireState(t, b, string(StateActive), "b")

	a := pairEngine(t, "a")
	a.EnablePeerFailover()
	a.SetPeerProbe(func() (PeerView, bool) { return viewOf(b, false), true })
	_, err = a.AddDestinationWithID(pairID, callerConfig)
	require.NoError(t, err)
	require.NoError(t, a.ApplyDestinationState(pairID, "a", true)) // pre-takeover snapshot
	a.ReconcileDestinations(staleOfB)

	requireState(t, a, "standby", "b")
	require.Empty(t, a.SendingDestinationIDs())
}

// An operator's switch-over starts at once, without a read of the peer: the
// peer may not have stopped yet.
func TestDestinationPair_SwitchOverDoesNotReadPeer(t *testing.T) {
	a, b := pairEngine(t, "a"), pairEngine(t, "b")
	addBoth(t, a, b, true)
	b.SetPeerProbe(func() (PeerView, bool) { return viewOf(a, false), true })
	require.NoError(t, b.ReassignDestinationEngine(pairID, "b"))
	requireState(t, b, string(StateActive), "b")
}

// Each engine listens on its own address: a listener has no standby.
func TestDestinationPair_ListenerOnBoth(t *testing.T) {
	a, b := pairEngine(t, "a"), pairEngine(t, "b")
	for _, m := range []*Manager{a, b} {
		_, err := m.AddDestinationWithID(pairID, DestinationConfig{Type: "srt-listener", Port: 9001})
		require.NoError(t, err)
		require.NoError(t, m.StartDestination(pairID))
		st, err := m.GetDestination(pairID)
		require.NoError(t, err)
		require.NotEqual(t, "standby", st.State)
		require.True(t, st.Armed)
	}
	require.Empty(t, b.SendingDestinationIDs())
}

// Engines whose files list the destinations in another order, as during a
// rolling update, still store each target under one ID.
func TestApplyStartup_IDFollowsTargetNotPosition(t *testing.T) {
	x := StartupDestination{DestinationConfig: callerConfig}
	y := StartupDestination{DestinationConfig: DestinationConfig{Type: "rtmp", URL: "rtmp://192.0.2.2/live/key"}}
	a, b := pairEngine(t, "a"), pairEngine(t, "b")
	// Armed only, left to the failover tick: an RTMP adapter needs FFmpeg.
	a.EnablePeerFailover()
	b.EnablePeerFailover()
	require.NoError(t, a.ApplyStartup(StartupConfig{Destinations: []StartupDestination{x, y}}))
	require.NoError(t, b.ApplyStartup(StartupConfig{Destinations: []StartupDestination{y, x}}))

	byTarget := func(m *Manager) map[string]string {
		out := map[string]string{}
		for _, d := range m.ListDestinations() {
			out[d.Config.Type+d.Config.URL+d.Config.Address] = d.ID
		}
		return out
	}
	require.Equal(t, byTarget(a), byTarget(b))
	require.Len(t, byTarget(a), 2)

	// One target twice is refused by name, not as a clash of IDs.
	err := pairEngine(t, "a").ApplyStartup(StartupConfig{Destinations: []StartupDestination{x, x}})
	require.ErrorContains(t, err, "same type, target and stream ID")

	// Another stream ID on one target is another destination.
	z := x
	z.StreamID = "other"
	require.NoError(t, pairEngine(t, "a").ApplyStartup(StartupConfig{Destinations: []StartupDestination{x, z}}))
}

// Both engines reading one startup file store its destinations under one ID.
func TestApplyStartup_SharedIDsAndDeferredStart(t *testing.T) {
	cfg := StartupConfig{Destinations: []StartupDestination{{DestinationConfig: callerConfig}}}
	a, b := pairEngine(t, "a"), pairEngine(t, "b")
	a.EnablePeerFailover()
	b.EnablePeerFailover()
	require.NoError(t, a.ApplyStartup(cfg))
	require.NoError(t, b.ApplyStartup(cfg))

	da, db := a.ListDestinations(), b.ListDestinations()
	require.Len(t, da, 1)
	require.Len(t, db, 1)
	require.Equal(t, da[0].ID, db[0].ID)
	require.Equal(t, "standby", da[0].State, "started before the peer is known")

	a.ReconcileDestinations(viewOf(b, true))
	require.Equal(t, []string{da[0].ID}, a.SendingDestinationIDs())
}
