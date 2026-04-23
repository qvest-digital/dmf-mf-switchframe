package switcher

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/switchframe/server/transition"
)

// TestSwitcher_HotStateSnapshotConsistency pins the Phase-2 invariant: a
// concurrent Cut cannot produce a torn (state, transEngine, programSource)
// triple in a hotState snapshot. Holds two workers against the switcher —
// one oscillating Cut, one repeatedly loading hotState and asserting the
// transEngine-vs-state invariant:
//
//   - StateTransitioning / StateFTBTransitioning / StateFTBReversing imply
//     transEngine != nil at snapshot time.
//   - StateIdle and StateFTB imply transEngine == nil.
//
// Passes on the Phase-1 mirror-write scaffolding because every transEngine
// assignment is paired with a state transition inside a single
// updateHotState CoW closure — the reader sees exactly one published
// snapshot, never the midpoint. Fails if someone splits the two mutations
// across separate updateHotState calls.
func TestSwitcher_HotStateSnapshotConsistency(t *testing.T) {
	relay := distribution.NewRelay()
	sw := newTestSwitcher(t, relay)

	relayA := distribution.NewRelay()
	relayB := distribution.NewRelay()
	sw.RegisterSource("camA", relayA)
	sw.RegisterSource("camB", relayB)
	if err := sw.Cut(context.Background(), "camA"); err != nil {
		t.Fatalf("initial Cut: %v", err)
	}

	stop := make(chan struct{})
	errCh := make(chan string, 2)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		keys := []string{"camA", "camB"}
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			_ = sw.Cut(context.Background(), keys[i%len(keys)])
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 2_000_000; i++ {
			select {
			case <-stop:
				return
			default:
			}
			hs := sw.hotState.Load()
			if hs == nil {
				errCh <- "hotState.Load returned nil mid-Cut"
				return
			}
			if hs.state.isInTransition() && hs.transEngine == nil {
				errCh <- "torn snapshot: state=transitioning but transEngine=nil"
				return
			}
			if (hs.state == StateIdle || hs.state == StateFTB) && hs.transEngine != nil {
				errCh <- "torn snapshot: state=idle/ftb but transEngine!=nil"
				return
			}
		}
	}()

	time.Sleep(250 * time.Millisecond)
	close(stop)
	wg.Wait()

	select {
	case msg := <-errCh:
		t.Fatalf("snapshot invariant violated: %s", msg)
	default:
	}
}

// TestSwitcher_HotStateSnapshotConsistency_Transitions exercises the highest-risk
// writer paths for hotState tearing: StartTransition + AbortTransition (mix)
// cycled in a tight loop. Before H1 was fixed, StartTransition published
// hs.state=Transitioning via transitionState() before the paired transEngine
// publish at the end of the function, so readers saw
// (state=Transitioning && transEngine==nil) for the duration of engine
// construction. Post-fix, state is only published alongside transEngine in a
// single updateHotState closure.
func TestSwitcher_HotStateSnapshotConsistency_Transitions(t *testing.T) {
	relay := distribution.NewRelay()
	sw := newTestSwitcher(t, relay)
	sw.SetTransitionConfig(TransitionConfig{
		DecoderFactory: func() (transition.VideoDecoder, error) {
			return transition.NewMockDecoder(4, 4), nil
		},
	})

	relayA := distribution.NewRelay()
	relayB := distribution.NewRelay()
	sw.RegisterSource("camA", relayA)
	sw.RegisterSource("camB", relayB)
	if err := sw.Cut(context.Background(), "camA"); err != nil {
		t.Fatalf("initial Cut: %v", err)
	}

	stop := make(chan struct{})
	errCh := make(chan string, 2)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if err := sw.StartTransition(context.Background(), "camB", "mix", 60000, ""); err == nil {
				sw.AbortTransition()
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 2_000_000; i++ {
			select {
			case <-stop:
				return
			default:
			}
			hs := sw.hotState.Load()
			if hs == nil {
				errCh <- "hotState.Load returned nil mid-transition"
				return
			}
			if hs.state.isInTransition() && hs.transEngine == nil {
				errCh <- "torn snapshot: state=transitioning but transEngine=nil"
				return
			}
			if (hs.state == StateIdle || hs.state == StateFTB) && hs.transEngine != nil {
				errCh <- "torn snapshot: state=idle/ftb but transEngine!=nil"
				return
			}
		}
	}()

	time.Sleep(250 * time.Millisecond)
	close(stop)
	wg.Wait()

	select {
	case msg := <-errCh:
		t.Fatalf("snapshot invariant violated: %s", msg)
	default:
	}
}
