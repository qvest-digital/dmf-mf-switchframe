// Package cmdqueue provides a PTP-timed command queue for multi-region
// active-active command scheduling. Commands are stamped with a PTP microsecond
// ExecuteAt time and drained when the caller's frame-loop clock reaches that time.
package cmdqueue

import (
	"container/heap"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
)

// DefaultMaxAge is the default staleness threshold (2 seconds in µs).
const DefaultMaxAge = int64(2_000_000)

// maxSeen is the maximum number of seen Seq values before the dedup map is pruned.
const maxSeen = 100_000

// MaxDrainPerCall limits commands drained per call to prevent clock-jump bursts.
const MaxDrainPerCall = 32

// Sentinel errors returned by Enqueue.
var (
	ErrStaleCommand     = errors.New("cmdqueue: command is too old (stale)")
	ErrDuplicateCommand = errors.New("cmdqueue: duplicate command_seq")
	ErrQueueFull        = errors.New("cmdqueue: queue is full")
)

// Command is a single schedulable control action.
type Command struct {
	ExecuteAt int64           `json:"execute_at"`  // PTP microseconds; 0 = execute immediately
	Seq       uint64          `json:"command_seq"` // monotonic sequence for dedup
	Action    string          `json:"action"`      // "cut", "preview", etc.
	Payload   json.RawMessage `json:"payload"`
}

// Option configures a Queue at construction time.
type Option func(*Queue)

// WithMaxAge sets the staleness threshold in microseconds. Commands whose
// ExecuteAt is more than maxAge µs in the past are rejected as stale.
// Default: DefaultMaxAge (2s). For active-active mode use 10_000_000 (10s).
func WithMaxAge(maxAge int64) Option {
	return func(q *Queue) { q.maxAge = maxAge }
}

// WithMaxSize sets the maximum number of commands the queue will hold.
// When set to a positive value, Enqueue returns ErrQueueFull if the heap
// is at capacity. Default 0 means no limit (backward compatible).
func WithMaxSize(n int) Option {
	return func(q *Queue) { q.maxSize = n }
}

// WithWAL attaches a write-ahead log for crash recovery. When set, Enqueue
// appends commands to the WAL before adding them to the heap, and Drain
// marks executed entries. On construction, un-executed WAL entries are replayed.
func WithWAL(w *WAL) Option {
	return func(q *Queue) { q.wal = w }
}

// MetricsCallbacks provides event hooks for Prometheus metric instrumentation.
// All function fields are optional; nil callbacks are silently skipped.
type MetricsCallbacks struct {
	OnDrain     func(count int) // called after each Drain with the number of commands executed
	OnLate      func()          // called when a late command is promoted to immediate
	OnStale     func()          // called when a stale command is rejected
	OnDup       func()          // called when a duplicate command is rejected
	OnClockJump func()          // called when a backward clock jump is detected
}

// WithMetrics attaches metric callbacks to the queue.
func WithMetrics(cb *MetricsCallbacks) Option {
	return func(q *Queue) { q.metricsCB = cb }
}

// WithReplaySeqTracker registers a callback invoked once per WAL-replayed
// command during New() (after the replay Enqueue succeeds). The caller uses
// this to advance an external high-water mark (switcher.lastCommandSeq) so
// a restart does not report seq=0 on /api/peer/health during WAL replay.
//
// The callback runs synchronously inside New(); it must not block or
// acquire the Queue's internal lock.
func WithReplaySeqTracker(fn func(seq uint64)) Option {
	return func(q *Queue) { q.replaySeqTracker = fn }
}

// Queue holds pending commands sorted by ExecuteAt and drains them when the
// injected clock reaches their scheduled time.
type Queue struct {
	mu              sync.Mutex
	h               cmdHeap
	seen            map[uint64]struct{}
	prunedHighWater uint64 // highest Seq evicted during prune; rejects retransmits
	// inflightSeqs/inflightCount reserve commands whose WAL append is in
	// flight on another goroutine: Enqueue releases q.mu across the WAL
	// write+fsync so it never stalls the frame loop's Drain, and these
	// reservations keep the dedup and maxSize checks atomic with the
	// append. walAppendDone wakes same-Seq enqueues parked on a
	// reservation.
	inflightSeqs     map[uint64]struct{}
	inflightCount    int
	walAppendDone    *sync.Cond
	compacting       atomic.Bool // at most one background WAL compaction in flight
	clock            func() int64
	maxAge           int64
	maxSize          int
	wal              *WAL
	metricsCB        *MetricsCallbacks
	replaySeqTracker func(seq uint64) // optional; invoked per WAL-replayed command in New
	lateCommands     atomic.Int64
	lastDrainClock   atomic.Int64 // last clock value seen by Drain, for backward jump detection
	clockJumps       atomic.Int64
}

// LateCommands returns the number of late commands that were promoted to immediate execution.
func (q *Queue) LateCommands() int64 { return q.lateCommands.Load() }

// New creates a ready-to-use Queue. clock is called on every Enqueue and Drain
// to obtain the current PTP time in microseconds.
func New(clock func() int64, opts ...Option) *Queue {
	q := &Queue{
		seen:         make(map[uint64]struct{}),
		inflightSeqs: make(map[uint64]struct{}),
		clock:        clock,
		maxAge:       DefaultMaxAge,
	}
	q.walAppendDone = sync.NewCond(&q.mu)
	for _, opt := range opts {
		opt(q)
	}
	if q.wal != nil {
		// Seed dedup map from executed commands to prevent re-execution after crash.
		// Also advance the external seq tracker from executed tombstones so the
		// restart high-water mark covers the narrow window where commands
		// drained (and MarkExecuted'd) after the last seqPersist flush but
		// before the crash — switcher_seq.json missed them, but the WAL
		// preserved their Seq as a tombstone.
		if seqs, err := q.wal.ReplayExecutedSeqs(); err == nil {
			for _, seq := range seqs {
				q.seen[seq] = struct{}{}
				if q.replaySeqTracker != nil && seq > 0 {
					q.replaySeqTracker(seq)
				}
			}
		} else {
			// An unreadable WAL silently disables restart dedup — the
			// executed tombstones are the SOLE source of dedup state at
			// restart, so a peer retransmit of an already-executed Seq
			// would be re-executed. Operators must hear about it.
			slog.Error("cmdqueue: WAL executed-seq replay failed — restart dedup is disabled; retransmits of already-executed commands may re-execute",
				"err", err)
		}
		// Replay un-executed commands.
		if cmds, err := q.wal.Replay(q.maxAge); err == nil {
			for _, cmd := range cmds {
				if err := q.Enqueue(cmd); err != nil {
					continue
				}
				// Advance the external seq tracker so a restart's
				// /api/peer/health reflects the pre-restart high-water
				// mark even before any HTTP traffic re-triggers
				// TrackCommandSeq through the timedHandler path.
				if q.replaySeqTracker != nil && cmd.Seq > 0 {
					q.replaySeqTracker(cmd.Seq)
				}
			}
		} else {
			slog.Error("cmdqueue: WAL replay failed — pending commands from before the restart were not recovered",
				"err", err)
		}
	}
	return q
}

// Enqueue adds cmd to the queue. It returns ErrStaleCommand if the command's
// ExecuteAt (when non-zero) is more than maxAge microseconds in the past, and
// ErrDuplicateCommand if a command with the same Seq has already been enqueued.
func (q *Queue) Enqueue(cmd Command) error {
	now := q.clock()

	// Grace period — late commands execute immediately, truly stale ones are dropped.
	if cmd.ExecuteAt != 0 {
		lateness := now - cmd.ExecuteAt
		if lateness > q.maxAge*2 {
			if q.metricsCB != nil && q.metricsCB.OnStale != nil {
				q.metricsCB.OnStale()
			}
			return ErrStaleCommand
		}
		if lateness > q.maxAge {
			cmd.ExecuteAt = 0
			q.lateCommands.Add(1)
			if q.metricsCB != nil && q.metricsCB.OnLate != nil {
				q.metricsCB.OnLate()
			}
		}
	}

	q.mu.Lock()

	// Dedup by Seq (skip when Seq is 0 — untimed/legacy commands).
	if cmd.Seq > 0 {
		for {
			// Reject commands at or below the pruned high-water mark.
			// After pruning, these Seq values are no longer in the seen map but
			// must still be rejected to prevent double execution of retransmits.
			if cmd.Seq <= q.prunedHighWater {
				if q.metricsCB != nil && q.metricsCB.OnDup != nil {
					q.metricsCB.OnDup()
				}
				q.mu.Unlock()
				return ErrDuplicateCommand
			}
			if _, seen := q.seen[cmd.Seq]; seen {
				if q.metricsCB != nil && q.metricsCB.OnDup != nil {
					q.metricsCB.OnDup()
				}
				q.mu.Unlock()
				return ErrDuplicateCommand
			}
			// The same Seq has a WAL append in flight on another goroutine:
			// wait for it to resolve, then re-run the dedup checks. If it
			// succeeded the Seq is now seen (duplicate); if its append
			// failed the Seq stays unseen and this retransmit proceeds.
			if _, busy := q.inflightSeqs[cmd.Seq]; !busy {
				break
			}
			q.walAppendDone.Wait()
		}
	}

	// Size limit check — reject before mutating any state. In-flight WAL
	// reservations count toward capacity so a burst of concurrent enqueues
	// cannot overshoot maxSize while their appends run off-lock.
	if q.maxSize > 0 && len(q.h)+q.inflightCount >= q.maxSize {
		q.mu.Unlock()
		return ErrQueueFull
	}

	// Persist to WAL before recording the Seq as seen and committing to heap.
	// The dedup record must come AFTER a successful append: if Append fails
	// (disk full, EIO, fsync failure) the command is neither persisted nor
	// executed, so its Seq must remain unseen to allow a retransmit to be
	// accepted rather than rejected as a duplicate.
	//
	// The append (a file write + fsync, commonly 1-20ms) runs WITHOUT q.mu:
	// holding the queue lock across an fsync would stall the frame loop's
	// Drain for the fsync duration — and a burst of concurrent enqueues
	// would stack one fsync each under the lock. Atomicity with the dedup
	// check is preserved by reserving the Seq in inflightSeqs while
	// unlocked; concurrent enqueues of the same Seq park above until the
	// reservation resolves.
	if q.wal != nil {
		q.inflightCount++
		if cmd.Seq > 0 {
			q.inflightSeqs[cmd.Seq] = struct{}{}
		}
		q.mu.Unlock()

		err := q.wal.Append(cmd)

		q.mu.Lock()
		q.inflightCount--
		if cmd.Seq > 0 {
			delete(q.inflightSeqs, cmd.Seq)
		}
		q.walAppendDone.Broadcast()
		if err != nil {
			q.mu.Unlock()
			return err
		}
	}

	// Record the Seq as seen only now that durability is guaranteed (skip for
	// untimed/legacy commands with Seq 0).
	if cmd.Seq > 0 {
		q.seen[cmd.Seq] = struct{}{}
		if len(q.seen) > maxSeen {
			// Seq is monotonic — keep only recent entries by rebuilding with
			// entries above a high-water mark. This retains the most recent
			// ~maxSeen/2 entries instead of clearing everything.
			var minSeq uint64
			if cmd.Seq > uint64(maxSeen/2) {
				minSeq = cmd.Seq - uint64(maxSeen/2)
			}
			pruned := make(map[uint64]struct{}, maxSeen/2)
			for seq := range q.seen {
				if seq >= minSeq {
					pruned[seq] = struct{}{}
				}
			}
			q.seen = pruned
			q.prunedHighWater = minSeq
		}
	}

	heap.Push(&q.h, cmd)
	q.mu.Unlock()
	return nil
}

// ClockJumps returns the number of backward clock jumps detected during Drain.
func (q *Queue) ClockJumps() int64 { return q.clockJumps.Load() }

// Drain removes and returns all commands whose ExecuteAt is <= the current
// clock value. Commands are returned in ascending ExecuteAt order.
// Commands with ExecuteAt=0 are always returned.
func (q *Queue) Drain() []Command {
	now := q.clock()

	// Detect backward clock jump (NTP/PTP correction).
	// Only warn — don't drop commands. They'll drain naturally when the clock
	// catches back up, and MaxDrainPerCall prevents burst-executing a backlog.
	prev := q.lastDrainClock.Load()
	if prev > 0 && now < prev {
		jump := prev - now
		q.clockJumps.Add(1)
		if q.metricsCB != nil && q.metricsCB.OnClockJump != nil {
			q.metricsCB.OnClockJump()
		}
		slog.Warn("cmdqueue: backward clock jump detected",
			"jump_us", jump, "prev_us", prev, "now_us", now)
	}
	q.lastDrainClock.Store(now)

	q.mu.Lock()

	var out []Command
	for q.h.Len() > 0 && len(out) < MaxDrainPerCall {
		top := q.h[0]
		if top.ExecuteAt != 0 && top.ExecuteAt > now {
			break
		}
		heap.Pop(&q.h)
		out = append(out, top)
	}
	q.mu.Unlock()

	// Mark drained commands as executed in WAL.  The executed entries are
	// the sole source of dedup state at restart (Queue.New seeds the
	// `seen` map from ReplayExecutedSeqs), so we do NOT Truncate — a
	// crash-retransmit between Drain and client retry would otherwise
	// re-dispatch the command.
	//
	// This WAL I/O runs AFTER releasing q.mu so it never blocks concurrent
	// Enqueue from HTTP handlers nor stalls the frame loop while holding the
	// queue lock. All drained commands are marked in a SINGLE
	// MarkExecutedCommands call, which APPENDS one tombstone line per
	// command with one fsync — it never reads or rewrites the file, so the
	// frame-loop cost is O(drained commands), not O(file size). The full
	// commands (not just seqs) are passed so Seq-0 (untimed/legacy,
	// dedup-exempt) commands get content-correlated tombstones: several
	// Seq-0 commands can be pending at once, and a bare {Seq:0} tombstone
	// used to mark ALL of them executed, silently dropping the still-pending
	// ones at crash replay.
	//
	// Compaction — the only O(file size) pass — keeps the file bounded at
	// roughly the last maxSeen executed tombstones (matching the in-memory
	// seen-map watermark) plus pending entries. It is gated on
	// walCompactMinAppends of growth and runs on a background goroutine so
	// the frame loop never pays a full-file read+rewrite+fsync; the CAS
	// flag ensures at most one compaction is in flight.
	if q.wal != nil && len(out) > 0 {
		if err := q.wal.MarkExecutedCommands(out); err != nil {
			slog.Warn("cmdqueue: WAL MarkExecutedCommands failed — crash here can replay these commands",
				"count", len(out), "err", err)
		}
		if q.compacting.CompareAndSwap(false, true) {
			go func() {
				defer q.compacting.Store(false)
				if err := q.wal.MaybeCompactExecuted(maxSeen); err != nil {
					slog.Warn("cmdqueue: WAL compact failed — file will continue growing until next successful compact",
						"err", err)
				}
			}()
		}
	}

	if len(out) > 0 && q.metricsCB != nil && q.metricsCB.OnDrain != nil {
		q.metricsCB.OnDrain(len(out))
	}

	return out
}

// Pending returns the number of commands currently waiting in the queue.
func (q *Queue) Pending() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.h.Len()
}

// ── min-heap implementation ───────────────────────────────────────────────────

type cmdHeap []Command

func (h cmdHeap) Len() int { return len(h) }

func (h cmdHeap) Less(i, j int) bool {
	ti, tj := h[i].ExecuteAt, h[j].ExecuteAt
	switch {
	case ti == 0 && tj == 0:
		return h[i].Seq < h[j].Seq
	case ti == 0:
		return true
	case tj == 0:
		return false
	case ti != tj:
		return ti < tj
	default:
		return h[i].Seq < h[j].Seq
	}
}

func (h cmdHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *cmdHeap) Push(x any) {
	*h = append(*h, x.(Command))
}

func (h *cmdHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}
