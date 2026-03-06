package operator

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

const (
	// staleTimeout is how long before an operator's session is considered stale.
	staleTimeout = 60 * time.Second

	// cleanupInterval is how often the cleanup goroutine runs.
	cleanupInterval = 15 * time.Second
)

// SessionManager tracks active operator sessions and subsystem locks.
// All lock operations are serialized through a single mutex to prevent
// race conditions between lock acquisition and release.
type SessionManager struct {
	log           *slog.Logger
	mu            sync.Mutex
	sessions      map[string]*Session       // operator ID → session
	locks         map[Subsystem]*LockInfo   // subsystem → lock
	onStateChange func()

	cancel context.CancelFunc
	done   chan struct{}
}

// NewSessionManager creates a session manager with a background cleanup goroutine.
func NewSessionManager() *SessionManager {
	ctx, cancel := context.WithCancel(context.Background())
	sm := &SessionManager{
		log:      slog.With("component", "operator"),
		sessions: make(map[string]*Session),
		locks:    make(map[Subsystem]*LockInfo),
		cancel:   cancel,
		done:     make(chan struct{}),
	}
	go sm.cleanupLoop(ctx)
	return sm
}

// Connect registers an active session for the given operator.
func (sm *SessionManager) Connect(operatorID, name string, role Role) {
	sm.mu.Lock()
	sm.sessions[operatorID] = &Session{
		OperatorID: operatorID,
		Name:       name,
		Role:       role,
		LastSeen:   time.Now(),
	}
	sm.mu.Unlock()
	sm.notifyStateChange()
}

// Disconnect removes an operator's session and releases their locks.
func (sm *SessionManager) Disconnect(operatorID string) {
	sm.mu.Lock()
	delete(sm.sessions, operatorID)
	sm.releaseAllLocks(operatorID)
	sm.mu.Unlock()
	sm.notifyStateChange()
}

// Heartbeat updates the last-seen time for an operator's session.
func (sm *SessionManager) Heartbeat(operatorID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if s, ok := sm.sessions[operatorID]; ok {
		s.LastSeen = time.Now()
	}
}

// AcquireLock locks a subsystem for the given operator.
func (sm *SessionManager) AcquireLock(operatorID string, sub Subsystem) error {
	sm.mu.Lock()

	session, ok := sm.sessions[operatorID]
	if !ok {
		sm.mu.Unlock()
		return ErrSessionNotFound
	}
	if !ValidSubsystems[sub] {
		sm.mu.Unlock()
		return ErrInvalidSubsystem
	}
	if !CanLock(session.Role, sub) {
		sm.mu.Unlock()
		return ErrNoPermission
	}

	if existing, locked := sm.locks[sub]; locked {
		if existing.HolderID == operatorID {
			sm.mu.Unlock()
			return nil // Idempotent re-lock.
		}
		sm.mu.Unlock()
		return ErrSubsystemLocked
	}

	sm.locks[sub] = &LockInfo{
		HolderID:   operatorID,
		HolderName: session.Name,
		AcquiredAt: time.Now(),
	}
	name := session.Name
	sm.mu.Unlock()

	sm.log.Info("lock acquired", "operator", name, "subsystem", sub)
	sm.notifyStateChange()
	return nil
}

// ReleaseLock releases a lock held by the given operator.
func (sm *SessionManager) ReleaseLock(operatorID string, sub Subsystem) error {
	sm.mu.Lock()

	existing, locked := sm.locks[sub]
	if !locked {
		sm.mu.Unlock()
		return ErrNotLocked
	}
	if existing.HolderID != operatorID {
		sm.mu.Unlock()
		return ErrLockNotOwned
	}

	holderName := existing.HolderName
	delete(sm.locks, sub)
	sm.mu.Unlock()

	sm.log.Info("lock released", "operator", holderName, "subsystem", sub)
	sm.notifyStateChange()
	return nil
}

// ForceReleaseLock allows a director to release any operator's lock.
func (sm *SessionManager) ForceReleaseLock(requestorID string, sub Subsystem) error {
	sm.mu.Lock()

	session, ok := sm.sessions[requestorID]
	if !ok {
		sm.mu.Unlock()
		return ErrSessionNotFound
	}
	if session.Role != RoleDirector {
		sm.mu.Unlock()
		return ErrNoPermission
	}

	existing, locked := sm.locks[sub]
	if !locked {
		sm.mu.Unlock()
		return ErrNotLocked
	}

	directorName := session.Name
	holderName := existing.HolderName
	delete(sm.locks, sub)
	sm.mu.Unlock()

	sm.log.Info("lock force-released", "director", directorName, "holder", holderName, "subsystem", sub)
	sm.notifyStateChange()
	return nil
}

// CheckLock verifies that the operator can proceed with a command on the subsystem.
// Returns nil if the subsystem is unlocked or locked by the same operator.
// Returns ErrSubsystemLocked if locked by another operator.
func (sm *SessionManager) CheckLock(operatorID string, sub Subsystem) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	existing, locked := sm.locks[sub]
	if !locked {
		return nil
	}
	if existing.HolderID == operatorID {
		return nil
	}
	return ErrSubsystemLocked
}

// ActiveSessions returns a snapshot of all active sessions.
func (sm *SessionManager) ActiveSessions() []Session {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	result := make([]Session, 0, len(sm.sessions))
	for _, s := range sm.sessions {
		result = append(result, *s)
	}
	return result
}

// ActiveLocks returns a snapshot of all active locks.
func (sm *SessionManager) ActiveLocks() map[Subsystem]LockInfo {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	result := make(map[Subsystem]LockInfo, len(sm.locks))
	for sub, info := range sm.locks {
		result[sub] = *info
	}
	return result
}

// OnStateChange registers a callback invoked when session or lock state changes.
func (sm *SessionManager) OnStateChange(fn func()) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.onStateChange = fn
}

// Close stops the cleanup goroutine and waits for it to finish.
func (sm *SessionManager) Close() {
	sm.cancel()
	<-sm.done
}

// releaseAllLocks removes all locks held by the given operator.
// Must be called with sm.mu held.
func (sm *SessionManager) releaseAllLocks(operatorID string) {
	for sub, info := range sm.locks {
		if info.HolderID == operatorID {
			delete(sm.locks, sub)
			sm.log.Info("lock auto-released", "operator", info.HolderName, "subsystem", sub)
		}
	}
}

// cleanupLoop periodically removes stale sessions.
func (sm *SessionManager) cleanupLoop(ctx context.Context) {
	defer close(sm.done)
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sm.cleanupStaleSessions()
		}
	}
}

// cleanupStaleSessions removes sessions that haven't sent a heartbeat recently.
func (sm *SessionManager) cleanupStaleSessions() {
	sm.mu.Lock()
	now := time.Now()
	var stale []struct{ id, name string }
	for id, s := range sm.sessions {
		if now.Sub(s.LastSeen) > staleTimeout {
			stale = append(stale, struct{ id, name string }{id, s.Name})
		}
	}
	for _, s := range stale {
		sm.releaseAllLocks(s.id)
		delete(sm.sessions, s.id)
	}
	sm.mu.Unlock()

	for _, s := range stale {
		sm.log.Info("session stale, removed", "operator", s.name, "id", s.id)
	}
	if len(stale) > 0 {
		sm.notifyStateChange()
	}
}

// notifyStateChange invokes the callback outside the lock.
func (sm *SessionManager) notifyStateChange() {
	sm.mu.Lock()
	fn := sm.onStateChange
	sm.mu.Unlock()
	if fn != nil {
		fn()
	}
}
