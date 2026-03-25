package st2110

import "sync"

// ST2110Snapshot provides debug snapshot data for ST 2110 subsystem diagnostics.
// It implements the debug.SnapshotProvider interface (DebugSnapshot() map[string]any)
// without importing the debug package to avoid circular dependencies.
type ST2110Snapshot struct {
	mu       sync.RWMutex
	ptp      PTPStatus
	sessions []sessionInfo
}

type sessionInfo struct {
	Name  string
	Type  string
	Stats SessionStats
}

// NewST2110Snapshot creates a new ST2110Snapshot.
func NewST2110Snapshot() *ST2110Snapshot {
	return &ST2110Snapshot{}
}

// SetPTP updates the current PTP synchronization status.
func (s *ST2110Snapshot) SetPTP(status PTPStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ptp = status
}

// AddSession adds or updates a session by name.
func (s *ST2110Snapshot) AddSession(name, typ string, stats SessionStats) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.sessions {
		if s.sessions[i].Name == name {
			s.sessions[i].Type = typ
			s.sessions[i].Stats = stats
			return
		}
	}
	s.sessions = append(s.sessions, sessionInfo{
		Name:  name,
		Type:  typ,
		Stats: stats,
	})
}

// RemoveSession removes a session by name.
func (s *ST2110Snapshot) RemoveSession(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.sessions {
		if s.sessions[i].Name == name {
			s.sessions = append(s.sessions[:i], s.sessions[i+1:]...)
			return
		}
	}
}

// DebugSnapshot returns a map suitable for JSON serialization containing
// PTP status and all active session statistics.
func (s *ST2110Snapshot) DebugSnapshot() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ptp := map[string]any{
		"locked":         s.ptp.Locked,
		"grandmaster_id": s.ptp.GrandmasterID,
		"offset_ns":      s.ptp.OffsetNs,
		"domain":         s.ptp.Domain,
	}

	sessions := make([]map[string]any, len(s.sessions))
	for i, sess := range s.sessions {
		sessions[i] = map[string]any{
			"name":            sess.Name,
			"type":            sess.Type,
			"frames_received": sess.Stats.FramesReceived,
			"frames_dropped":  sess.Stats.FramesDropped,
			"frames_corrupt":  sess.Stats.FramesCorrupt,
			"packets_lost":    sess.Stats.PacketsLost,
		}
	}

	return map[string]any{
		"ptp":      ptp,
		"sessions": sessions,
	}
}
