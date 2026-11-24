package switcher

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/zsiec/prism/distribution"

	"github.com/zsiec/switchframe/server/cmdqueue"
)

// The frame loop dispatches drained commands only while no keyed command
// runs on arrival, so what the queue records as run is what ran, in order.
func TestDrainCommandQueueWaitsForLocalRun(t *testing.T) {
	s := newTestSwitcherDirect(distribution.NewRelay())
	q := cmdqueue.New(func() int64 { return 1_000 }, cmdqueue.WithStateKeys(map[string][]string{"cut": {"program"}}))
	dispatched := make(chan struct{}, 1)
	d := cmdqueue.NewDispatcher()
	d.Register("cut", func(json.RawMessage) error { dispatched <- struct{}{}; return nil })
	s.SetCommandQueue(q, d)
	if err := q.Enqueue(cmdqueue.Command{ExecuteAt: 500, Seq: 1, Action: "cut"}); err != nil {
		t.Fatal(err)
	}

	running, release := make(chan struct{}), make(chan struct{})
	go func() {
		_ = q.RunLocal(cmdqueue.Command{Action: "cut", Seq: 2, OrderAt: 400}, func() bool {
			close(running)
			<-release
			return true
		})
	}()
	<-running
	go s.drainCommandQueue()
	select {
	case <-dispatched:
		t.Fatal("a drained command was dispatched while a command ran on arrival")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case <-dispatched:
	case <-time.After(2 * time.Second):
		t.Fatal("the drained command was not dispatched once released")
	}
}
