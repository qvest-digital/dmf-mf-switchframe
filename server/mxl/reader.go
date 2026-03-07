package mxl

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// VideoGrain carries a raw V210 video frame read from an MXL flow.
type VideoGrain struct {
	V210   []byte // V210 packed pixel data
	Width  int
	Height int
	PTS    int64 // MXL grain index as PTS
}

// AudioGrain carries raw float32 PCM audio read from an MXL flow.
type AudioGrain struct {
	PCM        [][]float32 // De-interleaved channels
	SampleRate int
	Channels   int
	PTS        int64 // MXL sample index as PTS
}

// ReaderConfig configures an MXL flow reader.
type ReaderConfig struct {
	// BufSize is the channel buffer size for video/audio grains.
	// Defaults to 4 if zero.
	BufSize int

	// TimeoutMs is the read timeout in milliseconds. Defaults to 100.
	TimeoutMs int

	// Video dimensions (required for video flows to know V210 line stride).
	Width  int
	Height int

	// Audio samples per read (required for audio flows). Defaults to 1024.
	SamplesPerRead int

	// Logger. Defaults to slog.Default().
	Logger *slog.Logger
}

func (c *ReaderConfig) defaults() {
	if c.BufSize <= 0 {
		c.BufSize = 4
	}
	if c.TimeoutMs <= 0 {
		c.TimeoutMs = 100
	}
	if c.SamplesPerRead <= 0 {
		c.SamplesPerRead = 1024
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
}

// Reader continuously reads from an MXL flow and delivers grains on channels.
type Reader struct {
	config  ReaderConfig
	videoCh chan VideoGrain
	audioCh chan AudioGrain
	wg      sync.WaitGroup
}

// NewVideoReader creates a Reader for a discrete (video) MXL flow.
func NewVideoReader(config ReaderConfig) *Reader {
	config.defaults()
	return &Reader{
		config:  config,
		videoCh: make(chan VideoGrain, config.BufSize),
	}
}

// NewAudioReader creates a Reader for a continuous (audio) MXL flow.
func NewAudioReader(config ReaderConfig) *Reader {
	config.defaults()
	return &Reader{
		config:  config,
		audioCh: make(chan AudioGrain, config.BufSize),
	}
}

// Video returns the channel that delivers video grains.
// Returns nil for audio readers.
func (r *Reader) Video() <-chan VideoGrain {
	return r.videoCh
}

// Audio returns the channel that delivers audio grains.
// Returns nil for video readers.
func (r *Reader) Audio() <-chan AudioGrain {
	return r.audioCh
}

// StartVideo begins reading video grains from the flow in a goroutine.
// Blocks until the context is cancelled or an unrecoverable error occurs.
func (r *Reader) StartVideo(ctx context.Context, flow DiscreteReader) {
	r.wg.Add(1)
	go r.videoLoop(ctx, flow)
}

// StartAudio begins reading audio samples from the flow in a goroutine.
func (r *Reader) StartAudio(ctx context.Context, flow ContinuousReader) {
	r.wg.Add(1)
	go r.audioLoop(ctx, flow)
}

// Wait blocks until all reader goroutines have stopped.
func (r *Reader) Wait() {
	r.wg.Wait()
}

func (r *Reader) videoLoop(ctx context.Context, flow DiscreteReader) {
	defer r.wg.Done()
	defer close(r.videoCh)

	log := r.config.Logger
	timeoutNs := uint64(r.config.TimeoutMs) * 1_000_000
	config := flow.ConfigInfo()

	// Start reading from the current head position.
	index, err := flow.HeadIndex()
	if err != nil {
		log.Error("mxl reader: failed to get head index", "error", err)
		return
	}

	var lastPTS int64
	consecutiveErrors := 0
	const maxConsecutiveErrors = 50

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		data, info, err := flow.ReadGrain(index, timeoutNs)
		if err != nil {
			consecutiveErrors++
			if consecutiveErrors >= maxConsecutiveErrors {
				log.Error("mxl reader: too many consecutive errors, stopping",
					"errors", consecutiveErrors, "last_error", err)
				return
			}
			// Brief backoff on timeout/too-early errors.
			time.Sleep(time.Millisecond)
			continue
		}
		consecutiveErrors = 0

		if info.Invalid {
			index++
			continue
		}

		pts := int64(info.Index)

		// Detect timestamp discontinuity.
		if lastPTS > 0 && pts-lastPTS > 2 {
			log.Warn("mxl reader: timestamp discontinuity",
				"expected", lastPTS+1, "got", pts, "gap", pts-lastPTS)
		}
		lastPTS = pts

		grain := VideoGrain{
			V210:   data,
			Width:  r.config.Width,
			Height: r.config.Height,
			PTS:    pts,
		}

		// If width/height not configured, derive from config.
		if grain.Width == 0 && len(config.SliceSizes) > 0 && config.SliceSizes[0] > 0 {
			// V210 line stride = (width + 47) / 48 * 128
			// We can't easily reverse this without knowing one dimension.
			// Rely on configured width/height.
			grain.Width = r.config.Width
			grain.Height = r.config.Height
		}

		select {
		case r.videoCh <- grain:
		case <-ctx.Done():
			return
		}

		index++
	}
}

func (r *Reader) audioLoop(ctx context.Context, flow ContinuousReader) {
	defer r.wg.Done()
	defer close(r.audioCh)

	log := r.config.Logger
	timeoutNs := uint64(r.config.TimeoutMs) * 1_000_000
	config := flow.ConfigInfo()

	sampleRate := int(config.GrainRate.Numerator)
	if config.GrainRate.Denominator > 1 {
		sampleRate = int(config.GrainRate.Float64())
	}
	channels := int(config.ChannelCount)
	samplesPerRead := r.config.SamplesPerRead

	// Read position: start from current MXL time (wall-clock for stubs).
	index := CurrentIndex(config.GrainRate)
	// PTS counter: monotonic from 0, independent of read position.
	// This ensures audio PTS aligns with video PTS (both start near 0).
	var ptsCounter int64

	consecutiveErrors := 0
	const maxConsecutiveErrors = 50

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		pcm, err := flow.ReadSamples(index, samplesPerRead, timeoutNs)
		if err != nil {
			consecutiveErrors++
			if consecutiveErrors >= maxConsecutiveErrors {
				log.Error("mxl audio reader: too many consecutive errors, stopping",
					"errors", consecutiveErrors, "last_error", err)
				return
			}
			time.Sleep(time.Millisecond)
			continue
		}
		consecutiveErrors = 0

		grain := AudioGrain{
			PCM:        pcm,
			SampleRate: sampleRate,
			Channels:   channels,
			PTS:        ptsCounter,
		}

		select {
		case r.audioCh <- grain:
		case <-ctx.Done():
			return
		}

		index += uint64(samplesPerRead)
		ptsCounter += int64(samplesPerRead)
	}
}
