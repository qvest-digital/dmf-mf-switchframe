package switcher

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/zsiec/prism/distribution"
)

var (
	benchSilentLogger      = slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 10}))
	benchSlogSilencedOnce  sync.Once
	benchPrevDefaultLogger *slog.Logger
)

// silenceSlogForBench replaces the default slog handler with a no-op sink so
// prism-distribution log lines do not pollute benchmark output. Idempotent;
// only runs the first call per process.
func silenceSlogForBench() {
	benchSlogSilencedOnce.Do(func() {
		benchPrevDefaultLogger = slog.Default()
		slog.SetDefault(benchSilentLogger)
	})
}

// newBenchSwitcher constructs a Switcher with n virtual sources registered,
// sized for 1080p benchmarking. Uses RegisterVirtualSource so the bench does
// not require a decoder factory. Program source is set to the first key so
// handleRawVideoFrame follows the "program passes through" path for index 0
// and the "filtered" path for the remaining n-1 — roughly matching a
// production workload where many sources are on-tile but only one is PGM.
func newBenchSwitcher(b *testing.B, nSources int) *Switcher {
	b.Helper()
	silenceSlogForBench()
	relay := distribution.NewRelay()
	benchFmt := PipelineFormat{
		Name:   "bench",
		Width:  1920,
		Height: 1080,
		FPSNum: 60,
		FPSDen: 1,
	}
	s := &Switcher{
		log:           benchSilentLogger,
		programRelay:  relay,
		health:        newHealthMonitor(),
		videoProcCh:   make(chan videoProcWork, 8),
		videoProcDone: make(chan struct{}),
		// Pre-seed legacy defaults so initAtomicSlots mirrors them.
		ftbDurationMs:            defaultFTBDurationMs,
		transitionAbortThreshold: defaultTransitionAbortThreshold,
	}
	s.framePool.Store(NewFramePool(8, 1920, 1080, ColorYUV420_8bit))
	s.frameBudgetNs.Store(benchFmt.FrameBudgetNs())
	s.pipelineFormat.Store(&benchFmt)
	s.recentCmdErrors = newRecentErrors(8)
	s.delayBuffer = NewDelayBuffer(s)
	s.initAtomicSlots()
	s.registerBuiltinSourcesLocked()
	go s.videoProcessingLoop()

	for i := 0; i < nSources; i++ {
		key := makeBenchSourceKey(i)
		sourceRelay := distribution.NewRelay()
		s.RegisterVirtualSource(key, sourceRelay)
	}

	// Make the first virtual source the program so the main hot-path
	// branch (pass-through to broadcastProcessedFromPF) fires once per
	// bench iteration when sourceKey == program.
	_ = s.Cut(context.Background(), makeBenchSourceKey(0))

	b.Cleanup(func() { s.Close() })
	return s
}

func makeBenchSourceKey(i int) string {
	return "bench-cam-" + fmtIntTwoDigits(i)
}

// fmtIntTwoDigits avoids a strconv.Itoa allocation inside the hot path when
// keys are constructed on-demand in parallel workers. Keys are pre-computed
// outside b.ResetTimer via SourceKeys(), so this only runs during setup.
func fmtIntTwoDigits(i int) string {
	if i < 10 {
		return "0" + string('0'+rune(i))
	}
	tens := i / 10
	ones := i % 10
	return string([]byte{'0' + byte(tens), '0' + byte(ones)})
}

// newBenchRawFrame returns a ProcessingFrame of the given dimensions with a
// pre-allocated YUV420 buffer. The buffer is shared across all hot-path
// invocations inside the bench loop — handleRawVideoFrame does not mutate
// pf.Data, so this is safe. PTS monotonically increases per call only when
// the caller updates it; benches that don't care about PTS can reuse the
// default zero.
func newBenchRawFrame(width, height int) *ProcessingFrame {
	size := width * height * 3 / 2
	return &ProcessingFrame{
		Data:   make([]byte, size),
		Width:  width,
		Height: height,
		Format: ColorYUV420_8bit,
		PTS:    0,
	}
}

// BenchmarkHandleRawVideoFrame_32Sources measures per-call cost of the raw
// YUV video handler under 32-source load. Mean ns/op on the Phase 1
// baseline is the number we target to improve after Phase 2 reader
// migration.
func BenchmarkHandleRawVideoFrame_32Sources(b *testing.B) {
	sw := newBenchSwitcher(b, 32)
	pf := newBenchRawFrame(1920, 1080)
	keys := sw.SourceKeys()
	if len(keys) == 0 {
		b.Fatal("no sources registered")
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			sw.handleRawVideoFrame(keys[i%len(keys)], pf)
			i++
		}
	})
}

// BenchmarkHandleRawVideoFrame_UnderWriterContention measures tail latency
// when a writer goroutine hammers SetLabel at 100 Hz while the handler is
// called by b.RunParallel workers. The metric that matters here is p99:
// RWMutex writer-starvation semantics cause reader tail latency to spike on
// every writer arrival. Phase 2 replaces the RLock with lock-free
// atomic.Pointer loads, so p99 should flatten under contention.
func BenchmarkHandleRawVideoFrame_UnderWriterContention(b *testing.B) {
	sw := newBenchSwitcher(b, 32)
	pf := newBenchRawFrame(1920, 1080)
	keys := sw.SourceKeys()
	if len(keys) == 0 {
		b.Fatal("no sources registered")
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		i := 0
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				_ = sw.SetLabel(context.Background(), keys[i%len(keys)], "flicker")
				i++
			}
		}
	}()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			sw.handleRawVideoFrame(keys[i%len(keys)], pf)
			i++
		}
	})
	b.StopTimer()
	close(stop)
	wg.Wait()
}
