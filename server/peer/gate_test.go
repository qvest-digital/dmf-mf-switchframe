package peer

import (
	"errors"
	"testing"
)

func TestGate_NilPollerAllows(t *testing.T) {
	var p *HealthPoller
	if err := p.Gate(); err != nil {
		t.Errorf("nil.Gate() = %v, want nil", err)
	}
}

func TestGate_StandaloneAllows(t *testing.T) {
	p := NewHealthPoller("http://peer") // no identity → standalone
	if err := p.Gate(); err != nil {
		t.Errorf("standalone Gate() = %v, want nil", err)
	}
}

func TestGate_LeaderAllows(t *testing.T) {
	p := NewHealthPoller("http://peer", WithLocalIdentity("a"))
	p.SetPeerStateForTest(true, "b", 0)
	if err := p.Gate(); err != nil {
		t.Errorf("leader Gate() = %v, want nil", err)
	}
}

func TestGate_FollowerHealthyAllows(t *testing.T) {
	// During healthy active-active both engines run commands; dedup at
	// the output layer via BUG-001's engine-label guard.
	p := NewHealthPoller("http://peer", WithLocalIdentity("b"))
	p.SetPeerStateForTest(true, "a", 0)
	if err := p.Gate(); err != nil {
		t.Errorf("follower-healthy Gate() = %v, want nil", err)
	}
}

func TestGate_FollowerPartitionedBlocks(t *testing.T) {
	p := NewHealthPoller("http://peer", WithLocalIdentity("b"))
	p.SetPeerStateForTest(false, "a", 0)
	err := p.Gate()
	if !errors.Is(err, ErrGatedByFollower) {
		t.Errorf("follower-partitioned Gate() = %v, want ErrGatedByFollower", err)
	}
}

func TestGate_UnreachablePeerNeverKnownBlocks(t *testing.T) {
	// Startup-before-first-poll: peerLabel unknown → conservative follower.
	p := NewHealthPoller("http://peer", WithLocalIdentity("a"))
	// Never called SetPeerStateForTest — poller state is fresh.
	err := p.Gate()
	if !errors.Is(err, ErrGatedByFollower) {
		t.Errorf("cold-start Gate() = %v, want ErrGatedByFollower", err)
	}
}

func TestGate_ForcedLeaderOverridesPartition(t *testing.T) {
	p := NewHealthPoller("http://peer", WithLocalIdentity("b"))
	p.SetPeerStateForTest(false, "a", 0)
	p.BumpEpoch() // local epoch now 1 > peerEpoch 0 → leader
	if err := p.Gate(); err != nil {
		t.Errorf("forced-leader Gate() = %v, want nil", err)
	}
}

func TestBumpEpoch_InvokesPersistCallback(t *testing.T) {
	var persisted []uint64
	persist := func(e uint64) error {
		persisted = append(persisted, e)
		return nil
	}
	p := NewHealthPoller("http://peer",
		WithLocalIdentity("a"),
		WithEpochPersist(persist),
	)

	p.BumpEpoch()
	p.BumpEpoch()

	if len(persisted) != 2 {
		t.Fatalf("persist called %d times, want 2", len(persisted))
	}
	if persisted[0] != 1 || persisted[1] != 2 {
		t.Errorf("persist values = %v, want [1 2]", persisted)
	}
}

func TestBumpEpoch_PersistErrorIsLoggedNotFatal(t *testing.T) {
	// Persistence errors must not prevent the in-memory epoch from
	// incrementing — force-leader must succeed even if disk I/O fails,
	// since the in-memory state is still authoritative for this process.
	persist := func(uint64) error { return errors.New("disk full") }
	p := NewHealthPoller("http://peer",
		WithLocalIdentity("a"),
		WithEpochPersist(persist),
	)

	newEpoch := p.BumpEpoch()
	if newEpoch != 1 {
		t.Errorf("BumpEpoch returned %d, want 1 even though persist errored", newEpoch)
	}
	if got := p.LocalEpoch(); got != 1 {
		t.Errorf("LocalEpoch = %d, want 1", got)
	}
}
