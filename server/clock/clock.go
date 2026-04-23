// Package clock provides PTP-microsecond time + sync-state detection.
//
// Now() is the ordering clock used by the multi-region cmdqueue; swapping
// it for a real PTP-disciplined source is a separate (OPS-003) effort. This
// file's concern is the detection channel: GetSyncState reports whether
// the local clock can be trusted as a time reference. Before BUG-057 that
// function was a hardcoded stub that always said "synchronized" — which
// actively lied to the browser's /api/peer/health polling and left PTP
// faults invisible to operators. The replacement keeps the return shape
// identical but populates it from a background sampler that compares the
// wall clock (CLOCK_REALTIME via time.Now) to the monotonic clock (via
// time.Since on a captured start).
//
// The sampler cannot distinguish PTP from NTP as the disciplining source —
// a full pmc-based detection is Phase B. What it CAN detect is the common
// failure mode: the disciplining source has gone away and the local
// oscillator is free-running or being stepped. A 50ms drift over the 10s
// sliding window flips synchronized→false and leaves enough signal for the
// browser + metrics to raise alarms.
package clock

import (
	"sync"
	"sync/atomic"
	"time"
)

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
//
// Consumer contract: check Source alongside Synchronized when deciding
// whether to surface a UI alarm. Source == "systemclock-initializing"
// means the sampler has not yet produced a verdict (first 10s after boot);
// Synchronized == true in that window is a conservative default, not a
// confirmed measurement. Phase B consumers (UI chip, cmdqueue callback)
// should treat "initializing" as "unknown, do not alarm, do not trust."
type SyncState struct {
	Synchronized  bool   `json:"synchronized"`
	WindowDriftUs int64  `json:"windowDriftUs"` // signed wall-vs-monotonic drift accumulated over the sampler window (NOT a servo instantaneous offset)
	Source        string `json:"source"`        // "ptp" | "systemclock-monitored" | "systemclock-initializing" | "unknown"
}

// syncStateFn is the mockable seam for tests. atomic.Pointer so a test's
// Store is visible to any concurrently-running goroutine without a race,
// and so t.Parallel-using tests can't corrupt a single-var seam.
var syncStateFn atomic.Pointer[func() SyncState]

func init() {
	f := defaultGetSyncState
	syncStateFn.Store(&f)
}

// SetGetSyncStateFn installs a custom SyncState provider — tests use this
// to inject a fixture result. Pass nil to restore the default sampler.
// Returns the previously-installed fn so tests can defer-restore.
func SetGetSyncStateFn(fn func() SyncState) func() SyncState {
	prev := syncStateFn.Load()
	if fn == nil {
		f := defaultGetSyncState
		syncStateFn.Store(&f)
	} else {
		syncStateFn.Store(&fn)
	}
	if prev == nil {
		return defaultGetSyncState
	}
	return *prev
}

// GetSyncState returns the current clock synchronization state as observed
// by the background sampler (or the test fixture when overridden).
func GetSyncState() SyncState {
	if fn := syncStateFn.Load(); fn != nil {
		return (*fn)()
	}
	return defaultGetSyncState()
}

// samplerDriftThresholdUs — drift at or above this amount between wall
// and monotonic elapsed over the sampler window flips synchronized=false.
// 50 ms is 3+ orders of magnitude above any healthy PTP/NTP discipline
// budget and well under any frame-cadence interval we care about
// downstream; a single accidental step or a minute of free-running
// oscillator will trip it.
//
// What this does NOT catch: slow free-running drift (~10 µs/sec during
// grandmaster loss with phc2sys free-wheeling) accumulates to ~100 µs
// over the 10s window — well below threshold. Detecting that reliably
// requires positive PTP evidence (pmc shell-out), which is Phase B.
const samplerDriftThresholdUs = 50_000

// samplerWindowSamples — number of per-second samples retained before a
// drift verdict is issued. Keeps short transients (one-time NTP step)
// from false-alarming the steady-state detector.
const samplerWindowSamples = 10

// samplerInterval — cadence of the monotonic-vs-wall sample. 1 s is slow
// enough that the sampler adds ~0 CPU; fast enough that a grandmaster
// loss shows up within the window size × interval budget (10 s).
const samplerInterval = time.Second

// syncStateAtomic is the cache populated by runSampler and read by
// defaultGetSyncState. nil during cold boot (before the sampler starts);
// the getter falls through to the initializing default in that case.
var syncStateAtomic atomic.Pointer[SyncState]

// samplerOnce guarantees the sampler goroutine starts exactly once on
// first call to defaultGetSyncState — callers do not need to wire a
// lifecycle hook at app init.
var samplerOnce sync.Once

// initializingState is returned during the sampler's warmup (before the
// first full window). Conservative default: claim synchronized (so the
// UI doesn't red-alarm at boot on a healthy system) but call it out via
// the distinctive Source so the operator can tell this is an unwatched
// window rather than a confirmed reading.
var initializingState = SyncState{
	Synchronized:  true,
	WindowDriftUs: 0,
	Source:        "systemclock-initializing",
}

func defaultGetSyncState() SyncState {
	samplerOnce.Do(func() { go runSampler() })
	if s := syncStateAtomic.Load(); s != nil {
		return *s
	}
	return initializingState
}

// runSampler collects monotonic-vs-wall-clock deltas on a fixed cadence
// and writes the verdict to syncStateAtomic. Runs for the process
// lifetime; no stop channel because sync detection has no meaningful
// "off" state.
func runSampler() {
	type sample struct {
		monoNs int64
		realNs int64
	}

	startMono := time.Now() // carries a monotonic reading
	startRealNs := startMono.UnixNano()

	ring := make([]sample, 0, samplerWindowSamples)

	tick := time.NewTicker(samplerInterval)
	defer tick.Stop()

	for range tick.C {
		now := time.Now()
		elapsedMono := time.Since(startMono).Nanoseconds()
		elapsedReal := now.UnixNano() - startRealNs

		ring = append(ring, sample{monoNs: elapsedMono, realNs: elapsedReal})
		if len(ring) > samplerWindowSamples {
			ring = ring[len(ring)-samplerWindowSamples:]
		}
		if len(ring) < samplerWindowSamples {
			continue
		}

		first := ring[0]
		last := ring[len(ring)-1]
		// drift = how far the wall clock has jumped relative to the
		// monotonic clock over the window. Healthy systems: |drift| is
		// single-digit microseconds (NTP/PTP servo). Free-running or
		// stepped: tens of ms or more.
		drift := (last.realNs - first.realNs) - (last.monoNs - first.monoNs)
		absDrift := drift
		if absDrift < 0 {
			absDrift = -absDrift
		}
		windowDriftUs := drift / 1000
		synced := absDrift/1000 < samplerDriftThresholdUs

		syncStateAtomic.Store(&SyncState{
			Synchronized:  synced,
			WindowDriftUs: windowDriftUs,
			Source:        "systemclock-monitored",
		})
	}
}
