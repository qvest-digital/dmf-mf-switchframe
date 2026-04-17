package peer

import "errors"

// ErrGatedByFollower is returned by Gate when the local engine is a
// partitioned follower. REST handlers should map this to HTTP 409 so the
// browser can re-route the request to the peer (leader).
//
// Gate intentionally does not distinguish between "partitioned follower with
// known peer" and "partitioned follower that has never successfully polled
// the peer" — both are conservative-follower states, both block for the
// same reason (we can't confirm this engine is the lexical winner).
var ErrGatedByFollower = errors.New("peer: action gated — local engine is partitioned follower")

// Gate returns ErrGatedByFollower when the local role is
// RoleFollowerPartitioned, nil otherwise. Callers use this to decide
// whether a state-mutating REST request should proceed.
//
// Only the *partitioned* follower is blocked — healthy active-active
// commands run on both engines because the output-layer engine-label
// guard (BUG-001) deduplicates what reaches downstream receivers.
//
// A nil receiver returns nil — tests and single-engine deployments that
// never construct a poller do not gate.
func (p *HealthPoller) Gate() error {
	if p.Role() == RoleFollowerPartitioned {
		return ErrGatedByFollower
	}
	return nil
}
