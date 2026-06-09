package switcher

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Finding #82: swapPipeline's drain goroutine (`old.Wait(); old.Close()`)
// could observe a zero in-flight counter in the window between the runner's
// `s.pipeline.Load()` and Run's `inflight.Add(1)`, closing pipeline nodes
// (including cgo encoder nodes) while Run was still about to process a frame
// through them. runPipelineGuarded closes that window by registering
// in-flight FIRST and re-checking the pipeline pointer before running.

// swapGuardNode flags a violation when Process or a second Close is called
// after the node has been closed.
type swapGuardNode struct {
	closed       atomic.Bool
	processed    atomic.Int64
	violations   *atomic.Int64
	processDelay time.Duration
}

func (n *swapGuardNode) Name() string                          { return "swap_guard" }
func (n *swapGuardNode) Configure(format PipelineFormat) error { return nil }
func (n *swapGuardNode) Active() bool                          { return true }
func (n *swapGuardNode) Err() error                            { return nil }
func (n *swapGuardNode) Latency() time.Duration                { return 0 }

func (n *swapGuardNode) Process(dst, src *ProcessingFrame) *ProcessingFrame {
	if n.closed.Load() {
		n.violations.Add(1)
	}
	if n.processDelay > 0 {
		time.Sleep(n.processDelay)
	}
	n.processed.Add(1)
	if n.closed.Load() {
		n.violations.Add(1)
	}
	return src
}

func (n *swapGuardNode) Close() error {
	n.closed.Store(true)
	return nil
}

func buildSwapGuardPipeline(t *testing.T, violations *atomic.Int64, delay time.Duration) (*Pipeline, *swapGuardNode) {
	t.Helper()
	node := &swapGuardNode{violations: violations, processDelay: delay}
	p := &Pipeline{}
	format := PipelineFormat{Name: "test", Width: 32, Height: 24, FPSNum: 30, FPSDen: 1}
	require.NoError(t, p.Build(format, nil, []PipelineNode{node}))
	return p, node
}

// TestRunPipelineGuardedSkipsSwappedOutPipeline pins the guard semantics:
// once a swap has replaced the pipeline, a frame must be processed by the
// NEW pipeline's nodes, never by the old (possibly already closed) ones.
func TestRunPipelineGuardedSkipsSwappedOutPipeline(t *testing.T) {
	var violations atomic.Int64
	relay := newTestRelay()
	sw := newTestSwitcher(t, relay)
	defer sw.Close()

	p1, node1 := buildSwapGuardPipeline(t, &violations, 0)
	sw.pipeline.Store(p1)

	p2, node2 := buildSwapGuardPipeline(t, &violations, 0)
	sw.swapPipeline(p2)
	sw.drainWg.Wait() // old pipeline (p1) is now fully closed

	pf := &ProcessingFrame{Data: make([]byte, 32*24*3/2), Width: 32, Height: 24}
	out, ran := sw.runPipelineGuarded(pf)
	require.True(t, ran)
	require.NotNil(t, out)

	require.Equal(t, int64(0), node1.processed.Load(), "closed pipeline must not process frames")
	require.Equal(t, int64(1), node2.processed.Load(), "current pipeline must process the frame")
	require.Equal(t, int64(0), violations.Load())

	sw.pipeline.Store(nil) // detach test pipelines before Close
}

// TestSwapPipelineConcurrentRunNoUseAfterClose stress-tests rapid pipeline
// swaps against a goroutine continuously running frames. Nodes record a
// violation if Process executes after Close. Run with -race.
func TestSwapPipelineConcurrentRunNoUseAfterClose(t *testing.T) {
	var violations atomic.Int64
	relay := newTestRelay()
	sw := newTestSwitcher(t, relay)
	defer sw.Close()

	p0, _ := buildSwapGuardPipeline(t, &violations, 0)
	sw.pipeline.Store(p0)

	stop := make(chan struct{})
	runnerDone := make(chan struct{})
	go func() {
		defer close(runnerDone)
		pf := &ProcessingFrame{Data: make([]byte, 32*24*3/2), Width: 32, Height: 24}
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, _ = sw.runPipelineGuarded(pf)
		}
	}()

	for i := 0; i < 500; i++ {
		p, _ := buildSwapGuardPipeline(t, &violations, 0)
		sw.swapPipeline(p)
	}

	close(stop)
	<-runnerDone
	sw.drainWg.Wait()

	require.Equal(t, int64(0), violations.Load(),
		"no node may be processed after Close during pipeline swaps")

	sw.pipeline.Store(nil) // detach test pipelines before Close
}
