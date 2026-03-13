package output

// PerfOutputSample mirrors perf.OutputSample for interface satisfaction.
// We can't import the perf package from output (circular dependency), so
// we define compatible types here. The perf.Sampler wraps these via a thin adapter.
type PerfOutputSample struct {
	ViewerVideoSent    int64
	ViewerVideoDropped int64
	ViewerAudioSent    int64
	ViewerAudioDropped int64
	MuxerPTS           int64
	SRTBytesWritten    int64
	SRTOverflowCount   int64
	RecordingActive    bool
}

// PerfSample returns a performance snapshot of the output manager's current state.
// Safe for concurrent access from any goroutine.
func (m *Manager) PerfSample() PerfOutputSample {
	m.mu.Lock()
	var videoSent, videoDropped, audioSent, audioDropped int64
	var muxerPTS int64
	if m.viewer != nil {
		snap := m.viewer.DebugSnapshot()
		if v, ok := snap["video_sent"].(int64); ok {
			videoSent = v
		}
		if v, ok := snap["video_dropped"].(int64); ok {
			videoDropped = v
		}
		if v, ok := snap["audio_sent"].(int64); ok {
			audioSent = v
		}
		if v, ok := snap["audio_dropped"].(int64); ok {
			audioDropped = v
		}
	}
	if m.muxer != nil {
		muxerPTS = m.muxer.CurrentPTS()
	}
	m.mu.Unlock()

	srt := m.SRTOutputStatus()
	rec := m.RecordingStatus()

	return PerfOutputSample{
		ViewerVideoSent:    videoSent,
		ViewerVideoDropped: videoDropped,
		ViewerAudioSent:    audioSent,
		ViewerAudioDropped: audioDropped,
		MuxerPTS:           muxerPTS,
		SRTBytesWritten:    srt.BytesWritten,
		SRTOverflowCount:   srt.OverflowCount,
		RecordingActive:    rec.Active,
	}
}
