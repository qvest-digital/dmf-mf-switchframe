package mxl

import (
	"context"
	"log/slog"
)

// SwitcherSinkSetter sets a raw video sink on the switcher.
type SwitcherSinkSetter interface {
	SetRawVideoSink(sink func(yuv []byte, width, height int, pts int64))
}

// MixerSinkSetter sets a raw audio sink on the mixer.
type MixerSinkSetter interface {
	SetRawAudioSink(sink func(pcm []float32, pts int64, sampleRate, channels int))
}

// OutputConfig configures an MXL program output.
type OutputConfig struct {
	// FlowName identifies this output in MXL domain.
	FlowName string

	// Video dimensions.
	Width  int
	Height int

	// Audio parameters.
	SampleRate int
	Channels   int

	Logger *slog.Logger
}

// Output wires the switcher and mixer raw sinks to an MXL writer,
// publishing the program output to MXL shared memory.
type Output struct {
	config OutputConfig
	writer *Writer
	log    *slog.Logger
	cancel context.CancelFunc
}

// NewOutput creates an MXL program output.
func NewOutput(config OutputConfig) *Output {
	if config.SampleRate == 0 {
		config.SampleRate = 48000
	}
	if config.Channels == 0 {
		config.Channels = 2
	}
	log := config.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Output{
		config: config,
		writer: NewWriter(WriterConfig{
			Width:      config.Width,
			Height:     config.Height,
			SampleRate: config.SampleRate,
			Channels:   config.Channels,
			Logger:     log,
		}),
		log: log,
	}
}

// Start connects the output to MXL flows and registers sink callbacks.
// videoWriter and audioWriter are the MXL flow writers (can be nil to skip).
// sw and mixer have their raw sinks set to the MXL writer callbacks.
func (o *Output) Start(ctx context.Context, videoWriter DiscreteWriter, audioWriter ContinuousWriter, sw SwitcherSinkSetter, mixer MixerSinkSetter) {
	ctx, o.cancel = context.WithCancel(ctx)

	if videoWriter != nil {
		// Default to 29.97fps for grain rate — will use CurrentIndex for timing.
		o.writer.SetVideoWriter(videoWriter, Rational{30000, 1001})
	}
	if audioWriter != nil {
		o.writer.SetAudioWriter(audioWriter, Rational{int64(o.config.SampleRate), 1})
	}

	o.writer.Start(ctx)

	// Register sink callbacks.
	if sw != nil {
		sw.SetRawVideoSink(o.writer.WriteVideo)
	}
	if mixer != nil {
		mixer.SetRawAudioSink(o.writer.WriteAudio)
	}

	o.log.Info("MXL output started", "flow", o.config.FlowName,
		"video", videoWriter != nil, "audio", audioWriter != nil)
}

// Stop disconnects sinks and closes the MXL writer.
func (o *Output) Stop() {
	if o.cancel != nil {
		o.cancel()
	}
	o.writer.Close()
	o.log.Info("MXL output stopped", "flow", o.config.FlowName)
}
