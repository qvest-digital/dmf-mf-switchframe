// Package cmdqueue provides a PTP-timed command queue for multi-region
// active-active command scheduling. Commands are stamped with a PTP microsecond
// ExecuteAt time and drained when the caller's frame-loop clock reaches that time.
package cmdqueue

import (
	"container/heap"
	"encoding/json"
	"errors"
	"sync"
)

// DefaultMaxAge is the default staleness threshold (2 seconds in µs).
const DefaultMaxAge = int64(2_000_000)

// maxSeen is the maximum number of seen Seq values before the dedup map is pruned.
const maxSeen = 100_000

// Sentinel errors returned by Enqueue.
var (
	ErrStaleCommand     = errors.New("cmdqueue: command is too old (stale)")
	ErrDuplicateCommand = errors.New("cmdqueue: duplicate command_seq")
)

// Command is a single schedulable control action.
type Command struct {
	ExecuteAt int64           `json:"execute_at"` // PTP microseconds; 0 = execute immediately
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

// WithWAL attaches a write-ahead log for crash recovery. When set, Enqueue
// appends commands to the WAL before adding them to the heap, and Drain
// marks executed entries. On construction, un-executed WAL entries are replayed.
func WithWAL(w *WAL) Option {
	return func(q *Queue) { q.wal = w }
}

// Queue holds pending commands sorted by ExecuteAt and drains them when the
// injected clock reaches their scheduled time.
type Queue struct {
	mu     sync.Mutex
	h      cmdHeap
	seen   map[uint64]struct{}
	clock  func() int64
	maxAge int64
	wal    *WAL
}

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
	// Replay WAL if attached — re-enqueue commands that were not yet executed.
	if q.wal != nil {
		if cmds, err := q.wal.Replay(q.maxAge); err == nil {
			for _, cmd := range cmds {
				_ = q.Enqueue(cmd) // ignore dup errors from replay
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

	// Staleness check — only applies when ExecuteAt is non-zero.
	if cmd.ExecuteAt != 0 && now-cmd.ExecuteAt > q.maxAge {
		return ErrStaleCommand
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	// Dedup by Seq.
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

// Drain removes and returns all commands whose ExecuteAt is <= the current
// clock value. Commands are returned in ascending ExecuteAt order.
// Commands with ExecuteAt=0 are always returned.
func (q *Queue) Drain() []Command {
	now := q.clock()

	q.mu.Lock()
	defer q.mu.Unlock()

	var out []Command
	for q.h.Len() > 0 {
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
	// Treat ExecuteAt=0 as smallest (drain immediately) but keep heap consistent
	// when both are zero by using index as tiebreaker (stable relative to Push order).
	ti, tj := h[i].ExecuteAt, h[j].ExecuteAt
	switch {
	case ti == 0 && tj == 0:
		return false // equal; heap stability handled by Go's sort
	case ti == 0:
		return true
	case tj == 0:
		return false
	default:
		return ti < tj
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
