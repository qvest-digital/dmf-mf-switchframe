package operator

import (
	"testing"
	"time"
)

func TestSessionManager_Connect(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Alice", RoleDirector)
	sessions := sm.ActiveSessions()
	if len(sessions) != 1 {
		t.Errorf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].Name != "Alice" {
		t.Errorf("expected name 'Alice', got %q", sessions[0].Name)
	}
}

func TestSessionManager_Connect_Duplicate(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Alice", RoleDirector)
	sm.Connect("op1", "Alice", RoleDirector) // Should not panic or duplicate.
	if len(sm.ActiveSessions()) != 1 {
		t.Error("expected 1 session after duplicate connect")
	}
}

func TestSessionManager_Disconnect(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Alice", RoleDirector)
	sm.Disconnect("op1")
	if len(sm.ActiveSessions()) != 0 {
		t.Error("expected 0 sessions after disconnect")
	}
}

func TestSessionManager_Disconnect_ReleasesLocks(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Alice", RoleDirector)
	_ = sm.AcquireLock("op1", SubsystemSwitching)
	sm.Disconnect("op1")

	locks := sm.ActiveLocks()
	if len(locks) != 0 {
		t.Error("expected 0 locks after disconnect")
	}
}

func TestSessionManager_AcquireLock(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Alice", RoleDirector)
	err := sm.AcquireLock("op1", SubsystemSwitching)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}

	locks := sm.ActiveLocks()
	if len(locks) != 1 {
		t.Errorf("expected 1 lock, got %d", len(locks))
	}
	info, ok := locks[SubsystemSwitching]
	if !ok {
		t.Fatal("expected switching lock")
	}
	if info.HolderID != "op1" {
		t.Errorf("expected holder 'op1', got %q", info.HolderID)
	}
}

func TestSessionManager_AcquireLock_NoSession(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	err := sm.AcquireLock("op1", SubsystemSwitching)
	if err != ErrSessionNotFound {
		t.Errorf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestSessionManager_AcquireLock_InvalidSubsystem(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Alice", RoleDirector)
	err := sm.AcquireLock("op1", Subsystem("unknown"))
	if err != ErrInvalidSubsystem {
		t.Errorf("expected ErrInvalidSubsystem, got %v", err)
	}
}

func TestSessionManager_AcquireLock_NoPermission(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Bob", RoleAudio)
	err := sm.AcquireLock("op1", SubsystemSwitching)
	if err != ErrNoPermission {
		t.Errorf("expected ErrNoPermission, got %v", err)
	}
}

func TestSessionManager_AcquireLock_AlreadyLocked(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Alice", RoleDirector)
	sm.Connect("op2", "Bob", RoleDirector)

	_ = sm.AcquireLock("op1", SubsystemSwitching)
	err := sm.AcquireLock("op2", SubsystemSwitching)
	if err != ErrSubsystemLocked {
		t.Errorf("expected ErrSubsystemLocked, got %v", err)
	}
}

func TestSessionManager_AcquireLock_SameOperatorIdempotent(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Alice", RoleDirector)
	_ = sm.AcquireLock("op1", SubsystemSwitching)
	err := sm.AcquireLock("op1", SubsystemSwitching)
	if err != nil {
		t.Errorf("expected no error for same-operator re-lock, got %v", err)
	}
}

func TestSessionManager_ReleaseLock(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Alice", RoleDirector)
	_ = sm.AcquireLock("op1", SubsystemSwitching)
	err := sm.ReleaseLock("op1", SubsystemSwitching)
	if err != nil {
		t.Fatalf("ReleaseLock: %v", err)
	}
	if len(sm.ActiveLocks()) != 0 {
		t.Error("expected 0 locks after release")
	}
}

func TestSessionManager_ReleaseLock_NotOwned(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Alice", RoleDirector)
	sm.Connect("op2", "Bob", RoleDirector)
	_ = sm.AcquireLock("op1", SubsystemSwitching)

	err := sm.ReleaseLock("op2", SubsystemSwitching)
	if err != ErrLockNotOwned {
		t.Errorf("expected ErrLockNotOwned, got %v", err)
	}
}

func TestSessionManager_ReleaseLock_NotLocked(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Alice", RoleDirector)
	err := sm.ReleaseLock("op1", SubsystemSwitching)
	if err != ErrNotLocked {
		t.Errorf("expected ErrNotLocked, got %v", err)
	}
}

func TestSessionManager_ForceReleaseLock(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Alice", RoleDirector)
	sm.Connect("op2", "Bob", RoleAudio)
	_ = sm.AcquireLock("op2", SubsystemAudio)

	// Director force-releases another operator's lock.
	err := sm.ForceReleaseLock("op1", SubsystemAudio)
	if err != nil {
		t.Fatalf("ForceReleaseLock: %v", err)
	}
	if len(sm.ActiveLocks()) != 0 {
		t.Error("expected 0 locks after force release")
	}
}

func TestSessionManager_ForceReleaseLock_NonDirector(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Alice", RoleDirector)
	sm.Connect("op2", "Bob", RoleAudio)
	_ = sm.AcquireLock("op1", SubsystemSwitching)

	err := sm.ForceReleaseLock("op2", SubsystemSwitching)
	if err != ErrNoPermission {
		t.Errorf("expected ErrNoPermission, got %v", err)
	}
}

func TestSessionManager_CheckLock_Allowed(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Alice", RoleDirector)
	_ = sm.AcquireLock("op1", SubsystemSwitching)

	err := sm.CheckLock("op1", SubsystemSwitching)
	if err != nil {
		t.Errorf("expected no error for lock owner, got %v", err)
	}
}

func TestSessionManager_CheckLock_LockedByOther(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Alice", RoleDirector)
	sm.Connect("op2", "Bob", RoleDirector)
	_ = sm.AcquireLock("op1", SubsystemSwitching)

	err := sm.CheckLock("op2", SubsystemSwitching)
	if err != ErrSubsystemLocked {
		t.Errorf("expected ErrSubsystemLocked, got %v", err)
	}
}

func TestSessionManager_CheckLock_Unlocked(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Alice", RoleDirector)
	err := sm.CheckLock("op1", SubsystemSwitching)
	if err != nil {
		t.Errorf("expected no error for unlocked subsystem, got %v", err)
	}
}

func TestSessionManager_Heartbeat(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	sm.Connect("op1", "Alice", RoleDirector)
	time.Sleep(10 * time.Millisecond)
	sm.Heartbeat("op1")

	sessions := sm.ActiveSessions()
	if len(sessions) != 1 {
		t.Fatal("expected 1 session")
	}
	if sessions[0].LastSeen.Before(time.Now().Add(-1 * time.Second)) {
		t.Error("expected LastSeen to be updated")
	}
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
