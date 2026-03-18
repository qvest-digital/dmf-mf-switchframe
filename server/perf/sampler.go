package perf

import (
	"sort"
	"strings"
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
	ProcDropped        int64
	DecodeQueueNs      int64
	DecodeNs           int64
	SyncWaitNs         int64
	ProcQueueNs        int64

	// Frame synchronizer stats
	FrameSyncReleaseFPS  float64
	FrameSyncSourceCount int
}

// SourceSample holds per-source performance data.
type SourceSample struct {
	DecodeLastNs  int64
	DecodeDrops   int64
	AvgFPS        float64
	AvgFrameBytes int
	Health        string
	IngestFPS     float64

	// RawFrameCount is a monotonic counter of raw video frames ingested.
	// The sampler computes IngestFPS from deltas between ticks.
	RawFrameCount int64

	// SRT connection stats (populated only for srt: sources)
	SRTRTTMs     float64
	SRTLossRate  float64
	SRTRecvBufMs float64
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
	FramesMixed        int64
	MaxInterFrameGapNs int64
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

// PreviewEncoderStats holds point-in-time stats for a single preview encoder.
type PreviewEncoderStats struct {
	FramesIn      int64
	FramesOut     int64
	FramesDropped int64
	LastEncodeMs  float64
	AvgEncodeMs   float64
}

// ingestFPSTracker holds per-source state for computing raw ingest FPS
// from monotonic frame count deltas between sampler ticks.
type ingestFPSTracker struct {
	lastCount int64
	lastTime  time.Time
}

// Sampler collects performance samples at 1Hz and maintains rolling
// ring buffers for windowed statistics.
type Sampler struct {
	mu sync.RWMutex

	// Per-source decode rings (key = source key)
	decodeRings map[string]*RingStat

	// Per-source raw ingest FPS tracking (key = source key).
	// Stores the raw frame count from the previous tick for delta computation.
	ingestFPSState map[string]*ingestFPSTracker

	// Pipeline total + per-node rings
	pipelineRing *RingStat
	nodeRings    map[string]*RingStat

	// E2E latency, audio mix cycle, broadcast gap
	e2eRing          *RingStat
	mixCycleRing     *RingStat
	broadcastGapRing *RingStat

	// Sub-stage latency rings
	decodeQueueRing *RingStat
	decodeRing      *RingStat
	syncWaitRing    *RingStat
	procQueueRing   *RingStat

	// Provider references
	switcher SwitcherPerf
	mixer    MixerPerf
	output   OutputPerf

	// Optional SRT stats provider (called per srt: source on each tick)
	srtStats func(key string) (rttMs, lossRate, recvBufMs float64, ok bool)

	// Optional preview encoder stats provider (called once per tick)
	previewStats     func() map[string]PreviewEncoderStats
	lastPreviewStats map[string]PreviewEncoderStats

	// Latest sample cache (for snapshot current values)
	lastSwitcherSample SwitcherSample
	lastMixerSample    MixerSample
	lastOutputSample   OutputSample

	// Baseline storage (max 10, in-memory, FIFO eviction)
	baselines     map[string]*BaselineSnapshot
	baselineOrder []string // insertion order for FIFO eviction

	// Lifecycle
	startTime time.Time
	done      chan struct{}
	wg        sync.WaitGroup
	stopOnce  sync.Once
}

// NewSampler creates a Sampler that reads from the given providers.
func NewSampler(sw SwitcherPerf, mx MixerPerf, out OutputPerf) *Sampler {
	return &Sampler{
		decodeRings:      make(map[string]*RingStat),
		ingestFPSState:   make(map[string]*ingestFPSTracker),
		pipelineRing:     &RingStat{},
		nodeRings:        make(map[string]*RingStat),
		e2eRing:          &RingStat{},
		mixCycleRing:     &RingStat{},
		broadcastGapRing: &RingStat{},
		decodeQueueRing:  &RingStat{},
		decodeRing:       &RingStat{},
		syncWaitRing:     &RingStat{},
		procQueueRing:    &RingStat{},
		switcher:         sw,
		mixer:            mx,
		output:           out,
		baselines:        make(map[string]*BaselineSnapshot),
		startTime:        time.Now(),
		done:             make(chan struct{}),
	}
}

// SetSRTStats registers an optional function that returns SRT connection
// stats for a given source key. Called once per srt: source on each tick.
func (s *Sampler) SetSRTStats(fn func(key string) (rttMs, lossRate, recvBufMs float64, ok bool)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.srtStats = fn
}

// SetPreviewStats registers an optional function that returns preview encoder
// stats for all active preview encoders. Called once per tick.
func (s *Sampler) SetPreviewStats(fn func() map[string]PreviewEncoderStats) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.previewStats = fn
}

// Start launches the 1Hz sampling goroutine.
func (s *Sampler) Start() {
	s.wg.Add(1)
	go s.tickLoop()
}

// Stop signals the sampling goroutine to exit and waits.
// Safe to call multiple times — subsequent calls are no-ops.
func (s *Sampler) Stop() {
	s.stopOnce.Do(func() {
		close(s.done)
		s.wg.Wait()
	})
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

	// Enrich SRT sources with connection stats
	if s.srtStats != nil {
		for key, src := range sw.Sources {
			if strings.HasPrefix(key, "srt:") {
				if rtt, loss, buf, ok := s.srtStats(key); ok {
					src.SRTRTTMs = rtt
					src.SRTLossRate = loss
					src.SRTRecvBufMs = buf
					sw.Sources[key] = src
				}
			}
		}
	}

	// Compute per-source raw ingest FPS from frame count deltas
	now := time.Now()
	for key, src := range sw.Sources {
		tracker, ok := s.ingestFPSState[key]
		if !ok {
			tracker = &ingestFPSTracker{}
			s.ingestFPSState[key] = tracker
		}
		if !tracker.lastTime.IsZero() {
			elapsed := now.Sub(tracker.lastTime).Seconds()
			if elapsed > 0 {
				delta := src.RawFrameCount - tracker.lastCount
				src.IngestFPS = float64(delta) / elapsed
				sw.Sources[key] = src
			}
		}
		tracker.lastCount = src.RawFrameCount
		tracker.lastTime = now
	}
	// Remove stale ingest FPS trackers
	for key := range s.ingestFPSState {
		if _, ok := sw.Sources[key]; !ok {
			delete(s.ingestFPSState, key)
		}
	}

	// Collect preview encoder stats (point-in-time)
	if s.previewStats != nil {
		s.lastPreviewStats = s.previewStats()
	}

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
	// Remove stale source entries no longer in the current sample.
	for key := range s.decodeRings {
		if _, ok := sw.Sources[key]; !ok {
			delete(s.decodeRings, key)
		}
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
	// Remove stale node entries no longer in the current sample.
	for name := range s.nodeRings {
		if _, ok := sw.NodeTimings[name]; !ok {
			delete(s.nodeRings, name)
		}
	}

	// E2E, mix cycle, broadcast gap
	s.e2eRing.Push(sw.E2ELastNs)
	s.mixCycleRing.Push(mx.MixCycleLastNs)
	s.broadcastGapRing.Push(sw.BroadcastGapNs)

	// Sub-stage breakdown
	s.decodeQueueRing.Push(sw.DecodeQueueNs)
	s.decodeRing.Push(sw.DecodeNs)
	s.syncWaitRing.Push(sw.SyncWaitNs)
	s.procQueueRing.Push(sw.ProcQueueNs)
}

const maxBaselines = 10

// SaveBaseline captures the current 60s window stats as a named baseline.
func (s *Sampler) SaveBaseline(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Evict oldest (FIFO) if at max capacity
	if len(s.baselines) >= maxBaselines && len(s.baselineOrder) > 0 {
		oldest := s.baselineOrder[0]
		s.baselineOrder = s.baselineOrder[1:]
		delete(s.baselines, oldest)
	}

	// Remove existing entry from order if overwriting
	for i, n := range s.baselineOrder {
		if n == name {
			s.baselineOrder = append(s.baselineOrder[:i], s.baselineOrder[i+1:]...)
			break
		}
	}

	snap := s.buildBaselineLocked()
	s.baselines[name] = snap
	s.baselineOrder = append(s.baselineOrder, name)
}

// DeleteBaseline removes a named baseline.
func (s *Sampler) DeleteBaseline(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.baselines, name)
	for i, n := range s.baselineOrder {
		if n == name {
			s.baselineOrder = append(s.baselineOrder[:i], s.baselineOrder[i+1:]...)
			break
		}
	}
}
