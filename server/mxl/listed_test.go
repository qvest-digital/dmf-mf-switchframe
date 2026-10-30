package mxl

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	liveID     = "6a1f0e2b-3c4d-4e5f-8a9b-0c1d2e3f4a5b"
	missingID1 = "11111111-1111-4111-8111-111111111111"
	missingID2 = "22222222-2222-4222-8222-222222222222"
)

// waitingDomain stands in for libmxl over a domain that waits for missing
// names: open takes one lock for the whole instance, as libmxl's
// Instance::getFlowReader does, and a flow without a directory holds it for
// lookupWait before it fails, as a lookup on mxlfs mounted with wait= does.
type waitingDomain struct {
	dir        string
	lookupWait time.Duration
	store      *flowStore

	mu sync.Mutex // the instance lock
}

func newWaitingDomain(t *testing.T, wait time.Duration, flows map[string]*liveFlow) *waitingDomain {
	t.Helper()
	d := &waitingDomain{dir: t.TempDir(), lookupWait: wait, store: newFlowStore(flows)}
	for id := range flows {
		if err := os.Mkdir(filepath.Join(d.dir, id+".mxl-flow"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func (d *waitingDomain) open(id string) (DiscreteReader, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, err := os.Stat(filepath.Join(d.dir, strings.ToLower(id)+".mxl-flow")); err != nil {
		time.Sleep(d.lookupWait)
		return nil, errors.New("mxl: open reader: flow not found")
	}
	return d.store.open(id)
}

// Inputs retrying flows nobody writes do not hold up a connection to a flow
// that is there. In libmxl every open of an instance takes one lock, and on
// a waiting domain an open of a missing flow keeps it for the whole wait, so
// a connection queued behind them failed although its own flow was live.
func TestOnlyListed_MissingFlowsDoNotHoldUpAConnection(t *testing.T) {
	const wait = 2 * time.Second
	d := newWaitingDomain(t, wait, map[string]*liveFlow{liveID: newLiveFlow(1, videoShape)})
	open := OnlyListed(d.dir, d.open)

	for _, id := range []string{missingID1, missingID2} {
		r := NewRetargetableReader(open, nil, nil)
		t.Cleanup(func() { _ = r.Close() })
		r.(Retargetable).Retarget(id)
	}
	// Let the retries reach the instance lock first, as booked inputs do at
	// startup before any connection is made.
	time.Sleep(50 * time.Millisecond)

	live := NewRetargetableReader(open, nil, nil)
	t.Cleanup(func() { _ = live.Close() })
	startVideo(t, live)
	ctx, cancel := context.WithTimeout(context.Background(), wait/4)
	defer cancel()
	start := time.Now()
	if err := live.(Retargetable).Connect(ctx, liveID); err != nil {
		t.Fatalf("connection to a live flow failed after %v: %v", time.Since(start), err)
	}
}

// A listed flow is opened whatever the case of its id; one the domain does
// not list never reaches open.
func TestOnlyListed_OpensOnlyListedFlows(t *testing.T) {
	d := newWaitingDomain(t, time.Hour, map[string]*liveFlow{liveID: newLiveFlow(1, videoShape)})
	var opened []string
	open := OnlyListed(d.dir, func(id string) (DiscreteReader, error) {
		opened = append(opened, id)
		return d.store.open(strings.ToLower(id))
	})

	if _, err := open(missingID1); err == nil {
		t.Error("a flow the domain does not list opened")
	}
	if len(opened) != 0 {
		t.Errorf("open called for a flow the domain does not list: %v", opened)
	}
	if _, err := open(strings.ToUpper(liveID)); err != nil {
		t.Errorf("listed flow, id in upper case: %v", err)
	}
}

// A domain that cannot be listed leaves the decision to open.
func TestOnlyListed_UnlistableDomainOpens(t *testing.T) {
	called := false
	open := OnlyListed(filepath.Join(t.TempDir(), "absent"), func(string) (DiscreteReader, error) {
		called = true
		return nil, errors.New("not found")
	})
	_, _ = open(missingID1)
	if !called {
		t.Error("open not called for a domain that cannot be listed")
	}
}
