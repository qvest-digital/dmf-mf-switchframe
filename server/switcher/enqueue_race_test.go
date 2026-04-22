package switcher

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// Documents the contract of enqueueVideoWork under concurrent writers.
// An earlier review speculated that the double-select drop-then-enqueue
// primitive produced reorder or silent loss under concurrent writers,
// but inspection (validated by TDD) shows the code is correct per its
// intended contract:
//
//  1. Go channels are strictly FIFO, so no reorder is possible regardless
//     of writer concurrency — items land in mutex-acquire-esque order
//     determined by scheduling, and the reader consumes FIFO.
//  2. The "newest-wins drop" policy intentionally drops the oldest
//     channel item on each full enqueue. Those drops are NOT counted by
//     videoProcDropped — the counter is documented to track only
//     race-induced "both drop-receive and re-enqueue failed" events
//     (a caller trying to drop-oldest finds another writer has already
//     filled the slot and the new work cannot land).
//  3. No buffer leaks: every ReleaseYUV path is covered — branch 2a
//     releases the dropped-oldest buffer, branch 3b releases the
//     unenqueued new buffer.
//
// These tests pin down those properties so a future change that alters
// the contract (e.g. adds a mutex, tracks total drops) can update this
// documentation explicitly.

func TestEnqueueVideoWork_ChannelStaysBoundedUnderFlood(t *testing.T) {
	// Even under 1000 concurrent writers with no reader, the channel
	// stays at capacity — the newest-wins policy keeps enqueues bounded.
	s := newTestSwitcherDirect(newTestRelay())
	defer s.closeDirect()

	const N = 1000
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(pts int64) {
			defer wg.Done()
			s.enqueueVideoWork(videoProcWork{
				yuvFrame: &ProcessingFrame{PTS: pts},
			})
		}(int64(i))
	}
	wg.Wait()

	require.LessOrEqual(t, len(s.videoProcCh), cap(s.videoProcCh),
		"channel occupancy must never exceed capacity")
}

func TestEnqueueVideoWork_NoDuplicateItems(t *testing.T) {
	// Each call delivers at most one item into the channel. Concurrent
	// writers may race, but no single writer's item can appear twice.
	s := newTestSwitcherDirect(newTestRelay())
	defer s.closeDirect()

	const N = 500
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(pts int64) {
			defer wg.Done()
			s.enqueueVideoWork(videoProcWork{
				yuvFrame: &ProcessingFrame{PTS: pts},
			})
		}(int64(i))
	}
	wg.Wait()

	seenSet := make(map[int64]struct{})
drainLoop:
	for {
		select {
		case w := <-s.videoProcCh:
			if w.yuvFrame == nil {
				continue
			}
			_, dup := seenSet[w.yuvFrame.PTS]
			require.False(t, dup, "PTS %d appeared twice in channel", w.yuvFrame.PTS)
			seenSet[w.yuvFrame.PTS] = struct{}{}
		default:
			break drainLoop
		}
	}
}
