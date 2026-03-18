package switcher

// PerfSwitcherSample mirrors perf.SwitcherSample for interface satisfaction.
// We can't import the perf package from switcher (circular dependency), so
// we define compatible types here. The perf.Sampler wraps these via a thin adapter.
type PerfSwitcherSample struct {
	Sources            map[string]PerfSourceSample
	PipelineLastNs     int64
	NodeTimings        map[string]int64
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

// PerfSourceSample mirrors perf.SourceSample.
type PerfSourceSample struct {
	DecodeLastNs  int64
	DecodeDrops   int64
	AvgFPS        float64
	AvgFrameBytes int
	Health        string

	// RawFrameCount is the monotonic raw video ingest counter.
	// The perf sampler computes IngestFPS from deltas between ticks.
	RawFrameCount int64
}

// PerfSample returns a performance snapshot of the switcher's current state.
// Safe for concurrent access from any goroutine.
func (s *Switcher) PerfSample() PerfSwitcherSample {
	s.mu.RLock()
	sources := make(map[string]PerfSourceSample, len(s.sources))
	for key, ss := range s.sources {
		sample := PerfSourceSample{Health: string(s.health.rawStatus(key))}
		if ss.viewer != nil {
			if sd := ss.viewer.srcDecoder.Load(); sd != nil {
				lastNs, _, drops := sd.PerfStats()
				avgSize, avgFPS := sd.Stats()
				sample.DecodeLastNs = lastNs
				sample.DecodeDrops = drops
				sample.AvgFPS = avgFPS
				sample.AvgFrameBytes = int(avgSize)
			}
		}
		sample.RawFrameCount = ss.rawFrameCount.Load()
		sources[key] = sample
	}

	// Frame sync stats
	var frameSyncReleaseFPS float64
	var frameSyncSourceCount int
	if fs := s.frameSync; fs != nil {
		frameSyncReleaseFPS = fs.ReleaseFPS()
		frameSyncSourceCount = fs.SourceCount()
	}

	s.mu.RUnlock()

	// Read pipeline node timings
	nodeTimings := make(map[string]int64)
	if p := s.pipeline.Load(); p != nil {
		snap := p.Snapshot()
		if nodes, ok := snap["active_nodes"].([]map[string]any); ok {
			for _, node := range nodes {
				name, _ := node["name"].(string)
				lastNs, _ := node["last_ns"].(int64)
				if name != "" {
					nodeTimings[name] = lastNs
				}
			}
		}
	}

	return PerfSwitcherSample{
		Sources:              sources,
		PipelineLastNs:       s.videoProcLastNano.Load(),
		NodeTimings:          nodeTimings,
		E2ELastNs:            s.lastE2ENs.Load(),
		QueueLen:             len(s.videoProcCh),
		OutputFPS:            float64(s.outputFPSLastSecond.Load()),
		BroadcastGapNs:       s.maxBroadcastIntervalNano.Load(),
		VideoBroadcast:       s.videoBroadcastCount.Load(),
		DeadlineViolations:   s.deadlineViolations.Load(),
		FrameBudgetNs:        s.frameBudgetNs.Load(),
		ProcDropped:          s.videoProcDropped.Load(),
		DecodeQueueNs:        s.lastDecodeQueueNs.Load(),
		DecodeNs:             s.lastDecodeNs.Load(),
		SyncWaitNs:           s.lastSyncWaitNs.Load(),
		ProcQueueNs:          s.lastProcQueueNs.Load(),
		FrameSyncReleaseFPS:  frameSyncReleaseFPS,
		FrameSyncSourceCount: frameSyncSourceCount,
	}
}
