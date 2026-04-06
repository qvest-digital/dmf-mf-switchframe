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
