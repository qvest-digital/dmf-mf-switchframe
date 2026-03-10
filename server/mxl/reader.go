package mxl

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// VideoGrain carries a raw V210 video frame read from an MXL flow.
type VideoGrain struct {
	V210     []byte    // V210 packed pixel data
	Width    int
	Height   int
	PTS      int64     // Monotonic frame counter (1, 2, 3, ...)
	ReadTime time.Time // Wall-clock time when grain was read (for AV sync)
}

// AudioGrain carries raw float32 PCM audio read from an MXL flow.
type AudioGrain struct {
	PCM        [][]float32 // De-interleaved channels
	SampleRate int
	Channels   int
	PTS        int64     // Monotonic sample counter from 0
	ReadTime   time.Time // Wall-clock time when grain was read (for AV sync)
}

// DataGrain carries raw metadata/ancillary data read from an MXL flow.
type DataGrain struct {
	Data []byte // Raw payload data
	PTS  int64  // Monotonic grain counter (1, 2, 3, ...)
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
	dataCh  chan DataGrain
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

// NewDataReader creates a Reader for a discrete data (metadata/ancillary) MXL flow.
func NewDataReader(config ReaderConfig) *Reader {
	config.defaults()
	return &Reader{
		config: config,
		dataCh: make(chan DataGrain, config.BufSize),
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

// Data returns the channel that delivers data grains.
// Returns nil for video/audio readers.
func (r *Reader) Data() <-chan DataGrain {
	return r.dataCh
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

// StartData begins reading data grains from the flow in a goroutine.
func (r *Reader) StartData(ctx context.Context, flow DiscreteReader) {
	r.wg.Add(1)
	go r.dataLoop(ctx, flow)
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

	// Monotonic PTS counter starting from 0, incremented by 1 per grain.
	// Both video and audio PTS use their own monotonic counter domains
	// (frame number for video, sample count for audio). The downstream
	// PTS conversion handles scaling to 90 kHz MPEG-TS clock.
	var videoPTSCounter int64

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
			errStr := err.Error()

			// On "too late" errors, re-sync to the current write head
			// instead of dying. The ring buffer moved past our position.
			if strings.Contains(errStr, "too late") {
				if headIdx, hErr := flow.HeadIndex(); hErr == nil {
					gap := headIdx - index
					log.Warn("mxl video reader: ring buffer wrapped, re-syncing",
						"gap", gap, "old_index", index, "new_index", headIdx-2)
					index = headIdx - 2
					consecutiveErrors = 0
					continue
				}
			}

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

		videoPTSCounter++
		pts := videoPTSCounter
		readTime := time.Now()

		grain := VideoGrain{
			V210:     data,
			Width:    r.config.Width,
			Height:   r.config.Height,
			PTS:      pts,
			ReadTime: readTime,
		}

		// If width/height not configured, derive from config.
		if grain.Width == 0 && len(config.SliceSizes) > 0 && config.SliceSizes[0] > 0 {
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
	config := flow.ConfigInfo()

	// Use a short timeout for audio reads (5ms). The MXL SDK may use a coarse
	// internal polling interval; a long timeout (100ms) causes the SDK to hold
	// the cgo thread for extended periods, starving other flow readers and
	// reducing audio throughput to ~60% of expected. With a short timeout we
	// release the SDK quickly and retry from Go-land with fine-grained control.
	const audioTimeoutNs = 5_000_000 // 5ms

	sampleRate := int(config.GrainRate.Numerator)
	if config.GrainRate.Denominator > 1 {
		sampleRate = int(config.GrainRate.Float64())
	}
	channels := int(config.ChannelCount)
	samplesPerRead := r.config.SamplesPerRead

	// Read position: start from the ring buffer's current write head so we
	// don't read stale samples that have already been overwritten.
	index, err := flow.HeadIndex()
	if err != nil {
		// Fallback to wall-clock approximation.
		index = CurrentIndex(config.GrainRate)
		log.Warn("mxl audio reader: HeadIndex unavailable, using wall-clock", "error", err)
	}
	// PTS counter: monotonic from 0, independent of read position.
	// This ensures audio PTS aligns with video PTS (both start near 0).
	var ptsCounter int64

	fatalErrors := 0
	const maxFatalErrors = 50

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		pcm, err := flow.ReadSamples(index, samplesPerRead, audioTimeoutNs)
		if err != nil {
			errStr := err.Error()

			// On "too late" errors, re-sync to the current write head
			// instead of dying. The ring buffer moved past our position.
			if strings.Contains(errStr, "too late") {
				if head, hErr := flow.HeadIndex(); hErr == nil {
					index = head
					fatalErrors = 0
					continue
				}
			}

			// Timeout and too-early are normal waiting conditions —
			// the writer hasn't produced samples at our position yet.
			// Don't count these as errors; just retry after a brief yield.
			if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "too early") {
				time.Sleep(500 * time.Microsecond)
				continue
			}

			// Actual errors (flow invalid, permission denied, etc.)
			fatalErrors++
			if fatalErrors >= maxFatalErrors {
				log.Error("mxl audio reader: too many consecutive errors, stopping",
					"errors", fatalErrors, "last_error", err)
				return
			}
			time.Sleep(time.Millisecond)
			continue
		}
		fatalErrors = 0

		grain := AudioGrain{
			PCM:        pcm,
			SampleRate: sampleRate,
			Channels:   channels,
			PTS:        ptsCounter,
			ReadTime:   time.Now(),
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

func (r *Reader) dataLoop(ctx context.Context, flow DiscreteReader) {
	defer r.wg.Done()
	defer close(r.dataCh)

	log := r.config.Logger
	timeoutNs := uint64(r.config.TimeoutMs) * 1_000_000

	// Start reading from the current head position.
	index, err := flow.HeadIndex()
	if err != nil {
		log.Error("mxl data reader: failed to get head index", "error", err)
		return
	}

	// Monotonic PTS counter starting from 0, incremented by 1 per grain.
	var dataPTSCounter int64

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
			errStr := err.Error()

			// On "too late" errors, re-sync to the current write head
			// instead of dying. The ring buffer moved past our position.
			if strings.Contains(errStr, "too late") {
				if headIdx, hErr := flow.HeadIndex(); hErr == nil {
					gap := headIdx - index
					log.Warn("mxl data reader: ring buffer wrapped, re-syncing",
						"gap", gap, "old_index", index, "new_index", headIdx-2)
					index = headIdx - 2
					consecutiveErrors = 0
					continue
				}
			}

			consecutiveErrors++
			if consecutiveErrors >= maxConsecutiveErrors {
				log.Error("mxl data reader: too many consecutive errors, stopping",
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

		dataPTSCounter++
		pts := dataPTSCounter

		grain := DataGrain{
			Data: data,
			PTS:  pts,
		}

		select {
		case r.dataCh <- grain:
		case <-ctx.Done():
			return
		}

		index++
	}
}
