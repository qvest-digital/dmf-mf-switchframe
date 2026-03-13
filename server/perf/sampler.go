package perf

import (
	"sort"
	"sync"
	"time"
)

// RingStat holds a circular buffer of int64 samples (one per second tick).
type RingStat struct {
	buf  [60]int64 // 60 seconds of history
	head int
	len  int
}

func (rs *RingStat) Push(v int64) {
	rs.buf[rs.head] = v
	rs.head = (rs.head + 1) % 60
	if rs.len < 60 {
		rs.len++
	}
}

// WindowStats holds min/max/mean/p95 for a time window.
type WindowStats struct {
	MinNs  int64 `json:"min_ns"`
	MaxNs  int64 `json:"max_ns"`
	MeanNs int64 `json:"mean_ns"`
	P95Ns  int64 `json:"p95_ns"`
}

func (rs *RingStat) Window(n int) WindowStats {
	if n > rs.len {
		n = rs.len
	}
	if n == 0 {
		return WindowStats{}
	}

	samples := make([]int64, n)
	// Read last n samples from ring buffer (oldest first)
	start := (rs.head - n + 60) % 60
	for i := 0; i < n; i++ {
		samples[i] = rs.buf[(start+i)%60]
	}

	var sum int64
	minV, maxV := samples[0], samples[0]
	for _, v := range samples {
		sum += v
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}

	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	p95Idx := int(float64(len(samples)-1) * 0.95)

	return WindowStats{
		MinNs:  minV,
		MaxNs:  maxV,
		MeanNs: sum / int64(n),
		P95Ns:  samples[p95Idx],
	}
}

// SwitcherPerf provides switcher performance samples.
type SwitcherPerf interface {
	PerfSample() SwitcherSample
}

// SwitcherSample holds per-tick switcher performance data.
type SwitcherSample struct {
	Sources            map[string]SourceSample
	PipelineLastNs     int64
	NodeTimings        map[string]int64 // node name -> last_ns
	E2ELastNs          int64
	QueueLen           int
	OutputFPS          float64
	BroadcastGapNs     int64
	VideoBroadcast     int64
	DeadlineViolations int64
	FrameBudgetNs      int64
}

// SourceSample holds per-source performance data.
type SourceSample struct {
	DecodeLastNs  int64
	DecodeDrops   int64
	AvgFPS        float64
	AvgFrameBytes int
	Health        string
}

// MixerPerf provides mixer performance samples.
type MixerPerf interface {
	PerfSample() MixerSample
}

// MixerSample holds per-tick mixer performance data.
type MixerSample struct {
	Mode               string
	MixCycleLastNs     int64
	FramesOutput       int64
	FramesPassthrough  int64
	FramesMixed        int64
	MaxInterFrameGapNs int64
	DeadlineFlushes    int64
	DecodeErrors       int64
	EncodeErrors       int64
	MomentaryLUFS      float64
	ShortTermLUFS      float64
	IntegratedLUFS     float64
}

// OutputPerf provides output performance samples.
type OutputPerf interface {
	PerfSample() OutputSample
}

// OutputSample holds per-tick output performance data.
type OutputSample struct {
	ViewerVideoSent    int64
	ViewerVideoDropped int64
	ViewerAudioSent    int64
	ViewerAudioDropped int64
	MuxerPTS           int64
	SRTBytesWritten    int64
	SRTOverflowCount   int64
	RecordingActive    bool
}

// Sampler collects performance samples at 1Hz and maintains rolling
// ring buffers for windowed statistics.
type Sampler struct {
	mu sync.RWMutex

	// Per-source decode rings (key = source key)
	decodeRings map[string]*RingStat

	// Pipeline total + per-node rings
	pipelineRing *RingStat
	nodeRings    map[string]*RingStat

	// E2E latency, audio mix cycle, broadcast gap
	e2eRing          *RingStat
	mixCycleRing     *RingStat
	broadcastGapRing *RingStat

	// Provider references
	switcher SwitcherPerf
	mixer    MixerPerf
	output   OutputPerf

	// Latest sample cache (for snapshot current values)
	lastSwitcherSample SwitcherSample
	lastMixerSample    MixerSample
	lastOutputSample   OutputSample

	// Baseline storage (max 10, in-memory)
	baselines map[string]*BaselineSnapshot

	// Lifecycle
	startTime time.Time
	done      chan struct{}
	wg        sync.WaitGroup
}

// NewSampler creates a Sampler that reads from the given providers.
func NewSampler(sw SwitcherPerf, mx MixerPerf, out OutputPerf) *Sampler {
	return &Sampler{
		decodeRings:      make(map[string]*RingStat),
		pipelineRing:     &RingStat{},
		nodeRings:        make(map[string]*RingStat),
		e2eRing:          &RingStat{},
		mixCycleRing:     &RingStat{},
		broadcastGapRing: &RingStat{},
		switcher:         sw,
		mixer:            mx,
		output:           out,
		baselines:        make(map[string]*BaselineSnapshot),
		startTime:        time.Now(),
		done:             make(chan struct{}),
	}
}

// Start launches the 1Hz sampling goroutine.
func (s *Sampler) Start() {
	s.wg.Add(1)
	go s.tickLoop()
}

// Stop signals the sampling goroutine to exit and waits.
func (s *Sampler) Stop() {
	close(s.done)
	s.wg.Wait()
}

func (s *Sampler) tickLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			s.tick()
		}
	}
}

func (s *Sampler) tick() {
	sw := s.switcher.PerfSample()
	mx := s.mixer.PerfSample()
	out := s.output.PerfSample()

	s.mu.Lock()
	defer s.mu.Unlock()

	s.lastSwitcherSample = sw
	s.lastMixerSample = mx
	s.lastOutputSample = out

	// Per-source decode timing
	for key, src := range sw.Sources {
		ring, ok := s.decodeRings[key]
		if !ok {
			ring = &RingStat{}
			s.decodeRings[key] = ring
		}
		ring.Push(src.DecodeLastNs)
	}

	// Pipeline
	s.pipelineRing.Push(sw.PipelineLastNs)

	// Per-node timings
	for name, ns := range sw.NodeTimings {
		ring, ok := s.nodeRings[name]
		if !ok {
			ring = &RingStat{}
			s.nodeRings[name] = ring
		}
		ring.Push(ns)
	}

	// E2E, mix cycle, broadcast gap
	s.e2eRing.Push(sw.E2ELastNs)
	s.mixCycleRing.Push(mx.MixCycleLastNs)
	s.broadcastGapRing.Push(sw.BroadcastGapNs)
}

const maxBaselines = 10

// SaveBaseline captures the current 60s window stats as a named baseline.
func (s *Sampler) SaveBaseline(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Evict oldest if at max capacity
	if len(s.baselines) >= maxBaselines {
		// Delete the first key found (deterministic enough for a max-10 map)
		for k := range s.baselines {
			delete(s.baselines, k)
			break
		}
	}

	snap := s.buildBaselineLocked()
	s.baselines[name] = snap
}

// DeleteBaseline removes a named baseline.
func (s *Sampler) DeleteBaseline(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.baselines, name)
}
