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
