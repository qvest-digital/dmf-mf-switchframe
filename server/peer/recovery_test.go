package peer

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestRecovery_PeerWithHigherEpochWinsYieldsLocal simulates the
// "force-takeover-during-partition then heal" flow: engine B is promoted
// while A is unreachable, then A comes back online. A's poller sees B's
// higher epoch and Role() returns RoleFollowerHealthy.
func TestRecovery_PeerWithHigherEpochWinsYieldsLocal(t *testing.T) {
	// Simulated peer (engine B) responds with its own identity + a
	// forced-takeover epoch of 3.
	var serverEpoch atomic.Uint64
	serverEpoch.Store(3)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := HealthResponse{
			Status:      "ok",
			EngineLabel: "b",
			Epoch:       serverEpoch.Load(),
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	// Local engine A has never been force-leadered (epoch 0).
	p := NewHealthPoller(srv.URL,
		WithLocalIdentity("a"),
		WithPollInterval(50*time.Millisecond),
		WithFailureThreshold(2),
	)
	p.Start()
	defer p.Stop()

	// Wait for at least one successful poll.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if p.PeerLastSeen().IsZero() {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		break
	}
	if p.PeerLastSeen().IsZero() {
		t.Fatal("poller never recorded a successful peer poll")
	}

	// Confirm state: peerEpoch=3 > localEpoch=0 → follower-healthy
	// (yielding, even though "a" would normally win lexical tiebreak).
	if got := p.Role(); got != RoleFollowerHealthy {
		t.Errorf("Role() = %v, want RoleFollowerHealthy (peer has higher epoch)", got)
	}
	if got := p.LocalEpoch(); got != 0 {
		t.Errorf("LocalEpoch = %d, want 0 (we never bumped)", got)
	}
}

// TestRecovery_BumpEpochOvertakesPeer exercises the opposite case: local
// epoch is bumped during partition, peer returns with a lower epoch, and
// we hold leader.
func TestRecovery_BumpEpochOvertakesPeer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := HealthResponse{
			Status:      "ok",
			EngineLabel: "a",
			Epoch:       1,
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := NewHealthPoller(srv.URL,
		WithLocalIdentity("b"),
		WithPollInterval(50*time.Millisecond),
	)
	p.BumpEpoch() // 1
	p.BumpEpoch() // 2 — now > peer's 1
	p.Start()
	defer p.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !p.PeerLastSeen().IsZero() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if got := p.Role(); got != RoleLeader {
		t.Errorf("Role() = %v, want RoleLeader (localEpoch 2 > peerEpoch 1)", got)
	}
}
