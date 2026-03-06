// Package operator provides multi-operator support with role-based
// subsystem locking for the Switchframe video switcher. Operators
// register with a name and role, receive per-operator bearer tokens,
// and can lock subsystems (switching, audio, graphics, replay, output)
// to prevent conflicting commands from other operators.
package operator

import (
	"errors"
	"time"
)

// Sentinel errors for the operator subsystem.
var (
	ErrNotFound         = errors.New("operator: not found")
	ErrDuplicateName    = errors.New("operator: name already registered")
	ErrInvalidRole      = errors.New("operator: invalid role")
	ErrEmptyName        = errors.New("operator: name must not be empty")
	ErrNoPermission     = errors.New("operator: insufficient role permissions")
	ErrSubsystemLocked  = errors.New("operator: subsystem locked by another operator")
	ErrNotLocked        = errors.New("operator: subsystem not locked")
	ErrLockNotOwned     = errors.New("operator: lock not owned by this operator")
	ErrInvalidSubsystem = errors.New("operator: invalid subsystem")
	ErrSessionNotFound  = errors.New("operator: session not found")
)

// Role defines an operator's permission level.
type Role string

const (
	RoleDirector Role = "director"
	RoleAudio    Role = "audio"
	RoleGraphics Role = "graphics"
	RoleViewer   Role = "viewer"
)

// ValidRoles is the set of allowed roles.
var ValidRoles = map[Role]bool{
	RoleDirector: true,
	RoleAudio:    true,
	RoleGraphics: true,
	RoleViewer:   true,
}

// Subsystem identifies a lockable area of the switcher.
type Subsystem string

const (
	SubsystemSwitching Subsystem = "switching"
	SubsystemAudio     Subsystem = "audio"
	SubsystemGraphics  Subsystem = "graphics"
	SubsystemReplay    Subsystem = "replay"
	SubsystemOutput    Subsystem = "output"
)

// ValidSubsystems is the set of lockable subsystems.
var ValidSubsystems = map[Subsystem]bool{
	SubsystemSwitching: true,
	SubsystemAudio:     true,
	SubsystemGraphics:  true,
	SubsystemReplay:    true,
	SubsystemOutput:    true,
}

// rolePermissions defines which subsystems each role can command.
var rolePermissions = map[Role]map[Subsystem]bool{
	RoleDirector: {
		SubsystemSwitching: true,
		SubsystemAudio:     true,
		SubsystemGraphics:  true,
		SubsystemReplay:    true,
		SubsystemOutput:    true,
	},
	RoleAudio: {
		SubsystemAudio: true,
	},
	RoleGraphics: {
		SubsystemGraphics: true,
	},
	RoleViewer: {},
}

// roleLockable defines which subsystems each role can lock.
var roleLockable = map[Role]map[Subsystem]bool{
	RoleDirector: {
		SubsystemSwitching: true,
		SubsystemAudio:     true,
		SubsystemGraphics:  true,
		SubsystemReplay:    true,
		SubsystemOutput:    true,
	},
	RoleAudio: {
		SubsystemAudio: true,
	},
	RoleGraphics: {
		SubsystemGraphics: true,
	},
	RoleViewer: {},
}

// CanCommand returns whether the given role can issue commands to the subsystem.
func CanCommand(role Role, sub Subsystem) bool {
	perms, ok := rolePermissions[role]
	if !ok {
		return false
	}
	return perms[sub]
}

// CanLock returns whether the given role can lock the subsystem.
func CanLock(role Role, sub Subsystem) bool {
	lockable, ok := roleLockable[role]
	if !ok {
		return false
	}
	return lockable[sub]
}

// Operator is the persisted record for a registered operator.
type Operator struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Role  Role   `json:"role"`
	Token string `json:"token"`
}

// OperatorInfo is the JSON-serializable summary broadcast in ControlRoomState.
type OperatorInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Role      Role   `json:"role"`
	Connected bool   `json:"connected"`
}

// Session tracks an active operator connection.
type Session struct {
	OperatorID string
	Name       string
	Role       Role
	LastSeen   time.Time
}

// LockInfo describes an active subsystem lock.
type LockInfo struct {
	HolderID   string    `json:"holderId"`
	HolderName string    `json:"holderName"`
	AcquiredAt time.Time `json:"acquiredAt"`
}
