package peer

import "testing"

func TestRole_Matrix(t *testing.T) {
	type input struct {
		localLabel    string
		localEpoch    uint64
		peerReachable bool
		peerLabel     string
		peerEpoch     uint64
	}
	cases := []struct {
		name string
		in   input
		want Role
	}{
		{"standalone empty label", input{localLabel: ""}, RoleStandalone},

		// Healthy active-active — label tiebreak, equal epochs.
		{"A reachable B equal epoch → A leads", input{"a", 0, true, "b", 0}, RoleLeader},
		{"B reachable A equal epoch → B follows", input{"b", 0, true, "a", 0}, RoleFollowerHealthy},

		// Epoch dominates label.
		{"B with higher epoch beats A label-win", input{"b", 5, true, "a", 2}, RoleLeader},
		{"A with lower epoch yields to B", input{"a", 2, true, "b", 5}, RoleFollowerHealthy},

		// Partition.
		{"A partitioned, equal epochs → A leads (label win)", input{"a", 0, false, "b", 0}, RoleLeader},
		{"B partitioned, equal epochs → B follows", input{"b", 0, false, "a", 0}, RoleFollowerPartitioned},

		// Partition + stale epoch carried over from prior reachable window.
		{"Forced-demoted A under partition yields", input{"a", 1, false, "b", 2}, RoleFollowerPartitioned},
		{"Forced-promoted B under partition holds", input{"b", 3, false, "a", 1}, RoleLeader},

		// Startup-before-first-poll: peer never reachable, peerLabel empty,
		// peerEpoch zero. We don't yet know who we're up against, so we
		// conservatively pause non-idempotent work until we confirm.
		{"A never heard from peer → conservative follower", input{"a", 0, false, "", 0}, RoleFollowerPartitioned},
		{"B never heard from peer → conservative follower", input{"b", 0, false, "", 0}, RoleFollowerPartitioned},

		// Local epoch bump while peer unreachable (force-takeover before
		// recovery) correctly overrides the conservative default.
		{"A forced leader before ever seeing peer", input{"a", 1, false, "", 0}, RoleLeader},

		// Misconfiguration: both engines booted with the same label.
		// Tiebreak collapses to the !<  branch → both become followers
		// under reachable pairing (safe), and partitioned-follower on
		// split (still safe — no dual-leader). Documented so a
		// future refactor doesn't silently change the direction.
		{"both labeled a, reachable → dual-follower", input{"a", 0, true, "a", 0}, RoleFollowerHealthy},
		{"both labeled b, partitioned → dual-follower", input{"b", 0, false, "b", 0}, RoleFollowerPartitioned},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := NewHealthPoller("http://peer", WithLocalIdentity(tc.in.localLabel))
			p.SetPeerStateForTest(tc.in.peerReachable, tc.in.peerLabel, tc.in.peerEpoch)
			p.localEpoch.Store(tc.in.localEpoch)

			if got := p.Role(); got != tc.want {
				t.Errorf("Role() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRole_NilPollerIsStandalone(t *testing.T) {
	var p *HealthPoller
	if got := p.Role(); got != RoleStandalone {
		t.Errorf("nil.Role() = %v, want RoleStandalone", got)
	}
}

func TestLocalEpoch_BumpIncrements(t *testing.T) {
	p := NewHealthPoller("http://peer", WithLocalIdentity("a"))
	if got := p.LocalEpoch(); got != 0 {
		t.Fatalf("initial LocalEpoch = %d, want 0", got)
	}
	if got := p.BumpEpoch(); got != 1 {
		t.Errorf("first BumpEpoch = %d, want 1", got)
	}
	if got := p.BumpEpoch(); got != 2 {
		t.Errorf("second BumpEpoch = %d, want 2", got)
	}
	if got := p.LocalEpoch(); got != 2 {
		t.Errorf("LocalEpoch after two bumps = %d, want 2", got)
	}
}

func TestLocalIdentity_EmptyMeansStandalone(t *testing.T) {
	// Even with peer reachable and epoch bumped, an unlabeled engine stays
	// in standalone mode — the label is the enabler for peer-role logic.
	p := NewHealthPoller("http://peer")
	p.BumpEpoch()
	p.SetPeerStateForTest(true, "b", 0)
	if got := p.Role(); got != RoleStandalone {
		t.Errorf("Role() with empty label = %v, want RoleStandalone", got)
	}
}
