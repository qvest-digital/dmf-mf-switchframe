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

// Queue holds pending commands sorted by ExecuteAt and drains them when the
// injected clock reaches their scheduled time.
type Queue struct {
	mu              sync.Mutex
	h               cmdHeap
	seen            map[uint64]struct{}
	prunedHighWater uint64 // highest Seq evicted during prune; rejects retransmits
	clock           func() int64
	maxAge          int64
	maxSize         int
	wal             *WAL
	lateCommands    atomic.Int64
	lastDrainClock  int64 // last clock value seen by Drain, for backward jump detection
	clockJumps      atomic.Int64
}

// LateCommands returns the number of late commands that were promoted to immediate execution.
func (q *Queue) LateCommands() int64 { return q.lateCommands.Load() }

// New creates a ready-to-use Queue. clock is called on every Enqueue and Drain
// to obtain the current PTP time in microseconds.
func New(clock func() int64, opts ...Option) *Queue {
	q := &Queue{
		seen:   make(map[uint64]struct{}),
		clock:  clock,
		maxAge: DefaultMaxAge,
	}
	for _, opt := range opts {
		opt(q)
	}
	if q.wal != nil {
		// Seed dedup map from executed commands to prevent re-execution after crash.
		if seqs, err := q.wal.ReplayExecutedSeqs(); err == nil {
			for _, seq := range seqs {
				q.seen[seq] = struct{}{}
			}
		}
		// Replay un-executed commands.
		if cmds, err := q.wal.Replay(q.maxAge); err == nil {
			for _, cmd := range cmds {
				_ = q.Enqueue(cmd)
			}
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
			return ErrStaleCommand
		}
		if lateness > q.maxAge {
			cmd.ExecuteAt = 0
			q.lateCommands.Add(1)
		}
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	// Size limit check — reject before mutating any state.
	if q.maxSize > 0 && len(q.h) >= q.maxSize {
		return ErrQueueFull
	}

	// Dedup by Seq (skip when Seq is 0 — untimed/legacy commands).
	if cmd.Seq > 0 {
		// Reject commands at or below the pruned high-water mark.
		// After pruning, these Seq values are no longer in the seen map but
		// must still be rejected to prevent double execution of retransmits.
		if cmd.Seq <= q.prunedHighWater {
			return ErrDuplicateCommand
		}
		if _, seen := q.seen[cmd.Seq]; seen {
			return ErrDuplicateCommand
		}
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

	// Persist to WAL before committing to heap.
	if q.wal != nil {
		if err := q.wal.Append(cmd); err != nil {
			return err
		}
	}

	heap.Push(&q.h, cmd)
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
	prev := atomic.LoadInt64(&q.lastDrainClock)
	if prev > 0 && now < prev {
		jump := prev - now
		q.clockJumps.Add(1)
		slog.Warn("cmdqueue: backward clock jump detected",
			"jump_us", jump, "prev_us", prev, "now_us", now)
	}
	atomic.StoreInt64(&q.lastDrainClock, now)

	q.mu.Lock()
	defer q.mu.Unlock()

	var out []Command
	for q.h.Len() > 0 && len(out) < MaxDrainPerCall {
		top := q.h[0]
		if top.ExecuteAt != 0 && top.ExecuteAt > now {
			break
		}
		heap.Pop(&q.h)
		out = append(out, top)
	}

	// Mark drained commands as executed in WAL.
	if q.wal != nil && len(out) > 0 {
		for _, cmd := range out {
			_ = q.wal.MarkExecuted(cmd.Seq)
		}
		// Truncate WAL when heap is empty (all commands executed).
		if q.h.Len() == 0 {
			_ = q.wal.Truncate()
		}
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
