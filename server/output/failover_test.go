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

// viewOf is what an engine sees of peer through its health polls.
func viewOf(peer *Manager, leads bool) PeerView {
	return PeerView{
		Known:      true,
		Label:      peer.EngineLabel(),
		Sending:    peer.SendingDestinationIDs(),
		LocalLeads: leads,
		Grace:      time.Hour,
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

	v := PeerView{Known: true, Label: "a", Grace: 50 * time.Millisecond}
	b.ReconcileDestinations(v)
	requireState(t, b, "standby", "a")
	time.Sleep(60 * time.Millisecond)
	b.ReconcileDestinations(v)
	requireState(t, b, string(StateActive), "b")
}

// PUT /engine sent to both engines moves the sender.
func TestDestinationPair_ManualSwitchOver(t *testing.T) {
	a, b := pairEngine(t, "a"), pairEngine(t, "b")
	addBoth(t, a, b, true)

	for _, m := range []*Manager{a, b} {
		require.NoError(t, m.ReassignDestinationEngine(pairID, "b"))
	}
	requireState(t, a, "standby", "b")
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
