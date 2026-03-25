package st2110

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// VideoTransmitter abstracts the cgo TX handle for ST 2110-20 video output.
type VideoTransmitter interface {
	PutFrame(yuv []byte) error
	Close() error
}

// AudioTransmitter abstracts the cgo TX handle for ST 2110-30 audio output.
type AudioTransmitter interface {
	PutFrame(pcm []float32) error
	Close() error
}

// AncillaryTransmitter abstracts the cgo TX handle for ST 2110-40 ancillary data output.
type AncillaryTransmitter interface {
	PutFrame(data []byte) error
	Close() error
}

// OutputConfig configures an ST 2110 program output.
type OutputConfig struct {
	FPSNum int // default 30000
	FPSDen int // default 1001
	Logger *slog.Logger
}

// yuvFrame holds a YUV420p frame for the steady-rate video ticker.
type yuvFrame struct {
	data   []byte
	width  int
	height int
}

// Output orchestrates ST 2110 program output by writing video at a steady
// frame rate via a ticker goroutine, and audio/ancillary synchronously.
//
// Video uses a steady-rate output model: WriteVideo stores the latest frame
// via atomic.Pointer, while a ticker goroutine submits it to the transmitter
// at a fixed frame rate. This ensures exactly one frame per period — no gaps,
// no bursts — matching the SMPTE ST 2110 essence timing requirements.
type Output struct {
	config OutputConfig
	log    *slog.Logger

	videoTx VideoTransmitter
	audioTx AudioTransmitter
	ancTx   AncillaryTransmitter

	// Steady-rate video: WriteVideo stores latest, ticker writes it.
	latestFrame atomic.Pointer[yuvFrame]

	closed    atomic.Bool
	closeDone chan struct{} // closed when Stop() completes; CAS-losers wait on it
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

// NewOutput creates an ST 2110 program output with the given configuration.
func NewOutput(cfg OutputConfig) *Output {
	if cfg.FPSNum == 0 {
		cfg.FPSNum = 30000
	}
	if cfg.FPSDen == 0 {
		cfg.FPSDen = 1001
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Output{
		config:    cfg,
		log:       log,
		closeDone: make(chan struct{}),
	}
}

// Start begins steady-rate video output and context lifecycle management.
// The video ticker writes the latest frame at the configured FPS rate.
// Audio and ancillary writes are submitted directly (no ticker).
func (o *Output) Start(videoTx VideoTransmitter, audioTx AudioTransmitter, ancTx AncillaryTransmitter) {
	o.videoTx = videoTx
	o.audioTx = audioTx
	o.ancTx = ancTx

	var ctx context.Context
	ctx, o.cancel = context.WithCancel(context.Background())

	if videoTx != nil {
		o.wg.Add(1)
		go func() {
			defer o.wg.Done()
			o.videoTickLoop(ctx)
		}()
	}

	// Context cleanup goroutine — NOT tracked in WaitGroup because
	// Stop() calls wg.Wait(), and including this would deadlock.
	go func() {
		<-ctx.Done()
	}()
}

// WriteVideo stores the latest YUV420p frame for the steady-rate ticker.
// Does NOT write to the transmitter directly — the ticker handles that.
// Called from a single goroutine (video processing loop).
func (o *Output) WriteVideo(yuv []byte, width, height int, pts int64) {
	if o.closed.Load() {
		return
	}
	cp := make([]byte, len(yuv))
	copy(cp, yuv)
	o.latestFrame.Store(&yuvFrame{
		data:   cp,
		width:  width,
		height: height,
	})
}

// WriteAudio submits PCM audio directly to the audio transmitter.
// Audio is event-driven (no ticker) — each call writes immediately.
func (o *Output) WriteAudio(pcm []float32, pts int64, sampleRate, channels int) {
	if o.closed.Load() {
		return
	}
	if o.audioTx == nil {
		return
	}
	if len(pcm) == 0 {
		return
	}
	if err := o.audioTx.PutFrame(pcm); err != nil {
		o.log.Error("st2110 output: failed to write audio",
			"error", err, "samples", len(pcm))
	}
}

// WriteAncillary submits ancillary data directly to the ancillary transmitter.
// Ancillary is event-driven — each call writes immediately.
func (o *Output) WriteAncillary(data []byte) {
	if o.closed.Load() {
		return
	}
	if o.ancTx == nil {
		return
	}
	if len(data) == 0 {
		return
	}
	if err := o.ancTx.PutFrame(data); err != nil {
		o.log.Error("st2110 output: failed to write ancillary data",
			"error", err, "len", len(data))
	}
}

// Stop disconnects transmitters and stops the output synchronously.
// Idempotent: calling Stop() more than once is safe — subsequent calls
// wait for the first Stop() to complete.
func (o *Output) Stop() {
	if !o.closed.CompareAndSwap(false, true) {
		// Another goroutine is stopping; wait for it to finish.
		<-o.closeDone
		return
	}
	defer close(o.closeDone)

	// Cancel context to signal the video ticker to exit.
	if o.cancel != nil {
		o.cancel()
	}

	// Wait for the video ticker goroutine to exit.
	o.wg.Wait()

	// Close transmitter handles.
	if o.videoTx != nil {
		if err := o.videoTx.Close(); err != nil {
			o.log.Error("st2110 output: failed to close video tx", "error", err)
		}
	}
	if o.audioTx != nil {
		if err := o.audioTx.Close(); err != nil {
			o.log.Error("st2110 output: failed to close audio tx", "error", err)
		}
	}
	if o.ancTx != nil {
		if err := o.ancTx.Close(); err != nil {
			o.log.Error("st2110 output: failed to close ancillary tx", "error", err)
		}
	}
}

// videoTickLoop writes the latest YUV frame to the video transmitter at a
// steady frame rate. This decouples the write rate from the pipeline callback
// rate, preventing gaps during keyframe waits and bursts during transitions.
func (o *Output) videoTickLoop(ctx context.Context) {
	intervalNs := float64(o.config.FPSDen) * 1e9 / float64(o.config.FPSNum)
	ticker := time.NewTicker(time.Duration(intervalNs))
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if o.closed.Load() {
				return
			}

			frame := o.latestFrame.Load()
			if frame == nil {
				continue
			}

			if err := o.videoTx.PutFrame(frame.data); err != nil {
				o.log.Error("st2110 output: failed to write video frame",
					"error", err, "width", frame.width, "height", frame.height)
			}
		}
	}
}
