package perf

import (
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/output"
	"github.com/zsiec/switchframe/server/switcher"
)

// SwitcherAdapter wraps a *switcher.Switcher to satisfy SwitcherPerf.
type SwitcherAdapter struct {
	SW *switcher.Switcher
}

// PerfSample delegates to the Switcher's PerfSample and converts types.
func (a *SwitcherAdapter) PerfSample() SwitcherSample {
	raw := a.SW.PerfSample()
	sources := make(map[string]SourceSample, len(raw.Sources))
	for k, v := range raw.Sources {
		sources[k] = SourceSample{
			DecodeLastNs:  v.DecodeLastNs,
			DecodeDrops:   v.DecodeDrops,
			AvgFPS:        v.AvgFPS,
			AvgFrameBytes: v.AvgFrameBytes,
			Health:        v.Health,
		}
	}
	return SwitcherSample{
		Sources:            sources,
		PipelineLastNs:     raw.PipelineLastNs,
		NodeTimings:        raw.NodeTimings,
		E2ELastNs:          raw.E2ELastNs,
		QueueLen:           raw.QueueLen,
		OutputFPS:          raw.OutputFPS,
		BroadcastGapNs:     raw.BroadcastGapNs,
		VideoBroadcast:     raw.VideoBroadcast,
		DeadlineViolations: raw.DeadlineViolations,
		FrameBudgetNs:      raw.FrameBudgetNs,
		DecodeQueueNs:      raw.DecodeQueueNs,
		DecodeNs:           raw.DecodeNs,
		SyncWaitNs:         raw.SyncWaitNs,
		ProcQueueNs:        raw.ProcQueueNs,
	}
}

// MixerAdapter wraps a *audio.Mixer to satisfy MixerPerf.
type MixerAdapter struct {
	Mixer *audio.Mixer
}

// PerfSample delegates to the Mixer's PerfSample and converts types.
func (a *MixerAdapter) PerfSample() MixerSample {
	raw := a.Mixer.PerfSample()
	return MixerSample{
		Mode:               raw.Mode,
		MixCycleLastNs:     raw.MixCycleLastNs,
		FramesOutput:       raw.FramesOutput,
		FramesPassthrough:  raw.FramesPassthrough,
		FramesMixed:        raw.FramesMixed,
		MaxInterFrameGapNs: raw.MaxInterFrameGapNs,
		DeadlineFlushes:    raw.DeadlineFlushes,
		DecodeErrors:       raw.DecodeErrors,
		EncodeErrors:       raw.EncodeErrors,
		MomentaryLUFS:      raw.MomentaryLUFS,
		ShortTermLUFS:      raw.ShortTermLUFS,
		IntegratedLUFS:     raw.IntegratedLUFS,
	}
}

// OutputAdapter wraps a *output.Manager to satisfy OutputPerf.
type OutputAdapter struct {
	Manager *output.Manager
}

// PerfSample delegates to the Manager's PerfSample and converts types.
func (a *OutputAdapter) PerfSample() OutputSample {
	raw := a.Manager.PerfSample()
	return OutputSample{
		ViewerVideoSent:    raw.ViewerVideoSent,
		ViewerVideoDropped: raw.ViewerVideoDropped,
		ViewerAudioSent:    raw.ViewerAudioSent,
		ViewerAudioDropped: raw.ViewerAudioDropped,
		MuxerPTS:           raw.MuxerPTS,
		SRTBytesWritten:    raw.SRTBytesWritten,
		SRTOverflowCount:   raw.SRTOverflowCount,
		RecordingActive:    raw.RecordingActive,
	}
}
