package output

// PerfOutputSample mirrors perf.OutputSample for interface satisfaction.
// We can't import the perf package from output (circular dependency), so
// we define compatible types here. The perf.Sampler wraps these via a thin adapter.
type PerfOutputSample struct {
	MuxerPTS        int64
	SRTBytesWritten int64
	SRTOverflowCount int64
	RecordingActive bool
}

// PerfSample returns a performance snapshot of the output manager's current state.
// Safe for concurrent access from any goroutine.
func (m *Manager) PerfSample() PerfOutputSample {
	m.mu.Lock()
	muxer := m.muxer
	m.mu.Unlock()

	var muxerPTS int64
	if muxer != nil {
		muxerPTS = muxer.CurrentPTS()
	}

	srt := m.SRTOutputStatus()
	rec := m.RecordingStatus()

	return PerfOutputSample{
		MuxerPTS:         muxerPTS,
		SRTBytesWritten:  srt.BytesWritten,
		SRTOverflowCount: srt.OverflowCount,
		RecordingActive:  rec.Active,
	}
}
