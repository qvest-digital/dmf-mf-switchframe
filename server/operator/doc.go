// Package operator provides multi-operator management with role-based
// subsystem locking for the Switchframe video switcher.
//
// Operators register with a name and role, receive a per-operator bearer
// token, and can lock subsystems (switching, audio, graphics, replay,
// output) to prevent conflicting commands. The system is backward-compatible:
// when no operators are registered, all requests pass through without
// authentication.
//
// Key types:
//   - [Store]: File-based operator registration (JSON, atomic writes)
//   - [SessionManager]: Session tracking, heartbeat, 60s stale timeout
//   - [Role]: Operator role (director, audio, graphics, viewer)
//   - [Subsystem]: Lockable subsystem identifier
//   - [Operator]: Registered operator with name, role, and token
//
// Four roles define permission boundaries: director (full access), audio
// (audio subsystem only), graphics (graphics only), and viewer (read-only).
// Directors can force-unlock any subsystem. Sessions expire after 60
// seconds without a heartbeat, automatically releasing held locks.
package operator
