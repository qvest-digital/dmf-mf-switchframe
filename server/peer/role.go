package peer

// Role reflects this engine's authority for non-idempotent commands in an
// active-active pair. It is recomputed on every peer-health poll.
//
// Tiebreak rules:
//   - Local epoch > peer epoch: this engine is leader (recently forced).
//   - Peer epoch > local epoch: peer is leader; this engine follows.
//   - Equal epochs: lexical label wins ("a" < "b").
//
// Epoch is bumped by the control-plane-authenticated /api/peer/force-leader
// endpoint (Phase 3). It persists across restart via WAL.
type Role int

const (
	// RoleStandalone is returned when no engine label is configured — i.e.
	// this is a single-engine session and no peer-role logic applies.
	// Gate() is always a no-op in this role.
	RoleStandalone Role = iota

	// RoleLeader is returned when this engine currently holds authority.
	// Either the peer is reachable and tiebreak picks this engine, or the
	// peer is unreachable and the deterministic tiebreak picks this engine.
	RoleLeader

	// RoleFollowerHealthy is returned when the peer is reachable and
	// tiebreak assigns leadership to the peer. Non-idempotent commands
	// still execute here — both engines run them during healthy active-
	// active because dedup is handled downstream (engine-label guard on
	// output, command_seq dedup on SCTE-35).
	RoleFollowerHealthy

	// RoleFollowerPartitioned is returned when the peer is unreachable and
	// tiebreak assigns leadership to the (unreachable) peer. Non-idempotent
	// commands are gated off in this state to prevent split-brain.
	RoleFollowerPartitioned
)

// String returns a stable lowercase name suitable for logging and wire
// exposure in state broadcasts.
func (r Role) String() string {
	switch r {
	case RoleStandalone:
		return "standalone"
	case RoleLeader:
		return "leader"
	case RoleFollowerHealthy:
		return "follower-healthy"
	case RoleFollowerPartitioned:
		return "follower-partitioned"
	}
	return "unknown"
}
