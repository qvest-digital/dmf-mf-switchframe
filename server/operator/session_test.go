package operator

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSessionManager_Connect(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Alice", RoleDirector)
	sessions := sm.ActiveSessions()
	require.Len(t, sessions, 1)
	require.Equal(t, "Alice", sessions[0].Name)
}

func TestSessionManager_Connect_Duplicate(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Alice", RoleDirector)
	sm.Connect("op1", "Alice", RoleDirector) // Should not panic or duplicate.
	require.Len(t, sm.ActiveSessions(), 1, "expected 1 session after duplicate connect")
}

func TestSessionManager_Disconnect(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Alice", RoleDirector)
	sm.Disconnect("op1")
	require.Empty(t, sm.ActiveSessions())
}

func TestSessionManager_Disconnect_ReleasesLocks(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Alice", RoleDirector)
	_ = sm.AcquireLock("op1", SubsystemSwitching)
	sm.Disconnect("op1")

	require.Empty(t, sm.ActiveLocks())
}

func TestSessionManager_AcquireLock(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Alice", RoleDirector)
	err := sm.AcquireLock("op1", SubsystemSwitching)
	require.NoError(t, err)

	locks := sm.ActiveLocks()
	require.Len(t, locks, 1)
	info, ok := locks[SubsystemSwitching]
	require.True(t, ok, "expected switching lock")
	require.Equal(t, "op1", info.HolderID)
}

func TestSessionManager_AcquireLock_NoSession(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	err := sm.AcquireLock("op1", SubsystemSwitching)
	require.ErrorIs(t, err, ErrSessionNotFound)
}

func TestSessionManager_AcquireLock_InvalidSubsystem(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Alice", RoleDirector)
	err := sm.AcquireLock("op1", Subsystem("unknown"))
	require.ErrorIs(t, err, ErrInvalidSubsystem)
}

func TestSessionManager_AcquireLock_NoPermission(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Bob", RoleAudio)
	err := sm.AcquireLock("op1", SubsystemSwitching)
	require.ErrorIs(t, err, ErrNoPermission)
}

func TestSessionManager_AcquireLock_AlreadyLocked(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Alice", RoleDirector)
	sm.Connect("op2", "Bob", RoleDirector)

	_ = sm.AcquireLock("op1", SubsystemSwitching)
	err := sm.AcquireLock("op2", SubsystemSwitching)
	require.ErrorIs(t, err, ErrSubsystemLocked)
}

func TestSessionManager_AcquireLock_SameOperatorIdempotent(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Alice", RoleDirector)
	_ = sm.AcquireLock("op1", SubsystemSwitching)
	err := sm.AcquireLock("op1", SubsystemSwitching)
	require.NoError(t, err, "expected no error for same-operator re-lock")
}

func TestSessionManager_ReleaseLock(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Alice", RoleDirector)
	_ = sm.AcquireLock("op1", SubsystemSwitching)
	err := sm.ReleaseLock("op1", SubsystemSwitching)
	require.NoError(t, err)
	require.Empty(t, sm.ActiveLocks())
}

func TestSessionManager_ReleaseLock_NotOwned(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Alice", RoleDirector)
	sm.Connect("op2", "Bob", RoleDirector)
	_ = sm.AcquireLock("op1", SubsystemSwitching)

	err := sm.ReleaseLock("op2", SubsystemSwitching)
	require.ErrorIs(t, err, ErrLockNotOwned)
}

func TestSessionManager_ReleaseLock_NotLocked(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Alice", RoleDirector)
	err := sm.ReleaseLock("op1", SubsystemSwitching)
	require.ErrorIs(t, err, ErrNotLocked)
}

func TestSessionManager_ForceReleaseLock(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Alice", RoleDirector)
	sm.Connect("op2", "Bob", RoleAudio)
	_ = sm.AcquireLock("op2", SubsystemAudio)

	// Director force-releases another operator's lock.
	err := sm.ForceReleaseLock("op1", SubsystemAudio)
	require.NoError(t, err)
	require.Empty(t, sm.ActiveLocks())
}

func TestSessionManager_ForceReleaseLock_NonDirector(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Alice", RoleDirector)
	sm.Connect("op2", "Bob", RoleAudio)
	_ = sm.AcquireLock("op1", SubsystemSwitching)

	err := sm.ForceReleaseLock("op2", SubsystemSwitching)
	require.ErrorIs(t, err, ErrNoPermission)
}

func TestSessionManager_CheckLock_Allowed(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Alice", RoleDirector)
	_ = sm.AcquireLock("op1", SubsystemSwitching)

	err := sm.CheckLock("op1", SubsystemSwitching)
	require.NoError(t, err)
}

func TestSessionManager_CheckLock_LockedByOther(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Alice", RoleDirector)
	sm.Connect("op2", "Bob", RoleDirector)
	_ = sm.AcquireLock("op1", SubsystemSwitching)

	err := sm.CheckLock("op2", SubsystemSwitching)
	require.ErrorIs(t, err, ErrSubsystemLocked)
}

func TestSessionManager_CheckLock_Unlocked(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Alice", RoleDirector)
	err := sm.CheckLock("op1", SubsystemSwitching)
	require.NoError(t, err)
}

func TestSessionManager_Heartbeat(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Alice", RoleDirector)
	time.Sleep(10 * time.Millisecond)
	sm.Heartbeat("op1")

	sessions := sm.ActiveSessions()
	require.Len(t, sessions, 1)
	require.False(t, sessions[0].LastSeen.Before(time.Now().Add(-1*time.Second)),
		"expected LastSeen to be updated")
}

func TestSessionManager_OnStateChange(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	called := make(chan struct{}, 10)
	sm.OnStateChange(func() {
		select {
		case called <- struct{}{}:
		default:
		}
	})

	sm.Connect("op1", "Alice", RoleDirector)
	select {
	case <-called:
	case <-time.After(1 * time.Second):
		t.Error("OnStateChange not called for Connect")
	}
}
