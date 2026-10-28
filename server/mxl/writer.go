package mxl

import (
	"context"
	"errors"
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
// slot is the index into Writer.v210Bufs that data points into; -1 indicates
// a standalone allocation that is not part of the ring and therefore needs
// no in-flight coordination (no current producer path emits these — both the
// 8-bit and 10-bit paths use ring slots — but the ticker still honors it).
type v210Frame struct {
	data []byte
	slot int
	seq  uint64 // producer's frame count; the writer repeats a frame it has written
}

// videoWriterRef wraps a DiscreteWriter and its grain rate for atomic access.
// Go's atomic.Pointer requires a concrete pointer type, not an interface.
type videoWriterRef struct {
	writer DiscreteWriter
	rate   Rational
}

// dataWriterRef wraps a DiscreteWriter and its grain rate for atomic access.
// Same pattern as videoWriterRef — atomic.Pointer for lock-free reads.
type dataWriterRef struct {
	writer DiscreteWriter
	rate   Rational
}

// Writer writes processed video and audio to MXL flows.
//
// Video: WriteVideo converts and buffers the latest frame and wakes the video
// goroutine, which commits it to the next grain index at once, within one
// grain of the wall-clock index (see videoIndexFor). A grain nothing new
// arrived for is filled with the last frame shortly after its period ends.
// A producer at the grain rate therefore gets one grain per frame whatever
// the phase between its clock and the grain grid, and a stalled one still
// gets a grain per period.
//
// Audio uses wall-clock indices with monotonic enforcement for contiguous
// sample delivery.
type Writer struct {
	config WriterConfig
	log    *slog.Logger

	// videoRef is lock-free: accessed atomically from the ticker hot path.
	videoRef atomic.Pointer[videoWriterRef]

	// dataRef is stored atomically (SetDataWriter can race Close), but
	// WriteDataGrain holds w.mu for the duration of the cgo WriteGrain call
	// so Close cannot release the flow writer while it is in use.
	dataRef atomic.Pointer[dataWriterRef]

	// mu protects audioWriter/audioRate and Close() resource cleanup, and is
	// held across the cgo WriteSamples/WriteGrain calls in WriteAudio and
	// WriteDataGrain: Close releases the underlying C flow writers under the
	// same mutex, so an in-flight write can never hit a released handle
	// (use-after-free in C). Contention is only writer-vs-Close — the video
	// hot path (WriteVideo + videoTickLoop) does not take this mutex.
	mu          sync.Mutex
	audioWriter ContinuousWriter
	audioRate   Rational

	closed    atomic.Bool
	closeDone chan struct{} // closed when Close() completes; CAS-losers wait on it
	wg        sync.WaitGroup

	// WriteVideo stores the latest frame and signals videoKick; the video
	// goroutine writes it.
	latestV210 atomic.Pointer[v210Frame]
	videoKick  chan struct{} // buffered(1)
	videoSeq   uint64        // frames stored; producer goroutine only

	// Last written audio index for monotonic enforcement.
	// Only accessed from the single mixer output goroutine.
	lastAudioIndex uint64

	// lastDataIndex is the index of the last data grain committed, 0 before
	// the first. Guarded by mu, like the write itself.
	lastDataIndex uint64

	// Reusable de-interleave buffers to avoid per-frame allocation.
	deinterleaveBuf [][]float32

	// Ring-buffered V210 output to avoid per-frame allocation.
	// WriteVideo advances v210BufIdx around the ring; the ticker claims
	// v210InFlight = (slot it is about to consume) and then re-confirms that
	// slot still matches the latest frame before its cgo WriteGrain copy
	// (claim-then-confirm — see videoTickLoop), clearing it back to -1 after.
	// WriteVideo refuses to reuse a slot that is currently in flight (dropping
	// the frame and bumping v210Drops). Four slots give the producer headroom
	// equal to one cgo WriteGrain; the confirm step removes any dependence on
	// the claim landing within a bounded Load→Store gap. The producer's burst
	// rate must still stay well under four conversions per WriteGrain so the
	// ticker's confirm loop converges (it does at any realistic frame rate).
	v210Bufs      [4][]byte
	v210BufIdx    int
	v210InFlight  atomic.Int64 // ring slot currently held by the ticker, or -1
	v210Drops     atomic.Int64 // WriteVideo calls dropped because the ring was in-flight
	v210BufWidth  int
	v210BufHeight int
}

// NewWriter creates an MXL writer.
func NewWriter(config WriterConfig) *Writer {
	log := config.Logger
	if log == nil {
		log = slog.Default()
	}
	w := &Writer{
		config:    config,
		log:       log,
		closeDone: make(chan struct{}),
		videoKick: make(chan struct{}, 1),
	}
	// Sentinel -1 = no claim; zero-value 0 would collide with slot 0 and
	// spuriously drop the first WriteVideo after startup.
	w.v210InFlight.Store(-1)
	return w
}

// VideoDrops returns the cumulative count of WriteVideo calls dropped because
// the ring slot the producer would have overwritten was still being consumed
// by the ticker. Non-zero under sustained over-production (transition storm,
// MXL shared-memory backpressure); steady-state should be 0.
func (w *Writer) VideoDrops() int64 {
	return w.v210Drops.Load()
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

// SetDataWriter sets the discrete writer for data output (e.g., SCTE-104).
func (w *Writer) SetDataWriter(dw DiscreteWriter, grainRate Rational) {
	w.dataRef.Store(&dataWriterRef{writer: dw, rate: grainRate})
}

// WriteDataGrain writes a data grain to MXL shared memory with the current
// timestamp. Event-driven — called from the injector sink, NOT on a ticker.
// Returns an error if no data writer is set or if the writer is closed.
func (w *Writer) WriteDataGrain(data []byte) error {
	if w.closed.Load() {
		return errors.New("mxl writer: closed")
	}

	// Hold w.mu for the duration of the cgo write: Close() releases the data
	// flow writer under the same mutex, so WriteGrain can never execute on an
	// already-released C handle. (The closed.Load above is just a fast path —
	// it is a TOCTOU without the mutex.)
	w.mu.Lock()
	defer w.mu.Unlock()

	ref := w.dataRef.Load()
	if ref == nil || ref.writer == nil {
		return errors.New("mxl writer: no data writer set")
	}

	var index uint64
	if CurrentIndex != nil {
		index = CurrentIndex(ref.rate)
	}
	// Two packets in one grain period would share an index, and libmxl
	// refuses a commit that is not past the last one, so the second is
	// written to the next grain instead of being lost.
	if w.lastDataIndex > 0 && index <= w.lastDataIndex {
		index = w.lastDataIndex + 1
	}

	if err := ref.writer.WriteGrain(index, data); err != nil {
		w.log.Error("mxl writer: failed to write data grain",
			"error", err, "index", index, "len", len(data))
		return err
	}
	w.lastDataIndex = index

	return nil
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

	// Convert YUV420p → V210 using a 4-slot ring coordinated with the
	// ticker via v210InFlight. See the field doc on v210Bufs.
	buf, nextIdx, ok := w.acquireV210Slot(width, height)
	if !ok {
		return
	}
	if err := YUV420pToV210Into(yuv, buf, width, height); err != nil {
		w.log.Error("mxl writer: YUV420p→V210 conversion failed",
			"error", err, "width", width, "height", height)
		return
	}

	w.publishV210(buf, nextIdx)
}

// publishV210 makes a converted frame the latest and wakes the video
// goroutine to write it.
func (w *Writer) publishV210(buf []byte, slot int) {
	w.videoSeq++
	w.latestV210.Store(&v210Frame{data: buf, slot: slot, seq: w.videoSeq})
	w.v210BufIdx = slot
	select {
	case w.videoKick <- struct{}{}:
	default:
	}
}

// acquireV210Slot returns the next V210 ring slot for the producer to fill,
// (re)allocating the ring when the resolution changes. It returns ok=false
// (after bumping v210Drops) when the slot is currently claimed by the ticker
// — the caller must drop the frame rather than overwrite a buffer that is
// mid-cgo-WriteGrain. Called only from the single video producer goroutine
// (WriteVideo / WriteVideo10bit — both produce V210 of the same size,
// V210LineStride(width)*height).
func (w *Writer) acquireV210Slot(width, height int) ([]byte, int, bool) {
	outSize := V210LineStride(width) * height
	if w.v210BufWidth != width || w.v210BufHeight != height {
		for i := range w.v210Bufs {
			w.v210Bufs[i] = make([]byte, outSize)
		}
		w.v210BufIdx = 0
		w.v210BufWidth = width
		w.v210BufHeight = height
		// Safe to drop the claim: any ticker that captured an old-resolution
		// frame still holds a GC-retained pointer to the old backing array,
		// which is distinct memory from the freshly-allocated ring above.
		w.v210InFlight.Store(-1)
	}

	// Pick the slot after the last-published one. If the ticker is mid-
	// WriteGrain on that slot, drop the frame rather than overwriting it.
	// With 4 slots, a single-in-flight consumer always leaves at least one
	// safe slot; the drop path only fires under extreme producer bursts.
	nextIdx := (w.v210BufIdx + 1) % len(w.v210Bufs)
	if int64(nextIdx) == w.v210InFlight.Load() {
		w.v210Drops.Add(1)
		return nil, 0, false
	}
	return w.v210Bufs[nextIdx], nextIdx, true
}

// WriteVideo10bit converts a YUV422P10LE (10-bit 4:2:2 planar) frame to V210
// LOSSLESSLY and stores it for the steady-rate output ticker. Unlike WriteVideo
// (which converts 8-bit 4:2:0 with bit-depth upscale and chroma interpolation),
// this path preserves the full 10-bit precision and 4:2:2 chroma resolution.
//
// Called from a single goroutine (video processing loop).
func (w *Writer) WriteVideo10bit(yuv []byte, width, height int, pts int64) {
	if w.closed.Load() {
		return
	}

	// Skip frames that don't match the configured output resolution.
	if w.config.Width > 0 && w.config.Height > 0 {
		if width != w.config.Width || height != w.config.Height {
			return
		}
	}

	// Convert YUV422P10LE → V210 losslessly into a ring slot. V210 output is
	// V210LineStride(width)*height bytes regardless of source bit depth, so
	// the 10-bit path shares the 8-bit path's 4-slot ring (and its in-flight
	// coordination with the ticker) instead of allocating ~5.5 MB per frame.
	buf, nextIdx, ok := w.acquireV210Slot(width, height)
	if !ok {
		return
	}
	if err := YUV422P10ToV210Into(yuv, buf, width, height); err != nil {
		w.log.Error("mxl writer: YUV422P10→V210 conversion failed",
			"error", err, "width", width, "height", height)
		return
	}

	w.publishV210(buf, nextIdx)
}

// videoTickLoop writes each new frame to MXL as it arrives and fills a
// grain nothing new arrived for with the last frame.
func (w *Writer) videoTickLoop(ctx context.Context) {
	ref := w.videoRef.Load()
	if ref == nil || ref.rate.Numerator <= 0 || ref.rate.Denominator <= 0 {
		return
	}
	period := time.Duration(float64(ref.rate.Denominator) * 1e9 / float64(ref.rate.Numerator))
	// A grain is filled this long after its period ends, so a frame that
	// arrives that much late still gets it.
	grace := period / 8

	timer := time.NewTimer(period)
	defer timer.Stop()

	var last, writtenSeq uint64
	haveLast := false

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.videoKick:
		case <-timer.C:
		}
		if w.closed.Load() {
			return
		}

		frame := w.latestV210.Load()
		ref = w.videoRef.Load()
		if ref == nil || ref.writer == nil {
			return
		}
		var now uint64
		if CurrentIndex != nil {
			now = CurrentIndex(ref.rate)
		}

		if frame != nil {
			if index, ok := videoIndexFor(now, last, haveLast, frame.seq != writtenSeq); ok {
				// Claim-then-confirm: publish the ring slot we intend to read
				// so WriteVideo will drop rather than overwrite it, THEN
				// re-load latest and confirm the slot is unchanged before the
				// cgo copy. If a producer published a newer frame (and thus a
				// different slot) between our load and our claim, retry
				// against the new frame instead of memcpying a slot whose
				// protection the producer never observed. slot < 0 denotes a
				// standalone (non-ring) buffer needing no claim.
				for frame.slot >= 0 {
					w.v210InFlight.Store(int64(frame.slot))
					latest := w.latestV210.Load()
					if latest == frame {
						break
					}
					frame = latest
				}

				err := ref.writer.WriteGrain(index, frame.data)
				if frame.slot >= 0 {
					w.v210InFlight.Store(-1)
				}
				if err != nil {
					w.log.Error("mxl writer: failed to write video grain",
						"error", err, "index", index)
				} else {
					last, haveLast, writtenSeq = index, true, frame.seq
				}
			}
		}

		// Wake for the next decision: when a held frame may be written,
		// or when the next grain falls due for a fill.
		wait := period
		if haveLast && NsUntilIndex != nil {
			if f := w.latestV210.Load(); f != nil && f.seq != writtenSeq {
				// A frame newer than the last write is held because the
				// writer is a grain ahead; write it once the clock moves.
				wait = time.Duration(NsUntilIndex(last, ref.rate))
			} else {
				wait = time.Duration(NsUntilIndex(last+2, ref.rate)) + grace
			}
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(wait)
	}
}

// videoIndexFor picks the grain index for the latest frame, or reports that
// nothing is written now. now is the wall-clock grain index, last the index
// committed before.
//
// A new frame goes to the grain after the last one at once, as long as that
// is no more than one grain ahead of the clock; further ahead it is held
// until the clock moves on, and a newer frame replaces it meanwhile. This
// lets a frame arrive up to a period early or late without being lost or
// shown twice: a producer at the grain rate whose clock beats against the
// grain grid keeps one grain per frame. A frame already written is
// repeated only once the clock has passed the grain after the last one, so
// a frame that is merely late still takes that grain. Behind by more than a
// grain, the writer resumes at the grain before the clock; libmxl leaves
// the indices in between invalid.
func videoIndexFor(now, last uint64, haveLast, fresh bool) (index uint64, ok bool) {
	if !haveLast {
		return now, true
	}
	next := last + 1
	if fresh {
		if next > now+1 {
			return 0, false
		}
	} else if now <= next {
		return 0, false
	}
	if now > 0 && next < now-1 {
		return now - 1, true
	}
	return next, true
}

// WriteAudio converts interleaved float32 PCM to de-interleaved and writes samples.
// Intended to be used as an audio.RawAudioSink callback.
// Called from a single goroutine (mixer output), so deinterleaveBuf reuse is safe.
func (w *Writer) WriteAudio(pcm []float32, pts int64, sampleRate, channels int) {
	if w.closed.Load() {
		return
	}
	if channels <= 0 || len(pcm) == 0 {
		return
	}

	// Hold w.mu for the duration of the cgo write: Close() releases the audio
	// flow writer under the same mutex, so WriteSamples can never execute on
	// an already-released C handle (use-after-free). Snapshotting the writer
	// and unlocking before the call would reintroduce that race.
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.audioWriter == nil {
		return
	}
	aw := w.audioWriter
	rate := w.audioRate

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

// Close releases the writer resources and waits for goroutines to exit.
func (w *Writer) Close() error {
	if !w.closed.CompareAndSwap(false, true) {
		// Another goroutine is closing; wait for it to finish.
		<-w.closeDone
		return nil
	}
	defer close(w.closeDone)

	// Wait for worker goroutines (videoTickLoop) to exit.
	// They check w.closed on each tick and return promptly.
	w.wg.Wait()

	w.mu.Lock()
	defer w.mu.Unlock()

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
	if ref := w.dataRef.Load(); ref != nil && ref.writer != nil {
		if err := ref.writer.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		w.dataRef.Store(nil)
	}
	return firstErr
}

// Start starts background goroutines for steady-rate output.
// The video ticker writes the latest frame at the configured grain rate.
// Call this after SetVideoWriter/SetAudioWriter.
func (w *Writer) Start(ctx context.Context) {
	if ref := w.videoRef.Load(); ref != nil && ref.writer != nil {
		w.wg.Add(1)
		go func() {
			defer w.wg.Done()
			w.videoTickLoop(ctx)
		}()
	}

	// ctx.Done goroutine is NOT tracked in the WaitGroup — it calls Close(),
	// which calls wg.Wait(). Including it would deadlock.
	go func() {
		<-ctx.Done()
		_ = w.Close()
	}()
}
