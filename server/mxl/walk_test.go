package mxl

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
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

// waitingDomain stands in for libmxl over an mxlfs mounted with wait=: a
// lookup of a flow directory that is not there waits up to lookupWait for
// it to appear, and open takes one lock for the whole instance, as libmxl's
// Instance::getFlowReader does, and makes that lookup under it.
type waitingDomain struct {
	dir        string
	lookupWait time.Duration
	store      *flowStore

	mu sync.Mutex // the instance lock

	lookupsMu sync.Mutex
	lookups   []string
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

// lookup waits for dir as a path walk on the mxlfs does.
func (d *waitingDomain) lookup(dir string) error {
	d.lookupsMu.Lock()
	d.lookups = append(d.lookups, filepath.Base(dir))
	d.lookupsMu.Unlock()
	deadline := time.Now().Add(d.lookupWait)
	for {
		_, err := os.Stat(dir)
		if err == nil || time.Now().After(deadline) {
			return err
		}
		time.Sleep(time.Millisecond)
	}
}

func (d *waitingDomain) looked(name string) bool {
	d.lookupsMu.Lock()
	defer d.lookupsMu.Unlock()
	return slices.Contains(d.lookups, name)
}

func (d *waitingDomain) open(id string) (DiscreteReader, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	id = strings.ToLower(id)
	if err := d.lookup(filepath.Join(d.dir, id+".mxl-flow")); err != nil {
		return nil, errors.New("mxl: open reader: flow not found")
	}
	return d.store.open(id)
}

// appear creates flow id, as a mirror or a writer on the node does.
func (d *waitingDomain) appear(t *testing.T, id string, f *liveFlow) {
	t.Helper()
	d.store.mu.Lock()
	d.store.flows[id] = f
	d.store.mu.Unlock()
	if err := os.Mkdir(filepath.Join(d.dir, id+".mxl-flow"), 0o755); err != nil {
		t.Error(err)
	}
}

// Inputs retrying flows nobody writes do not hold up a connection to a flow
// that is there. In libmxl every open of an instance takes one lock, and on
// a waiting domain the lookup of a missing flow keeps it for the whole wait,
// so a connection queued behind them failed although its own flow was live.
func TestWalkFirst_MissingFlowsDoNotHoldUpAConnection(t *testing.T) {
	const wait = 2 * time.Second
	d := newWaitingDomain(t, wait, map[string]*liveFlow{liveID: newLiveFlow(1, videoShape)})
	open := walkFirst(d.dir, d.lookup, d.open)

	for _, id := range []string{missingID1, missingID2} {
		r := NewRetargetableReader(open, nil, nil)
		t.Cleanup(func() { _ = r.Close() })
		r.(Retargetable).Retarget(id)
	}
	// Let the retries start waiting first, as booked inputs do at startup
	// before any connection is made.
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

// A flow the domain does not list yet is looked up, which is what has the
// node bring it here, and a connection to it succeeds once it appears
// within the wait.
func TestWalkFirst_ConnectsToAFlowThatAppearsDuringTheWait(t *testing.T) {
	d := newWaitingDomain(t, 2*time.Second, map[string]*liveFlow{})
	open := walkFirst(d.dir, d.lookup, d.open)

	r := NewRetargetableReader(open, nil, nil)
	t.Cleanup(func() { _ = r.Close() })
	startVideo(t, r)
	time.AfterFunc(100*time.Millisecond, func() { d.appear(t, missingID1, newLiveFlow(1, videoShape)) })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.(Retargetable).Connect(ctx, strings.ToUpper(missingID1)); err != nil {
		t.Fatalf("connection to a flow that appeared during the wait: %v", err)
	}
	if !d.looked(missingID1 + ".mxl-flow") {
		t.Errorf("flow directory not looked up in lower case: %v", d.lookups)
	}
}

// A flow the lookup does not find is not opened, so its open does not take
// libmxl's lock for a second wait.
func TestWalkFirst_DoesNotOpenAFlowTheLookupMisses(t *testing.T) {
	called := false
	open := walkFirst(t.TempDir(), func(string) error { return fs.ErrNotExist }, func(string) (DiscreteReader, error) {
		called = true
		return nil, nil
	})
	if _, err := open(missingID1); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("open of a flow the lookup missed: %v", err)
	}
	if called {
		t.Error("open called for a flow the lookup missed")
	}
}

// Off mxlfs, a missing flow reaches libmxl's open, which an intent shim
// preloaded into the process acts on to bring the flow here.
func TestWalkFirst_OpensDirectlyOffMxlfs(t *testing.T) {
	var opened []string
	open := WalkFirst(t.TempDir(), func(id string) (DiscreteReader, error) {
		opened = append(opened, id)
		return nil, errors.New("not found")
	})
	_, _ = open(missingID1)
	if len(opened) != 1 {
		t.Errorf("open of a missing flow off mxlfs called %d times, want once", len(opened))
	}
}
