package perf

import "time"

// BaselineSnapshot stores the 60s window stats at the time the baseline was saved.
type BaselineSnapshot struct {
	SavedAt  time.Time   `json:"saved_at"`
	Pipeline WindowStats `json:"pipeline"`
	E2E      WindowStats `json:"e2e"`
	MixCycle WindowStats `json:"mix_cycle"`
}

func (s *Sampler) buildBaselineLocked() *BaselineSnapshot {
	return &BaselineSnapshot{
		SavedAt:  time.Now(),
		Pipeline: s.pipelineRing.Window(60),
		E2E:      s.e2eRing.Window(60),
		MixCycle: s.mixCycleRing.Window(60),
	}
}

// BaselineDiff represents the delta between current and baseline stats.
type BaselineDiff struct {
	Name         string      `json:"name"`
	SavedAt      time.Time   `json:"saved_at"`
	PipelineDiff *WindowDiff `json:"pipeline"`
	E2EDiff      *WindowDiff `json:"e2e"`
	MixCycleDiff *WindowDiff `json:"mix_cycle"`
}

// WindowDiff holds deltas between two WindowStats.
type WindowDiff struct {
	MeanNsDelta int64   `json:"mean_ns_delta"`
	P95NsDelta  int64   `json:"p95_ns_delta"`
	PctChange   float64 `json:"pct_change"`
}

func diffWindow(current, baseline WindowStats) *WindowDiff {
	d := &WindowDiff{
		MeanNsDelta: current.MeanNs - baseline.MeanNs,
		P95NsDelta:  current.P95Ns - baseline.P95Ns,
	}
	if baseline.MeanNs > 0 {
		d.PctChange = float64(d.MeanNsDelta) / float64(baseline.MeanNs) * 100
	}
	return d
}

// PerfWindows holds the 1s/10s/60s windowed stats.
type PerfWindows struct {
	W1s  WindowStats `json:"1s"`
	W10s WindowStats `json:"10s"`
	W60s WindowStats `json:"60s"`
}

func ringToWindows(r *RingStat) PerfWindows {
	return PerfWindows{
		W1s:  r.Window(1),
		W10s: r.Window(10),
		W60s: r.Window(60),
	}
}

// PerfSnapshot is the top-level JSON response for GET /api/perf.
type PerfSnapshot struct {
	Timestamp     time.Time `json:"timestamp"`
	UptimeMs      int64     `json:"uptime_ms"`
	FrameBudgetNs int64     `json:"frame_budget_ns"`

	Sources map[string]PerfSourceSnapshot `json:"sources"`

	Pipeline PerfPipelineSnapshot `json:"pipeline"`

	E2E PerfE2ESnapshot `json:"e2e"`

	Audio PerfAudioSnapshot `json:"audio"`

	Broadcast PerfBroadcastSnapshot `json:"broadcast"`

	Output PerfOutputSnapshot `json:"output"`

	Baseline *BaselineDiff `json:"baseline"`
}

// PerfSourceSnapshot holds per-source decode performance.
type PerfSourceSnapshot struct {
	Health string           `json:"health"`
	Decode PerfSourceDecode `json:"decode"`
}

// PerfSourceDecode holds current + windowed decode stats.
type PerfSourceDecode struct {
	Current PerfSourceDecodeCurrent `json:"current"`
	Windows PerfWindows             `json:"windows"`
}

// PerfSourceDecodeCurrent holds the latest decode sample.
type PerfSourceDecodeCurrent struct {
	LastNs        int64   `json:"last_ns"`
	Drops         int64   `json:"drops"`
	AvgFPS        float64 `json:"avg_fps"`
	AvgFrameBytes int     `json:"avg_frame_bytes"`
}

// PerfPipelineSnapshot holds pipeline performance data.
type PerfPipelineSnapshot struct {
	Current            PerfPipelineCurrent         `json:"current"`
	Windows            PerfWindows                 `json:"windows"`
	Nodes              map[string]PerfNodeSnapshot `json:"nodes"`
	DeadlineViolations int64                       `json:"deadline_violations"`
	BudgetPct          float64                     `json:"budget_pct"`
}

// PerfPipelineCurrent holds current pipeline metrics.
type PerfPipelineCurrent struct {
	LastNs   int64 `json:"last_ns"`
	QueueLen int   `json:"queue_len"`
}

// PerfNodeSnapshot holds per-node timing with windows.
type PerfNodeSnapshot struct {
	Current PerfNodeCurrent `json:"current"`
	Windows PerfWindows     `json:"windows"`
}

// PerfNodeCurrent holds the latest node timing.
type PerfNodeCurrent struct {
	LastNs int64 `json:"last_ns"`
}

// PerfE2ESnapshot holds end-to-end latency data.
type PerfE2ESnapshot struct {
	Current PerfE2ECurrent `json:"current"`
	Windows PerfWindows    `json:"windows"`
}

// PerfE2ECurrent holds the latest E2E sample.
type PerfE2ECurrent struct {
	LastNs int64 `json:"last_ns"`
}

// PerfAudioSnapshot holds audio performance data.
type PerfAudioSnapshot struct {
	Mode     string            `json:"mode"`
	MixCycle PerfMixCycle      `json:"mix_cycle"`
	Counters PerfAudioCounters `json:"counters"`
	Loudness PerfLoudness      `json:"loudness"`
}

// PerfMixCycle holds mix cycle timing.
type PerfMixCycle struct {
	Current PerfMixCycleCurrent `json:"current"`
	Windows PerfWindows         `json:"windows"`
}

// PerfMixCycleCurrent holds the latest mix cycle sample.
type PerfMixCycleCurrent struct {
	LastNs int64 `json:"last_ns"`
}

// PerfAudioCounters holds audio frame counts.
type PerfAudioCounters struct {
	Output       int64 `json:"output"`
	Passthrough  int64 `json:"passthrough"`
	Mixed        int64 `json:"mixed"`
	DecodeErrors int64 `json:"decode_errors"`
	EncodeErrors int64 `json:"encode_errors"`
}

// PerfLoudness holds LUFS meter values.
type PerfLoudness struct {
	MomentaryLUFS  float64 `json:"momentary_lufs"`
	ShortTermLUFS  float64 `json:"short_term_lufs"`
	IntegratedLUFS float64 `json:"integrated_lufs"`
}

// PerfBroadcastSnapshot holds broadcast frame delivery data.
type PerfBroadcastSnapshot struct {
	Frames    int64   `json:"frames"`
	OutputFPS float64 `json:"output_fps"`
	Gap       PerfGap `json:"gap"`
}

// PerfGap holds broadcast gap data.
type PerfGap struct {
	Current PerfGapCurrent `json:"current"`
	Windows PerfWindows    `json:"windows"`
}

// PerfGapCurrent holds the latest broadcast gap.
type PerfGapCurrent struct {
	MaxNs int64 `json:"max_ns"`
}

// PerfOutputSnapshot holds output subsystem data.
type PerfOutputSnapshot struct {
	Viewer    PerfViewerSnapshot    `json:"viewer"`
	MuxerPTS  int64                 `json:"muxer_pts"`
	SRT       PerfSRTSnapshot       `json:"srt"`
	Recording PerfRecordingSnapshot `json:"recording"`
}

// PerfViewerSnapshot holds viewer delivery data.
type PerfViewerSnapshot struct {
	VideoSent    int64 `json:"video_sent"`
	VideoDropped int64 `json:"video_dropped"`
	AudioDropped int64 `json:"audio_dropped"`
}

// PerfSRTSnapshot holds SRT output data.
type PerfSRTSnapshot struct {
	BytesWritten  int64 `json:"bytes_written"`
	OverflowCount int64 `json:"overflow_count"`
}

// PerfRecordingSnapshot holds recording state.
type PerfRecordingSnapshot struct {
	Active bool `json:"active"`
}

// Snapshot computes the full PerfSnapshot. If baselineName is non-empty and
// a baseline with that name exists, includes a diff.
func (s *Sampler) Snapshot(baselineName string) *PerfSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sw := s.lastSwitcherSample
	mx := s.lastMixerSample
	out := s.lastOutputSample

	// Build per-source snapshots
	sources := make(map[string]PerfSourceSnapshot, len(sw.Sources))
	for key, src := range sw.Sources {
		ring := s.decodeRings[key]
		var windows PerfWindows
		if ring != nil {
			windows = ringToWindows(ring)
		}
		sources[key] = PerfSourceSnapshot{
			Health: src.Health,
			Decode: PerfSourceDecode{
				Current: PerfSourceDecodeCurrent{
					LastNs:        src.DecodeLastNs,
					Drops:         src.DecodeDrops,
					AvgFPS:        src.AvgFPS,
					AvgFrameBytes: src.AvgFrameBytes,
				},
				Windows: windows,
			},
		}
	}

	// Build per-node snapshots
	nodes := make(map[string]PerfNodeSnapshot, len(sw.NodeTimings))
	for name, ns := range sw.NodeTimings {
		ring := s.nodeRings[name]
		var windows PerfWindows
		if ring != nil {
			windows = ringToWindows(ring)
		}
		nodes[name] = PerfNodeSnapshot{
			Current: PerfNodeCurrent{LastNs: ns},
			Windows: windows,
		}
	}

	// Budget percentage (pipeline p95 / frame budget)
	var budgetPct float64
	if sw.FrameBudgetNs > 0 {
		pipeW60 := s.pipelineRing.Window(60)
		budgetPct = float64(pipeW60.P95Ns) / float64(sw.FrameBudgetNs) * 100
	}

	snap := &PerfSnapshot{
		Timestamp:     time.Now(),
		UptimeMs:      time.Since(s.startTime).Milliseconds(),
		FrameBudgetNs: sw.FrameBudgetNs,
		Sources:       sources,
		Pipeline: PerfPipelineSnapshot{
			Current: PerfPipelineCurrent{
				LastNs:   sw.PipelineLastNs,
				QueueLen: sw.QueueLen,
			},
			Windows:            ringToWindows(s.pipelineRing),
			Nodes:              nodes,
			DeadlineViolations: sw.DeadlineViolations,
			BudgetPct:          budgetPct,
		},
		E2E: PerfE2ESnapshot{
			Current: PerfE2ECurrent{LastNs: sw.E2ELastNs},
			Windows: ringToWindows(s.e2eRing),
		},
		Audio: PerfAudioSnapshot{
			Mode: mx.Mode,
			MixCycle: PerfMixCycle{
				Current: PerfMixCycleCurrent{LastNs: mx.MixCycleLastNs},
				Windows: ringToWindows(s.mixCycleRing),
			},
			Counters: PerfAudioCounters{
				Output:       mx.FramesOutput,
				Passthrough:  mx.FramesPassthrough,
				Mixed:        mx.FramesMixed,
				DecodeErrors: mx.DecodeErrors,
				EncodeErrors: mx.EncodeErrors,
			},
			Loudness: PerfLoudness{
				MomentaryLUFS:  mx.MomentaryLUFS,
				ShortTermLUFS:  mx.ShortTermLUFS,
				IntegratedLUFS: mx.IntegratedLUFS,
			},
		},
		Broadcast: PerfBroadcastSnapshot{
			Frames:    sw.VideoBroadcast,
			OutputFPS: sw.OutputFPS,
			Gap: PerfGap{
				Current: PerfGapCurrent{MaxNs: sw.BroadcastGapNs},
				Windows: ringToWindows(s.broadcastGapRing),
			},
		},
		Output: PerfOutputSnapshot{
			Viewer: PerfViewerSnapshot{
				VideoSent:    out.ViewerVideoSent,
				VideoDropped: out.ViewerVideoDropped,
				AudioDropped: out.ViewerAudioDropped,
			},
			MuxerPTS: out.MuxerPTS,
			SRT: PerfSRTSnapshot{
				BytesWritten:  out.SRTBytesWritten,
				OverflowCount: out.SRTOverflowCount,
			},
			Recording: PerfRecordingSnapshot{
				Active: out.RecordingActive,
			},
		},
	}

	// Baseline diff
	if baselineName != "" {
		if bl, ok := s.baselines[baselineName]; ok {
			snap.Baseline = &BaselineDiff{
				Name:         baselineName,
				SavedAt:      bl.SavedAt,
				PipelineDiff: diffWindow(s.pipelineRing.Window(60), bl.Pipeline),
				E2EDiff:      diffWindow(s.e2eRing.Window(60), bl.E2E),
				MixCycleDiff: diffWindow(s.mixCycleRing.Window(60), bl.MixCycle),
			}
		}
	}

	return snap
}
