package mxl

import (
	"context"
	"log/slog"
	"sync"
)

// WriterConfig configures an MXL flow writer.
type WriterConfig struct {
	// Video dimensions for YUV420p→V210 conversion.
	Width  int
	Height int

	// Audio parameters.
	SampleRate int
	Channels   int

	// Logger. Defaults to slog.Default().
	Logger *slog.Logger
}

// Writer writes processed video and audio to MXL flows.
// Implements the RawVideoSink and RawAudioSink callback signatures.
type Writer struct {
	config WriterConfig
	log    *slog.Logger

	mu          sync.Mutex
	videoWriter DiscreteWriter
	audioWriter ContinuousWriter
	videoRate   Rational
	audioRate   Rational
	closed      bool

	// Reusable de-interleave buffers to avoid per-frame allocation.
	deinterleaveBuf [][]float32
}

// NewWriter creates an MXL writer.
func NewWriter(config WriterConfig) *Writer {
	log := config.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Writer{
		config: config,
		log:    log,
	}
}

// SetVideoWriter sets the discrete writer for video output.
func (w *Writer) SetVideoWriter(dw DiscreteWriter, grainRate Rational) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.videoWriter = dw
	w.videoRate = grainRate
}

// SetAudioWriter sets the continuous writer for audio output.
func (w *Writer) SetAudioWriter(cw ContinuousWriter, sampleRate Rational) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.audioWriter = cw
	w.audioRate = sampleRate
}

// WriteVideo converts a YUV420p frame to V210 and writes it as a grain.
// Intended to be used as a switcher.RawVideoSink callback.
// The pf parameter carries YUV420p data, width, height, and PTS.
func (w *Writer) WriteVideo(yuv []byte, width, height int, pts int64) {
	w.mu.Lock()
	if w.closed || w.videoWriter == nil {
		w.mu.Unlock()
		return
	}
	vw := w.videoWriter
	rate := w.videoRate
	w.mu.Unlock()

	// Convert YUV420p → V210.
	v210, err := YUV420pToV210(yuv, width, height)
	if err != nil {
		w.log.Error("mxl writer: YUV420p→V210 conversion failed",
			"error", err, "width", width, "height", height)
		return
	}

	// Compute grain index from PTS or current time.
	var index uint64
	if CurrentIndex != nil {
		index = CurrentIndex(rate)
	}

	if err := vw.WriteGrain(index, v210); err != nil {
		w.log.Error("mxl writer: failed to write video grain",
			"error", err, "index", index)
	}
}

// WriteAudio converts interleaved float32 PCM to de-interleaved and writes samples.
// Intended to be used as an audio.RawAudioSink callback.
// Called from a single goroutine (mixer output), so deinterleaveBuf reuse is safe.
func (w *Writer) WriteAudio(pcm []float32, pts int64, sampleRate, channels int) {
	w.mu.Lock()
	if w.closed || w.audioWriter == nil {
		w.mu.Unlock()
		return
	}
	aw := w.audioWriter
	rate := w.audioRate
	w.mu.Unlock()

	if channels <= 0 || len(pcm) == 0 {
		return
	}

	// De-interleave: MXL audio is de-interleaved (one buffer per channel).
	// Reuse buffers across calls to avoid hot-path allocation.
	samplesPerCh := len(pcm) / channels
	if len(w.deinterleaveBuf) < channels {
		w.deinterleaveBuf = make([][]float32, channels)
	}
	deinterleaved := w.deinterleaveBuf[:channels]
	for ch := 0; ch < channels; ch++ {
		if cap(deinterleaved[ch]) < samplesPerCh {
			deinterleaved[ch] = make([]float32, samplesPerCh)
		} else {
			deinterleaved[ch] = deinterleaved[ch][:samplesPerCh]
		}
		for i := 0; i < samplesPerCh; i++ {
			deinterleaved[ch][i] = pcm[i*channels+ch]
		}
	}

	var index uint64
	if CurrentIndex != nil {
		index = CurrentIndex(rate)
	}

	if err := aw.WriteSamples(index, deinterleaved); err != nil {
		w.log.Error("mxl writer: failed to write audio samples",
			"error", err, "index", index)
	}
}

// Close releases the writer resources.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.closed = true

	var firstErr error
	if w.videoWriter != nil {
		if err := w.videoWriter.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		w.videoWriter = nil
	}
	if w.audioWriter != nil {
		if err := w.audioWriter.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		w.audioWriter = nil
	}
	return firstErr
}

// Start is a convenience method that keeps the writer alive until ctx is done.
func (w *Writer) Start(ctx context.Context) {
	go func() {
		<-ctx.Done()
		_ = w.Close()
	}()
}
