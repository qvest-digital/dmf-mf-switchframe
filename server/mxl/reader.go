package mxl

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/switchframe/server/clock"
)

// VideoGrain carries a raw V210 video frame read from an MXL flow.
type VideoGrain struct {
	V210   []byte // V210 packed pixel data
	Width  int
	Height int
	Index  uint64     // grain index on TAI
	Rate   clock.Rate // grain rate of the flow
	PTS    int64      // 90 kHz PTS of the index's TAI on the engine timeline
}

// AudioGrain carries raw float32 PCM audio read from an MXL flow.
type AudioGrain struct {
	PCM        [][]float32 // De-interleaved channels
	SampleRate int
	Channels   int
	Start      uint64 // index of the first sample on TAI
	PTS        int64  // 90 kHz PTS of Start's TAI on the engine timeline
}

// DataGrain carries raw metadata/ancillary data read from an MXL flow.
type DataGrain struct {
	Data  []byte // Raw payload data
	Index uint64 // grain index on TAI
	PTS   int64  // 90 kHz PTS of the index's TAI on the engine timeline
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

	// Audio samples per read. Zero reads audioReadSize at the program's
	// rate of the time.
	SamplesPerRead int

	// Logger. Defaults to slog.Default().
	Logger *slog.Logger
}

// videoMaxLag is how far behind the head the video loop's read position may
// fall while its consumer is the reason, before it skips ahead. A consumer
// slower than the flow then loses single grains as it goes rather than
// falling a whole ring behind and losing the ring at once on a re-sync.
// Delivery latency adds the channel's grains on top, about 3+4 grains. A
// position behind because the producer committed a burst is not skipped:
// the consumer counts as the cause only when a send waited longer than a
// grain period, and one that keeps up drains the burst.
const videoMaxLag = 3

// grainLookBack is how many grains behind the head the video and data loops
// resync to.
const grainLookBack = 2

// audioReadSize is the most samples of a flow at sampleRate the audio
// loop reads at a time, the program period being period 90 kHz ticks, 0
// without a program clock: 10 ms, the batch GStreamer MXL writers commit,
// or the audio's budget where that is shorter. A read is handed over only
// once its last sample is written, so its first sample reaches the mixer
// at least the read's duration after its media time, and the mixer reads
// audio clock.ReaderDelayGrains-1 periods after it: 8.3 ms at 120 fps,
// 4.2 ms at 240. period is rounded up to a whole tick, so the budget is
// taken one tick short per period and a read never exceeds it. A read
// takes no more than the producer has committed (committed), so a batch
// longer than the budget is split rather than waited for. A producer that
// commits 10 ms at a time still overruns a budget under 10 ms; what keeps
// it in is a read delay of more periods.
func audioReadSize(sampleRate int, period int64) int {
	n := sampleRate / 100
	if period > 0 {
		budget := (clock.ReaderDelayGrains - 1) * (period - 1)
		n = min(n, int(budget*int64(sampleRate)/90000))
	}
	return max(n, 1)
}

// maxAudioReadSize is the most samples of a flow at sampleRate a read of
// audioReadSize takes.
func maxAudioReadSize(sampleRate int) int {
	return audioReadSize(sampleRate, 0)
}

// audioLeadPeriods is how many program periods ahead of its first sample's
// media time the audio loop hands a read over, and minAudioLead the least
// it hands it over by. The mixer reads clock.ReaderDelayGrains periods
// behind the clock; at 240 fps two periods are 8.3 ms, and with every core
// busy a read held to that reached the mixer up to 5.3 ms after its read
// point. The floor covers that scheduling delay, which does not depend on
// any rate. Either way a read waits in the source's ring for a few tens of
// ms, far less than the 30720 mixer samples, 640 ms, the ring holds.
const (
	audioLeadPeriods = 2
	minAudioLead     = 20 * time.Millisecond
)

// audioReleaseLead is how far ahead of its first sample's media time a held
// read is handed over, for a program period in 90 kHz ticks; zero without a
// program clock, when nothing is held.
func audioReleaseLead(period int64) time.Duration {
	if period <= 0 {
		return 0
	}
	return max(audioLeadPeriods*time.Duration(period)*time.Second/90000, minAudioLead)
}

// maxAudioHold is the longest the audio loop holds one read. A producer's
// lead is some tens of its own grains, under 2 s at the lowest frame rate
// accepted, and does not depend on the program's rate. A flow stamped
// further ahead is not on the program's timeline at all, a wrong epoch or
// misstamped indices (TAI against UTC alone is 37 s), and is handed over as
// read rather than parking the loop.
const maxAudioHold = 4 * time.Second

func (c *ReaderConfig) defaults() {
	if c.BufSize <= 0 {
		c.BufSize = 4
	}
	if c.TimeoutMs <= 0 {
		c.TimeoutMs = 100
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

	// What a reader lost and how far behind it reads, for /metrics. Each
	// is one atomic operation on a path that already reads the head.
	skipped atomic.Uint64 // grains skipped because the consumer was behind
	resyncs atomic.Uint64 // jumps to the head after the ring overwrote the position
	lag     atomic.Uint64 // grains between the head and the read position, video only
}

// ReaderStats is what a Reader has lost, and how far behind the head of
// its flow it reads.
type ReaderStats struct {
	// SkippedGrains are grains skipped because the consumer was behind;
	// only a video reader skips.
	SkippedGrains uint64
	// Resyncs counts jumps to the head after the ring overwrote the read
	// position, each losing what lay between.
	Resyncs uint64
	// LagGrains is how many grains the read position trails the head, as
	// of the last read; video only.
	LagGrains uint64
}

// Stats returns what the reader has lost so far.
func (r *Reader) Stats() ReaderStats {
	return ReaderStats{SkippedGrains: r.skipped.Load(), Resyncs: r.resyncs.Load(), LagGrains: r.lag.Load()}
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

	// Start reading from the current head position.
	if !waitForFlow(ctx, flow) {
		return
	}
	index, err := flow.HeadIndex()
	if err != nil {
		log.Error("mxl reader: failed to get head index", "error", err)
		return
	}

	// PTS is the index's TAI: a skipped or invalid grain costs its time.
	rate := flow.ConfigInfo().GrainRate
	cr := clock.Rate{Num: rate.Numerator, Den: rate.Denominator}

	consecutiveErrors := 0
	const maxConsecutiveErrors = 50

	var skipped uint64
	// The consumer is behind when a send waits longer than a grain period;
	// a shorter wait is it being scheduled, as when a burst fills videoCh.
	period := 20 * time.Millisecond
	if rate.Numerator > 0 && rate.Denominator > 0 {
		period = time.Duration(int64(time.Second) * rate.Denominator / rate.Numerator)
	}
	waited := false // the last send waited longer than period
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Skip only what the consumer cannot take.
		headIdx, hErr := flow.HeadIndex()
		if hErr == nil {
			var lag uint64
			if headIdx > index {
				lag = headIdx - index
			}
			r.lag.Store(lag)
		}
		if waited && hErr == nil && headIdx > index+videoMaxLag {
			n := headIdx - videoMaxLag - index
			index = headIdx - videoMaxLag
			r.skipped.Add(n)
			if skipped += n; skipped == n || skipped/500 != (skipped-n)/500 {
				log.Warn("mxl video reader: consumer behind, skipping grains",
					"skipped_total", skipped)
			}
		}

		data, info, err := flow.ReadGrain(index, timeoutNs)
		if err != nil {
			// On "too late" errors, re-sync to the current write head
			// instead of dying. The ring buffer moved past our position.
			if errors.Is(err, ErrTooLate) {
				if headIdx, hErr := flow.HeadIndex(); hErr == nil {
					oldIndex := index
					if headIdx >= grainLookBack {
						index = headIdx - grainLookBack
					} else {
						index = headIdx
					}
					r.resyncs.Add(1)
					log.Warn("mxl video reader: ring buffer wrapped, re-syncing",
						"gap", headIdx-oldIndex, "old_index", oldIndex, "new_index", index)
					consecutiveErrors = 0
					continue
				}
			}

			// Timeout and too-early are transient — don't count as fatal errors.
			if errors.Is(err, ErrTimeout) || errors.Is(err, ErrTooEarly) {
				time.Sleep(time.Millisecond)
				continue
			}

			consecutiveErrors++
			if consecutiveErrors >= maxConsecutiveErrors {
				log.Error("mxl reader: too many consecutive errors, stopping",
					"errors", consecutiveErrors, "last_error", err)
				return
			}
			// Brief backoff on other errors.
			time.Sleep(time.Millisecond)
			continue
		}
		consecutiveErrors = 0

		if info.Invalid {
			index++
			continue
		}

		grain := VideoGrain{
			V210:   data,
			Width:  r.config.Width,
			Height: r.config.Height,
			Index:  index,
			Rate:   cr,
			PTS:    clock.IndexPTS(cr, index),
		}

		select {
		case r.videoCh <- grain:
			waited = false
		default:
			start := time.Now()
			select {
			case r.videoCh <- grain:
			case <-ctx.Done():
				return
			}
			waited = time.Since(start) > period
		}

		index++
	}
}

func (r *Reader) audioLoop(ctx context.Context, flow ContinuousReader) {
	defer r.wg.Done()
	defer close(r.audioCh)

	log := r.config.Logger
	if !waitForFlow(ctx, flow) {
		return
	}
	config := flow.ConfigInfo()

	// Use a short timeout for audio reads (5ms). The MXL SDK may use a coarse
	// internal polling interval; a long timeout (100ms) causes the SDK to hold
	// the cgo thread for extended periods, starving other flow readers and
	// reducing audio throughput to ~60% of expected. With a short timeout we
	// release the SDK quickly and retry from Go-land with fine-grained control.
	const audioTimeoutNs = 5_000_000 // 5ms

	sampleRate := flowSampleRate(config)
	channels := int(config.ChannelCount)
	// maxRead is the most samples a read takes: SamplesPerRead when set,
	// which every read then takes, or audioReadSize at the program's rate of
	// the time.
	maxRead := func() uint64 {
		if r.config.SamplesPerRead > 0 {
			return uint64(r.config.SamplesPerRead)
		}
		return uint64(audioReadSize(sampleRate, programPeriod()))
	}

	// Read position: start from the ring buffer's current write head so we
	// don't read stale samples that have already been overwritten.
	head, err := flow.HeadIndex()
	if err != nil {
		// Fallback to wall-clock approximation.
		head = CurrentIndex(config.GrainRate)
		log.Warn("mxl audio reader: HeadIndex unavailable, using wall-clock", "error", err)
	}
	// start is the first sample of the next read, which ends at
	// start+n and holds [start, start+n); its PTS is the TAI of start, so a
	// resync moves it by exactly the samples skipped. The first read
	// ends at the head.
	cr := clock.Rate{Num: config.GrainRate.Numerator, Den: config.GrainRate.Denominator}
	start := head - min(head, maxRead())

	fatalErrors := 0
	const maxFatalErrors = 50
	farAhead := false // reads are stamped beyond maxAudioHold; warned once per run

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		n := maxRead()
		var pcm [][]float32
		var err error
		if r.config.SamplesPerRead <= 0 {
			n, err = committed(flow, start, n, audioTimeoutNs)
		}
		if err == nil {
			pcm, err = flow.ReadSamples(start+n, int(n), audioTimeoutNs)
		}
		if err != nil {
			// On "too late" errors, re-sync to the current write head
			// instead of dying. The ring buffer moved past our position.
			if errors.Is(err, ErrTooLate) {
				if head, hErr := flow.HeadIndex(); hErr == nil && head >= maxRead() {
					start = head - maxRead()
					r.resyncs.Add(1)
					fatalErrors = 0
					continue
				}
			}

			// Timeout and too-early are normal waiting conditions —
			// the writer hasn't produced samples at our position yet.
			// Don't count these as errors; just retry after a brief yield.
			if errors.Is(err, ErrTimeout) || errors.Is(err, ErrTooEarly) {
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
			Start:      start,
			PTS:        clock.IndexPTS(cr, start),
		}

		// A producer may commit its audio well ahead of TAI. Handed over
		// as soon as it is read, audio more than the mixer's channel ring
		// ahead of the mixer's read point overwrites itself there and the
		// source plays silence for as long as it stays that far ahead.
		// Without a program clock there is no read point to hold for.
		var wait time.Duration
		if lead := audioReleaseLead(programPeriod()); lead > 0 {
			wait = time.Duration(NsUntilIndex(grain.Start, config.GrainRate)) - lead
		}
		switch {
		case wait > maxAudioHold:
			if !farAhead {
				log.Warn("mxl audio reader: audio stamped too far ahead of TAI to hold, handing it over as read",
					"ahead_ms", (time.Duration(NsUntilIndex(grain.Start, config.GrainRate))).Milliseconds())
			}
			farAhead = true
		case wait > 0:
			farAhead = false
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return
			}
		default:
			farAhead = false
		}

		select {
		case r.audioCh <- grain:
		case <-ctx.Done():
			return
		}

		start += n
	}
}

// committed returns how many samples from start on the next read takes:
// what the producer has committed, up to max. A producer commits a batch
// at a time, 10 ms for a GStreamer MXL writer, and a read that ends past
// the head waits for the next batch, so its first sample would reach the
// mixer that much later than it could. Read to the head, a read's first
// sample is no later than the producer's own batch makes it, whatever
// the batch's size.
//
// With nothing committed past start it waits up to timeoutNs for the next
// commit, with a read of its first sample, and returns that read's error.
// A head that does not move past a sample that could be read is not
// trusted, and max is read.
func committed(flow ContinuousReader, start, max uint64, timeoutNs uint64) (uint64, error) {
	head, err := flow.HeadIndex()
	if err != nil {
		return max, nil
	}
	if head <= start {
		if _, err := flow.ReadSamples(start+1, 1, timeoutNs); err != nil {
			return 0, err
		}
		if head, err = flow.HeadIndex(); err != nil || head <= start {
			return max, nil
		}
	}
	return min(max, head-start), nil
}

// flowSampleRate is the sample rate of an audio flow.
func flowSampleRate(config FlowConfig) int {
	if config.GrainRate.Denominator > 1 {
		return int(config.GrainRate.Float64())
	}
	return int(config.GrainRate.Numerator)
}

func (r *Reader) dataLoop(ctx context.Context, flow DiscreteReader) {
	defer r.wg.Done()
	defer close(r.dataCh)

	log := r.config.Logger
	timeoutNs := uint64(r.config.TimeoutMs) * 1_000_000

	// Start reading from the current head position.
	if !waitForFlow(ctx, flow) {
		return
	}
	index, err := flow.HeadIndex()
	if err != nil {
		log.Error("mxl data reader: failed to get head index", "error", err)
		return
	}

	drate := flow.ConfigInfo().GrainRate
	dcr := clock.Rate{Num: drate.Numerator, Den: drate.Denominator}

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
			// On "too late" errors, re-sync to the current write head
			// instead of dying. The ring buffer moved past our position.
			if errors.Is(err, ErrTooLate) {
				if headIdx, hErr := flow.HeadIndex(); hErr == nil {
					oldIndex := index
					if headIdx >= grainLookBack {
						index = headIdx - grainLookBack
					} else {
						index = headIdx
					}
					r.resyncs.Add(1)
					log.Warn("mxl data reader: ring buffer wrapped, re-syncing",
						"gap", headIdx-oldIndex, "old_index", oldIndex, "new_index", index)
					consecutiveErrors = 0
					continue
				}
			}

			// Timeout and too-early are transient — don't count as fatal errors.
			if errors.Is(err, ErrTimeout) || errors.Is(err, ErrTooEarly) {
				time.Sleep(time.Millisecond)
				continue
			}

			consecutiveErrors++
			if consecutiveErrors >= maxConsecutiveErrors {
				log.Error("mxl data reader: too many consecutive errors, stopping",
					"errors", consecutiveErrors, "last_error", err)
				return
			}
			// Brief backoff on other errors.
			time.Sleep(time.Millisecond)
			continue
		}
		consecutiveErrors = 0

		if info.Invalid {
			index++
			continue
		}

		grain := DataGrain{
			Data:  data,
			Index: index,
			PTS:   clock.IndexPTS(dcr, index),
		}

		select {
		case r.dataCh <- grain:
		case <-ctx.Done():
			return
		}

		index++
	}
}

// noFlowPoll is how often a reader loop with no flow checks for one.
const noFlowPoll = 20 * time.Millisecond

// waitForFlow returns once the flow can report a head index, or with any
// error other than ErrNoFlow, which the caller handles as before. A
// retargetable reader names no flow until it is connected, and its loop
// waits for that rather than ending. False means ctx ended first.
func waitForFlow(ctx context.Context, flow interface{ HeadIndex() (uint64, error) }) bool {
	for {
		if _, err := flow.HeadIndex(); !errors.Is(err, ErrNoFlow) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(noFlowPoll):
		}
	}
}
