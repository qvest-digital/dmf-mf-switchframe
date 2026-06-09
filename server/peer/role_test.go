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

		// Misconfiguration: both engines booted with the same label AND no
		// instance ID configured (the backward-compatible fail-safe). With no
		// secondary tiebreak key the lexical-label branch collapses → both
		// become followers under reachable pairing (safe), and partitioned-
		// follower on split (still safe — no dual-leader). The instance-ID
		// tiebreak that rescues leadership is exercised in
		// TestRole_SameLabelInstanceIDTiebreak below.
		{"both labeled a, no instance ID, reachable → dual-follower", input{"a", 0, true, "a", 0}, RoleFollowerHealthy},
		{"both labeled b, no instance ID, partitioned → dual-follower", input{"b", 0, false, "b", 0}, RoleFollowerPartitioned},
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

// TestRole_SameLabelInstanceIDTiebreak verifies the regression fix for the
// same-label collapse: when two engines share an --engine-label and have equal
// epochs, a distinct per-process instance ID breaks the tie deterministically
// so exactly one engine leads instead of both silently becoming followers.
func TestRole_SameLabelInstanceIDTiebreak(t *testing.T) {
	cases := []struct {
		name           string
		localLabel     string
		localInstance  string
		peerReachable  bool
		peerLabel      string
		peerInstance   string
		want           Role
	}{
		// Reachable, same label, distinct instance IDs: lexically-smaller leads.
		{"same label, local instance smaller, reachable → leader",
			"a", "inst-1", true, "a", "inst-2", RoleLeader},
		{"same label, local instance larger, reachable → follower-healthy",
			"a", "inst-2", true, "a", "inst-1", RoleFollowerHealthy},

		// Partitioned, same label, distinct instance IDs: lexically-smaller
		// leads (no dual-leader because the peer reaches the opposite verdict).
		{"same label, local instance smaller, partitioned → leader",
			"b", "aaa", false, "b", "bbb", RoleLeader},
		{"same label, local instance larger, partitioned → follower-partitioned",
			"b", "bbb", false, "b", "aaa", RoleFollowerPartitioned},

		// Identical instance IDs (e.g. both default to the same fallback):
		// no tiebreak, fail-safe both-follower preserved.
		{"same label, identical instance, reachable → follower-healthy",
			"a", "same", true, "a", "same", RoleFollowerHealthy},

		// Peer not yet publishing an instance ID: fail-safe preserved even
		// though local has one.
		{"same label, peer instance empty, reachable → follower-healthy",
			"a", "inst-1", true, "a", "", RoleFollowerHealthy},
		{"same label, local instance empty, partitioned → follower-partitioned",
			"b", "", false, "b", "inst-1", RoleFollowerPartitioned},

		// Distinct labels: instance ID is irrelevant, label still wins.
		{"distinct labels ignore instance IDs → label wins",
			"a", "zzz", true, "b", "aaa", RoleLeader},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := NewHealthPoller("http://peer",
				WithLocalIdentity(tc.localLabel),
				WithLocalInstanceID(tc.localInstance))
			p.SetPeerStateForTest(tc.peerReachable, tc.peerLabel, 0)
			p.SetPeerInstanceIDForTest(tc.peerInstance)

			if got := p.Role(); got != tc.want {
				t.Errorf("Role() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRole_SameLabelTiebreakNeverDualLeader is a mirror test: from the same
// inputs, the two engines must reach complementary verdicts so leadership is
// held by exactly one side (never both leader, never both follower) whenever
// instance IDs are distinct.
func TestRole_SameLabelTiebreakNeverDualLeader(t *testing.T) {
	mkPoller := func(instance, peerInstance string, reachable bool) *HealthPoller {
		p := NewHealthPoller("http://peer",
			WithLocalIdentity("x"),
			WithLocalInstanceID(instance))
		p.SetPeerStateForTest(reachable, "x", 0)
		p.SetPeerInstanceIDForTest(peerInstance)
		return p
	}

	for _, reachable := range []bool{true, false} {
		engineA := mkPoller("uuid-aaaa", "uuid-bbbb", reachable)
		engineB := mkPoller("uuid-bbbb", "uuid-aaaa", reachable)

		roleA := engineA.Role()
		roleB := engineB.Role()

		aLeads := roleA == RoleLeader
		bLeads := roleB == RoleLeader
		if aLeads == bLeads {
			t.Errorf("reachable=%v: expected exactly one leader, got A=%v B=%v",
				reachable, roleA, roleB)
		}
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
