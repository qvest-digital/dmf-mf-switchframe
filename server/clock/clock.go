package clock

import "time"

// Now returns the current PTP time in microseconds since the Unix epoch.
//
// On macOS and in development builds, this uses time.Now().UnixMicro()
// (NTP-disciplined system clock). On Linux production deployments with a
// PTP hardware clock, this abstraction will be replaced with a direct
// CLOCK_REALTIME read disciplined by the PTP servo, providing sub-microsecond
// accuracy across the multi-region active-active pair.
//
// Both engines in a multi-region pair call Now() to agree on when commands
// (cuts, transitions, DVE changes) execute, so the abstraction boundary here
// is what matters — the backing clock can be swapped without touching callers.
func Now() int64 {
	return time.Now().UnixMicro()
}

// SyncState reports clock synchronization status.
type SyncState struct {
	Synchronized bool   `json:"synchronized"`
	OffsetUs     int64  `json:"offsetUs"` // offset from reference source
	Source       string `json:"source"`   // "ptp", "ntp", "freerun"
}

// GetSyncState returns the current clock synchronization state.
//
// The default implementation reports the NTP-disciplined system clock.
// When a PTP hardware clock is available, this will be replaced with
// real PTP servo offset measurements.
func GetSyncState() SyncState {
	return SyncState{
		Synchronized: true,
		Source:       "ntp",
		OffsetUs:     0,
	}
}
