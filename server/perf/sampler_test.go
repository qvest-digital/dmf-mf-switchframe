package perf

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// --- Mock providers ---

type mockSwitcherPerf struct {
	mu     sync.Mutex
	sample SwitcherSample
}

func (m *mockSwitcherPerf) PerfSample() SwitcherSample {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sample
}

func (m *mockSwitcherPerf) set(s SwitcherSample) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sample = s
}

type mockMixerPerf struct {
	mu     sync.Mutex
	sample MixerSample
}

func (m *mockMixerPerf) PerfSample() MixerSample {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sample
}

func (m *mockMixerPerf) set(s MixerSample) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sample = s
}

type mockOutputPerf struct {
	mu     sync.Mutex
	sample OutputSample
}

func (m *mockOutputPerf) PerfSample() OutputSample {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sample
}

// --- RingStat tests ---

func TestRingStat_Push_Window(t *testing.T) {
	var rs RingStat
	// Push known values: 10, 20, 30, 40, 50
	rs.Push(10)
	rs.Push(20)
	rs.Push(30)
	rs.Push(40)
	rs.Push(50)

	w := rs.Window(5)

	if w.MinNs != 10 {
		t.Errorf("MinNs = %d, want 10", w.MinNs)
	}
	if w.MaxNs != 50 {
		t.Errorf("MaxNs = %d, want 50", w.MaxNs)
	}
	if w.MeanNs != 30 {
		t.Errorf("MeanNs = %d, want 30", w.MeanNs)
	}
	// p95 index = int(4 * 0.95) = int(3.8) = 3 → sorted[3] = 40
	if w.P95Ns != 40 {
		t.Errorf("P95Ns = %d, want 40", w.P95Ns)
	}
}

func TestRingStat_CircularOverwrite(t *testing.T) {
	var rs RingStat
	// Push 65 values (0..64), should keep last 60 (5..64)
	for i := int64(0); i < 65; i++ {
		rs.Push(i)
	}

	if rs.len != 60 {
		t.Fatalf("len = %d, want 60", rs.len)
	}

	w := rs.Window(60)
	// Oldest should be 5, newest 64
	if w.MinNs != 5 {
		t.Errorf("MinNs = %d, want 5", w.MinNs)
	}
	if w.MaxNs != 64 {
		t.Errorf("MaxNs = %d, want 64", w.MaxNs)
	}
	// Mean of 5..64 = (5+64)*60/2 / 60 = 34.5 → int64 division = 34
	expectedMean := int64((5 + 64) * 60 / 2 / 60)
	if w.MeanNs != expectedMean {
		t.Errorf("MeanNs = %d, want %d", w.MeanNs, expectedMean)
	}
}

func TestRingStat_WindowSingleSample(t *testing.T) {
	var rs RingStat
	rs.Push(42)

	w := rs.Window(1)
	if w.MinNs != 42 || w.MaxNs != 42 || w.MeanNs != 42 || w.P95Ns != 42 {
		t.Errorf("single sample: got min=%d max=%d mean=%d p95=%d, want all 42",
			w.MinNs, w.MaxNs, w.MeanNs, w.P95Ns)
	}
}

func TestRingStat_WindowEmpty(t *testing.T) {
	var rs RingStat
	w := rs.Window(10)
	if w.MinNs != 0 || w.MaxNs != 0 || w.MeanNs != 0 || w.P95Ns != 0 {
		t.Errorf("empty window: got min=%d max=%d mean=%d p95=%d, want all 0",
			w.MinNs, w.MaxNs, w.MeanNs, w.P95Ns)
	}
}

// --- Sampler tests ---

func TestSampler_Tick(t *testing.T) {
	sw := &mockSwitcherPerf{sample: SwitcherSample{
		Sources: map[string]SourceSample{
			"cam1": {DecodeLastNs: 5000, Health: "active"},
		},
		PipelineLastNs: 10000,
		NodeTimings:    map[string]int64{"encode": 3000},
		E2ELastNs:      15000,
		QueueLen:       2,
		OutputFPS:      30.0,
		BroadcastGapNs: 33000,
		FrameBudgetNs:  33333,
	}}
	mx := &mockMixerPerf{sample: MixerSample{
		Mode:           "mix",
		MixCycleLastNs: 2000,
		FramesOutput:   100,
	}}
	out := &mockOutputPerf{sample: OutputSample{
		ViewerVideoSent: 500,
	}}

	s := NewSampler(sw, mx, out)

	// Manually tick (don't start background goroutine)
	s.tick()

	// Verify rings were populated
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.pipelineRing.len != 1 {
		t.Errorf("pipelineRing.len = %d, want 1", s.pipelineRing.len)
	}
	if s.e2eRing.len != 1 {
		t.Errorf("e2eRing.len = %d, want 1", s.e2eRing.len)
	}
	if s.mixCycleRing.len != 1 {
		t.Errorf("mixCycleRing.len = %d, want 1", s.mixCycleRing.len)
	}
	ring, ok := s.decodeRings["cam1"]
	if !ok {
		t.Fatal("decodeRings missing cam1")
	}
	if ring.len != 1 {
		t.Errorf("cam1 decode ring len = %d, want 1", ring.len)
	}
	nodeRing, ok := s.nodeRings["encode"]
	if !ok {
		t.Fatal("nodeRings missing encode")
	}
	if nodeRing.len != 1 {
		t.Errorf("encode node ring len = %d, want 1", nodeRing.len)
	}
}

func TestSampler_Baseline_SaveAndDiff(t *testing.T) {
	sw := &mockSwitcherPerf{sample: SwitcherSample{
		PipelineLastNs: 10000,
		E2ELastNs:      20000,
		Sources:        map[string]SourceSample{},
		NodeTimings:    map[string]int64{},
		FrameBudgetNs:  33333,
	}}
	mx := &mockMixerPerf{sample: MixerSample{MixCycleLastNs: 5000}}
	out := &mockOutputPerf{}

	s := NewSampler(sw, mx, out)

	// Push some data
	for i := 0; i < 5; i++ {
		s.tick()
	}

	s.SaveBaseline("before")

	// Verify baseline exists
	s.mu.RLock()
	bl, ok := s.baselines["before"]
	s.mu.RUnlock()
	if !ok {
		t.Fatal("baseline 'before' not saved")
	}
	if bl.Pipeline.MeanNs != 10000 {
		t.Errorf("baseline pipeline mean = %d, want 10000", bl.Pipeline.MeanNs)
	}

	// Change values and verify diff
	sw.set(SwitcherSample{
		PipelineLastNs: 20000,
		E2ELastNs:      40000,
		Sources:        map[string]SourceSample{},
		NodeTimings:    map[string]int64{},
		FrameBudgetNs:  33333,
	})
	mx.set(MixerSample{MixCycleLastNs: 10000})

	for i := 0; i < 60; i++ {
		s.tick()
	}

	snap := s.Snapshot("before")
	if snap.Baseline == nil {
		t.Fatal("snapshot baseline diff is nil")
	}
	if snap.Baseline.Name != "before" {
		t.Errorf("baseline name = %q, want %q", snap.Baseline.Name, "before")
	}
	// Pipeline went from 10000 to 20000 → delta = 10000
	if snap.Baseline.PipelineDiff.MeanNsDelta != 10000 {
		t.Errorf("pipeline mean delta = %d, want 10000", snap.Baseline.PipelineDiff.MeanNsDelta)
	}
	if snap.Baseline.PipelineDiff.PctChange != 100.0 {
		t.Errorf("pipeline pct change = %f, want 100.0", snap.Baseline.PipelineDiff.PctChange)
	}
}

func TestSampler_Baseline_MaxEviction(t *testing.T) {
	sw := &mockSwitcherPerf{sample: SwitcherSample{
		Sources:     map[string]SourceSample{},
		NodeTimings: map[string]int64{},
	}}
	mx := &mockMixerPerf{}
	out := &mockOutputPerf{}

	s := NewSampler(sw, mx, out)
	s.tick()

	// Save 11 baselines — should cap at 10
	for i := 0; i < 11; i++ {
		s.SaveBaseline(string(rune('A' + i)))
	}

	s.mu.RLock()
	count := len(s.baselines)
	s.mu.RUnlock()

	if count != 10 {
		t.Errorf("baseline count = %d, want 10", count)
	}
}

func TestSampler_ConcurrentAccess(t *testing.T) {
	sw := &mockSwitcherPerf{sample: SwitcherSample{
		Sources: map[string]SourceSample{
			"cam1": {DecodeLastNs: 5000, Health: "active"},
		},
		PipelineLastNs: 10000,
		NodeTimings:    map[string]int64{"encode": 3000},
		E2ELastNs:      15000,
		FrameBudgetNs:  33333,
	}}
	mx := &mockMixerPerf{sample: MixerSample{MixCycleLastNs: 2000}}
	out := &mockOutputPerf{sample: OutputSample{ViewerVideoSent: 500}}

	s := NewSampler(sw, mx, out)

	var wg sync.WaitGroup
	// Concurrent ticks
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			s.tick()
		}
	}()

	// Concurrent snapshots
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = s.Snapshot("")
		}
	}()

	// Concurrent baseline saves
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			s.SaveBaseline("test")
			_ = s.Snapshot("test")
		}
	}()

	wg.Wait()
}

func TestSampler_Snapshot_WithBaseline(t *testing.T) {
	sw := &mockSwitcherPerf{sample: SwitcherSample{
		PipelineLastNs: 10000,
		E2ELastNs:      20000,
		Sources:        map[string]SourceSample{},
		NodeTimings:    map[string]int64{},
		FrameBudgetNs:  33333,
	}}
	mx := &mockMixerPerf{sample: MixerSample{MixCycleLastNs: 5000}}
	out := &mockOutputPerf{}

	s := NewSampler(sw, mx, out)

	// Populate rings
	for i := 0; i < 10; i++ {
		s.tick()
	}

	s.SaveBaseline("ref")

	snap := s.Snapshot("ref")
	if snap.Baseline == nil {
		t.Fatal("expected baseline diff, got nil")
	}
	if snap.Baseline.Name != "ref" {
		t.Errorf("baseline name = %q, want %q", snap.Baseline.Name, "ref")
	}
	// Same data, so deltas should be zero
	if snap.Baseline.PipelineDiff.MeanNsDelta != 0 {
		t.Errorf("pipeline mean delta = %d, want 0", snap.Baseline.PipelineDiff.MeanNsDelta)
	}
	if snap.Baseline.E2EDiff.MeanNsDelta != 0 {
		t.Errorf("e2e mean delta = %d, want 0", snap.Baseline.E2EDiff.MeanNsDelta)
	}
	if snap.Baseline.MixCycleDiff.MeanNsDelta != 0 {
		t.Errorf("mix cycle mean delta = %d, want 0", snap.Baseline.MixCycleDiff.MeanNsDelta)
	}
}

func TestSampler_Snapshot_UnknownBaseline(t *testing.T) {
	sw := &mockSwitcherPerf{sample: SwitcherSample{
		Sources:     map[string]SourceSample{},
		NodeTimings: map[string]int64{},
	}}
	mx := &mockMixerPerf{}
	out := &mockOutputPerf{}

	s := NewSampler(sw, mx, out)
	s.tick()

	snap := s.Snapshot("nonexistent")
	if snap.Baseline != nil {
		t.Errorf("expected nil baseline for unknown name, got %+v", snap.Baseline)
	}
}

func TestSampler_SubStageBreakdown(t *testing.T) {
	mock := &mockSwitcherPerf{
		sample: SwitcherSample{
			PipelineLastNs: 100_000,
			E2ELastNs:      14_000_000,
			DecodeQueueNs:  500_000,
			DecodeNs:       1_000_000,
			SyncWaitNs:     12_000_000,
			ProcQueueNs:    200_000,
			FrameBudgetNs:  33_333_333,
			Sources:        map[string]SourceSample{},
			NodeTimings:    map[string]int64{},
		},
	}
	s := NewSampler(mock, &mockMixerPerf{}, &mockOutputPerf{})
	s.tick()

	snap := s.Snapshot("")
	if snap.E2E.Stages == nil {
		t.Fatal("E2E.Stages should not be nil")
	}
	stages := snap.E2E.Stages

	if stages.DecodeQueue.Current.LastNs != 500_000 {
		t.Errorf("DecodeQueue current: got %d, want 500000", stages.DecodeQueue.Current.LastNs)
	}
	if stages.Decode.Current.LastNs != 1_000_000 {
		t.Errorf("Decode current: got %d, want 1000000", stages.Decode.Current.LastNs)
	}
	if stages.SyncWait.Current.LastNs != 12_000_000 {
		t.Errorf("SyncWait current: got %d, want 12000000", stages.SyncWait.Current.LastNs)
	}
	if stages.ProcQueue.Current.LastNs != 200_000 {
		t.Errorf("ProcQueue current: got %d, want 200000", stages.ProcQueue.Current.LastNs)
	}

	// Verify windowed stats
	w1s := stages.SyncWait.Windows
	if w1s.W1s.MeanNs != 12_000_000 {
		t.Errorf("SyncWait 1s mean: got %d, want 12000000", w1s.W1s.MeanNs)
	}
}

func TestSampler_StaleMapEntryCleanup(t *testing.T) {
	// Verify that decodeRings and nodeRings entries are removed when the
	// corresponding source/node disappears from the current sample.
	// Without cleanup, these maps grow unboundedly as sources are added/removed.
	sw := &mockSwitcherPerf{sample: SwitcherSample{
		Sources: map[string]SourceSample{
			"cam1": {DecodeLastNs: 5000, Health: "active"},
			"cam2": {DecodeLastNs: 6000, Health: "active"},
		},
		PipelineLastNs: 10000,
		NodeTimings: map[string]int64{
			"encode":  3000,
			"keyer":   1000,
		},
		E2ELastNs:     15000,
		FrameBudgetNs: 33333,
	}}
	mx := &mockMixerPerf{sample: MixerSample{MixCycleLastNs: 2000}}
	out := &mockOutputPerf{}

	s := NewSampler(sw, mx, out)

	// Tick with both sources and nodes present.
	s.tick()

	s.mu.RLock()
	require.Contains(t, s.decodeRings, "cam1")
	require.Contains(t, s.decodeRings, "cam2")
	require.Contains(t, s.nodeRings, "encode")
	require.Contains(t, s.nodeRings, "keyer")
	s.mu.RUnlock()

	// Remove cam2 and keyer from the sample (simulating source removal).
	sw.set(SwitcherSample{
		Sources: map[string]SourceSample{
			"cam1": {DecodeLastNs: 5000, Health: "active"},
		},
		PipelineLastNs: 10000,
		NodeTimings:    map[string]int64{"encode": 3000},
		E2ELastNs:      15000,
		FrameBudgetNs:  33333,
	})

	// Tick again — stale entries should be cleaned up.
	s.tick()

	s.mu.RLock()
	require.Contains(t, s.decodeRings, "cam1", "cam1 should still be present")
	require.NotContains(t, s.decodeRings, "cam2", "cam2 should be removed after disappearing from sample")
	require.Contains(t, s.nodeRings, "encode", "encode should still be present")
	require.NotContains(t, s.nodeRings, "keyer", "keyer should be removed after disappearing from sample")
	s.mu.RUnlock()
}

func TestSamplerDoubleStopNoPanic(t *testing.T) {
	sw := &mockSwitcherPerf{sample: SwitcherSample{
		Sources:     map[string]SourceSample{},
		NodeTimings: map[string]int64{},
	}}
	mx := &mockMixerPerf{}
	out := &mockOutputPerf{}

	s := NewSampler(sw, mx, out)
	s.Start()
	s.Stop()

	// Second Stop must not panic (double close on channel).
	require.NotPanics(t, func() {
		s.Stop()
	})
}
