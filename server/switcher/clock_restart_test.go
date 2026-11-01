package switcher

import (
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/cmdqueue"
)

// A timed format change is dispatched on the sync-loop goroutine, from
// drainCommandQueue. Changing the rate replaces the reference clock that
// goroutine is reading, so the replacement must not wait for the loop to
// end: the loop would wait for itself and the program would stop.
func TestTimedFormatSetRestartsClockFromSyncLoop(t *testing.T) {
	s := newTestSwitcherDirect(distribution.NewRelay())

	q := cmdqueue.New(clock.Now)
	d := cmdqueue.NewDispatcher()
	formatSet := make(chan error, 1)
	d.Register("format_set", func(json.RawMessage) error {
		err := s.SetPipelineFormat(PipelineFormat{
			Name: "test25", Width: 320, Height: 240, FPSNum: 25, FPSDen: 1,
		})
		formatSet <- err
		return err
	})
	var pings atomic.Int64
	d.Register("ping", func(json.RawMessage) error {
		pings.Add(1)
		return nil
	})
	s.SetCommandQueue(q, d)
	s.Start()
	// A deadlocked loop would also hang Close, so a failed run skips it.
	t.Cleanup(func() {
		if !t.Failed() {
			s.Close()
		}
	})

	if err := q.Enqueue(cmdqueue.Command{Seq: 1, Action: "format_set"}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-formatSet:
		if err != nil {
			t.Fatalf("SetPipelineFormat: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("format_set from the command queue did not return within 2s")
	}

	// The loop keeps draining commands, now on the new clock.
	if err := q.Enqueue(cmdqueue.Command{Seq: 2, Action: "ping"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for pings.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("sync loop stopped draining commands after the format change")
		}
		time.Sleep(5 * time.Millisecond)
	}

	s.configMu.Lock()
	rc := s.refClock
	s.configMu.Unlock()
	if n, den := rc.Rate(); n != 25 || den != 1 {
		t.Fatalf("clock rate = %d/%d, want 25/1", n, den)
	}
	before := rc.TickCount()
	time.Sleep(400 * time.Millisecond)
	if got := rc.TickCount() - before; got < 8 || got > 12 {
		t.Fatalf("clock ticked %d times in 400ms, want 10 +-2 at 25 fps", got)
	}
}
