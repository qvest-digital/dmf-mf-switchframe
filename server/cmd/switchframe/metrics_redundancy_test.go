package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/internal/metricstest"
	"github.com/zsiec/switchframe/server/nmos"
	"github.com/zsiec/switchframe/server/peer"
	"github.com/zsiec/switchframe/server/switcher"
)

// A single engine reports itself standalone and nothing about a peer.
func TestRedundancyCollector_Standalone(t *testing.T) {
	f := metricstest.Series(t, &redundancyCollector{a: &App{}})
	require.Equal(t, map[string]float64{"standalone": 1, "leader": 0, "follower": 0, "follower_partitioned": 0},
		f["switchframe_peer_role"])
	require.NotContains(t, f, "switchframe_peer_reachable")
	require.NotContains(t, f, "switchframe_nmos_registered")
}

// peerServing returns a peer engine answering health polls with program
// as its program source.
func peerServing(t *testing.T, program string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(peer.HealthResponse{Status: "ok", ProgramSource: program, EngineLabel: "b"})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// The program agreement compares what the peer last reported with this
// engine's own program: the signal that the pair has split.
func TestRedundancyCollector_ProgramAgreement(t *testing.T) {
	for _, tt := range []struct {
		peerProgram string
		agrees      float64
	}{{"cam1", 1}, {"cam2", 0}} {
		t.Run(tt.peerProgram, func(t *testing.T) {
			sw := switcher.New(distribution.NewRelay())
			defer sw.Close()
			sw.RegisterSource("cam1", distribution.NewRelay())
			sw.RegisterSource("cam2", distribution.NewRelay())
			require.NoError(t, sw.Cut(context.Background(), "cam1"))

			p := peer.NewHealthPoller(peerServing(t, tt.peerProgram),
				peer.WithLocalIdentity("a"), peer.WithPollInterval(10*time.Millisecond))
			p.Start()
			defer p.Stop()
			require.Eventually(t, p.PeerReachable, 2*time.Second, 10*time.Millisecond)

			f := metricstest.Series(t, &redundancyCollector{a: &App{sw: sw, peerPoller: p}})
			require.Equal(t, map[string]float64{"": tt.agrees}, f["switchframe_peer_program_agrees"])
			require.Equal(t, map[string]float64{"": 1}, f["switchframe_peer_reachable"])
			require.Equal(t, float64(1), f["switchframe_peer_role"]["leader"], "a wins the tiebreak over b")
		})
	}
}

// A partitioned follower is what gates non-idempotent commands; it reads
// as its own role.
func TestRedundancyCollector_PartitionedFollower(t *testing.T) {
	p := peer.NewHealthPoller("", peer.WithLocalIdentity("b"))
	p.SetPeerStateForTest(false, "a", 0)
	f := metricstest.Series(t, &redundancyCollector{a: &App{peerPoller: p}})
	require.Equal(t, float64(1), f["switchframe_peer_role"]["follower_partitioned"])
	require.Equal(t, map[string]float64{"": 0}, f["switchframe_peer_reachable"])
	require.NotContains(t, f, "switchframe_peer_program_agrees", "nothing to compare with an unreachable peer")
}

// The NMOS counters follow the registration loop and the activation
// handler, by result.
func TestRedundancyCollector_NMOS(t *testing.T) {
	n := &mxlNMOS{reg: nmos.NewRegistrationClient("http://registry.invalid", "node")}
	n.registered = true
	n.registrations.Add(2)
	n.heartbeatErrors.Add(1)
	n.activations.Add(3)
	n.lastHeartbeat.Store(time.Now().Add(-4 * time.Second).UnixNano())

	f := metricstest.Series(t, &redundancyCollector{a: &App{mxlNMOS: n}})
	require.Equal(t, map[string]float64{"": 1}, f["switchframe_nmos_registered"])
	require.Equal(t, map[string]float64{"ok": 2, "error": 0}, f["switchframe_nmos_registrations_total"])
	require.Equal(t, map[string]float64{"ok": 0, "error": 1}, f["switchframe_nmos_heartbeats_total"])
	require.Equal(t, map[string]float64{"ok": 3, "error": 0}, f["switchframe_nmos_activations_total"])
	require.InDelta(t, 4, f["switchframe_nmos_last_heartbeat_age_seconds"][""], 0.5)
}

// An activation the engine refuses is counted as an error where it
// happens, not left to the log.
func TestActivateMXLReceiver_CountsRefusals(t *testing.T) {
	a := &App{mxlNMOS: &mxlNMOS{receivers: map[string]mxlReceiverRef{}}}
	require.Error(t, a.activateMXLReceiver(context.Background(), "unknown", nmos.MXLEndpoint{}))
	require.Equal(t, int64(1), a.mxlNMOS.activationErrors.Load())
	require.Zero(t, a.mxlNMOS.activations.Load())
}
