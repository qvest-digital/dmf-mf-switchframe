package audio

// PerfMixerSample mirrors perf.MixerSample for interface satisfaction.
// We can't import the perf package from audio (circular dependency), so
// we define compatible types here. The perf.Sampler wraps these via a thin adapter.
type PerfMixerSample struct {
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

// PerfSample returns a performance snapshot of the mixer's current state.
// Safe for concurrent access from any goroutine.
func (m *Mixer) PerfSample() PerfMixerSample {
	return PerfMixerSample{
		Mode:               "mixing",
		MixCycleLastNs:     m.lastMixCycleNs.Load(),
		FramesOutput:       m.outputFrameCount.Load(),
		FramesMixed:        m.framesMixed.Load(),
		MaxInterFrameGapNs: m.maxInterFrameNano.Load(),
		DecodeErrors:       m.decodeErrors.Load(),
		EncodeErrors:       m.encodeErrors.Load(),
		MomentaryLUFS:      m.loudness.MomentaryLUFS(),
		ShortTermLUFS:      m.loudness.ShortTermLUFS(),
		IntegratedLUFS:     m.loudness.IntegratedLUFS(),
	}
}
