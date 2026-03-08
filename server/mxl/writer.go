package mxl

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
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

// v210Frame holds a converted V210 frame for the steady-rate output ticker.
type v210Frame struct {
	data []byte
}

// videoWriterRef wraps a DiscreteWriter and its grain rate for atomic access.
// Go's atomic.Pointer requires a concrete pointer type, not an interface.
type videoWriterRef struct {
	writer DiscreteWriter
	rate   Rational
}

// Writer writes processed video and audio to MXL flows.
//
// Video uses a steady-rate output model: WriteVideo converts and buffers
// the latest frame, while a ticker goroutine writes to MXL shared memory
// at a fixed frame rate (matching mxl-gst-testsrc behavior). This ensures:
//   - Exactly one grain per frame period (no gaps, no bursts)
//   - Wall-clock aligned indices (reader can always find data)
//   - During transitions: latest blended frame wins, no double-rate writes
//   - During cuts: last frame repeats during keyframe wait (no gap)
//
// Audio uses wall-clock indices with monotonic enforcement for contiguous
// sample delivery.
type Writer struct {
	config WriterConfig
	log    *slog.Logger

	// videoRef is lock-free: accessed atomically from the ticker hot path.
	videoRef atomic.Pointer[videoWriterRef]

	// mu protects audioWriter/audioRate and Close() resource cleanup.
	mu          sync.Mutex
	audioWriter ContinuousWriter
	audioRate   Rational

	closed atomic.Bool

	// Steady-rate video: WriteVideo stores latest frame, ticker writes it.
	latestV210 atomic.Pointer[v210Frame]

	// Last written audio index for monotonic enforcement.
	// Only accessed from the single mixer output goroutine.
	lastAudioIndex uint64

	// Reusable de-interleave buffers to avoid per-frame allocation.
	deinterleaveBuf [][]float32

	// Double-buffered V210 output to avoid per-frame allocation.
	// WriteVideo alternates between buf[0] and buf[1]; one is being
	// consumed by the ticker while the other is being filled.
	v210Bufs      [2][]byte
	v210BufIdx    int
	v210BufWidth  int
	v210BufHeight int
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
	w.videoRef.Store(&videoWriterRef{writer: dw, rate: grainRate})
}

// SetAudioWriter sets the continuous writer for audio output.
func (w *Writer) SetAudioWriter(cw ContinuousWriter, sampleRate Rational) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.audioWriter = cw
	w.audioRate = sampleRate
}

// WriteVideo converts a YUV420p frame to V210 and stores it for the
// steady-rate output ticker. Does NOT write to MXL directly — the
// ticker goroutine handles that at a fixed frame rate.
//
// Called from a single goroutine (video processing loop).
func (w *Writer) WriteVideo(yuv []byte, width, height int, pts int64) {
	if w.closed.Load() {
		return
	}

	// Skip frames that don't match the configured output resolution.
	// Non-MXL sources (e.g., synthetic demo cameras) may produce different
	// dimensions that can't be written to the fixed-size MXL flow.
	if w.config.Width > 0 && w.config.Height > 0 {
		if width != w.config.Width || height != w.config.Height {
			return
		}
	}

	// Convert YUV420p → V210 using double-buffered output.
	// We alternate between two pre-allocated buffers so the ticker goroutine
	// can safely read one while we fill the other.
	stride := V210LineStride(width)
	outSize := stride * height
	if w.v210BufWidth != width || w.v210BufHeight != height {
		w.v210Bufs[0] = make([]byte, outSize)
		w.v210Bufs[1] = make([]byte, outSize)
		w.v210BufIdx = 0
		w.v210BufWidth = width
		w.v210BufHeight = height
	}

	buf := w.v210Bufs[w.v210BufIdx]
	if err := YUV420pToV210Into(yuv, buf, width, height); err != nil {
		w.log.Error("mxl writer: YUV420p→V210 conversion failed",
			"error", err, "width", width, "height", height)
		return
	}

	w.latestV210.Store(&v210Frame{data: buf})
	w.v210BufIdx ^= 1
}

// videoTickLoop writes the latest V210 frame to MXL at a steady frame rate.
// This decouples the write rate from the pipeline callback rate, preventing
// gaps during keyframe waits and bursts during transitions.
func (w *Writer) videoTickLoop(ctx context.Context) {
	ref := w.videoRef.Load()
	if ref == nil || ref.rate.Numerator <= 0 || ref.rate.Denominator <= 0 {
		return
	}

	intervalNs := float64(ref.rate.Denominator) * 1e9 / float64(ref.rate.Numerator)
	ticker := time.NewTicker(time.Duration(intervalNs))
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if w.closed.Load() {
				return
			}

			frame := w.latestV210.Load()
			if frame == nil {
				continue
			}

			ref = w.videoRef.Load()
			if ref == nil || ref.writer == nil {
				return
			}

			var index uint64
			if CurrentIndex != nil {
				index = CurrentIndex(ref.rate)
			}

			if err := ref.writer.WriteGrain(index, frame.data); err != nil {
				w.log.Error("mxl writer: failed to write video grain",
					"error", err, "index", index)
			}
		}
	}
}

// WriteAudio converts interleaved float32 PCM to de-interleaved and writes samples.
// Intended to be used as an audio.RawAudioSink callback.
// Called from a single goroutine (mixer output), so deinterleaveBuf reuse is safe.
func (w *Writer) WriteAudio(pcm []float32, pts int64, sampleRate, channels int) {
	if w.closed.Load() {
		return
	}
	w.mu.Lock()
	if w.audioWriter == nil {
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

	// Wall-clock index with monotonic enforcement: never overlap previous batch.
	var index uint64
	if CurrentIndex != nil {
		index = CurrentIndex(rate)
	}
	expectedNext := w.lastAudioIndex + uint64(samplesPerCh)
	if index < expectedNext && w.lastAudioIndex > 0 {
		index = expectedNext
	}
	w.lastAudioIndex = index

	if err := aw.WriteSamples(index, deinterleaved); err != nil {
		w.log.Error("mxl writer: failed to write audio samples",
			"error", err, "index", index)
	}
}

// Close releases the writer resources.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed.Load() {
		return nil
	}
	w.closed.Store(true)

	var firstErr error
	if ref := w.videoRef.Load(); ref != nil && ref.writer != nil {
		if err := ref.writer.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		w.videoRef.Store(nil)
	}
	if w.audioWriter != nil {
		if err := w.audioWriter.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		w.audioWriter = nil
	}
	return firstErr
}

// Start starts background goroutines for steady-rate output.
// The video ticker writes the latest frame at the configured grain rate.
// Call this after SetVideoWriter/SetAudioWriter.
func (w *Writer) Start(ctx context.Context) {
	if ref := w.videoRef.Load(); ref != nil && ref.writer != nil {
		go w.videoTickLoop(ctx)
	}

	go func() {
		<-ctx.Done()
		_ = w.Close()
	}()
}
