package switcher

import (
	"runtime"
	"time"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/internal/atomicutil"
	"github.com/zsiec/switchframe/server/internal/video"
	"github.com/zsiec/switchframe/server/transition"
)

// sourceLossTimeout is the duration after which a lost source transitions
// from freeze (last frame) to broadcast black.
const sourceLossTimeout = 5 * time.Second

// synchronousProcessingLoop drives both video and audio from the reference clock.
// Each tick produces exactly one video frame and a precise number of audio samples,
// guaranteeing lockstep A/V alignment. This replaces both the FrameSynchronizer's
// timer-driven tick loop and the mixer's outputTicker.
//
// The loop exits when the reference clock's Ticks channel is closed (via Stop).
func (s *Switcher) synchronousProcessingLoop(rc *clock.ReferenceClock) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer s.syncLoopWg.Done()
	defer s.flushAACBuffer() // Issue 12: deliver partial AAC frame on shutdown

	for {
		tick, ok := <-rc.Ticks()
		if !ok {
			// A rate change replaces the clock before stopping the old
			// one; the loop follows it rather than ending.
			s.configMu.Lock()
			next, stopped := s.refClock, s.clockStopped
			s.configMu.Unlock()
			if stopped || next == rc {
				return
			}
			rc = next
			continue
		}
		// A tick whose grain has already ended when the loop takes it, as
		// the clock's catch-up delivers them after a long tick, sheds its
		// video; its audio is still produced. Only with a frame to repeat:
		// without one (first frame after a cut, GPU warmup) the late frame
		// is better than a gap.
		if rc.Realtime() && s.ticksBehind == 0 && s.lastBroadcastVFrame.Load() != nil {
			fpsNum, fpsDen := rc.Rate()
			end := clock.IndexToTimestamp(clock.Rate{Num: int64(fpsNum), Den: int64(fpsDen)}, tick.Index+1)
			if clock.TAINow() >= end {
				s.ticksBehind = 1
			}
		}
		s.processTick(tick)
	}
}

// flushAACBuffer delivers any remaining partial PCM as a padded AAC frame
// on shutdown (Issue 12).
func (s *Switcher) flushAACBuffer() {
	s.configMu.Lock()
	aacBuf := s.aacBuffer
	s.configMu.Unlock()

	if aacBuf == nil {
		return
	}

	frames, err := aacBuf.Flush()
	if err != nil {
		s.log.Warn("AAC flush error on shutdown", "err", err)
		return
	}

	relayCb := s.outputAudioCallback.Load()
	directCb := s.outputAudioCallbackDirect.Load()
	for _, af := range frames {
		if relayCb != nil {
			(*relayCb)(af)
		} else {
			s.programRelay.BroadcastAudio(af)
		}
		if directCb != nil {
			(*directCb)(af)
		}
	}
}

// processTick handles one reference clock tick: audio first (never skipped),
// then video, skipped while ticksBehind sheds late ticks.
func (s *Switcher) processTick(tick clock.Tick) {
	// Drain PTP-timed commands before any processing so timed commands
	// take effect at the frame boundary of this tick.
	s.drainCommandQueue()

	// === AUDIO FIRST (never skipped) ===
	s.processAudioTick(tick)

	// === VIDEO ===
	if s.ticksBehind > 0 {
		s.ticksBehind--
		s.overloadDrops.Add(1)
		s.logOverload()
		s.repeatLastVideoFrame(tick.VideoPTS)
		return
	}

	s.processVideoTick(tick)
}

// processAudioTick produces mixed PCM samples and feeds them to the AAC frame buffer.
func (s *Switcher) processAudioTick(tick clock.Tick) {
	s.configMu.Lock()
	mixer := s.syncMixer
	aacBuf := s.aacBuffer
	s.configMu.Unlock()

	if mixer == nil || aacBuf == nil {
		return
	}

	// One program A/V rule: the tick's audio is the sources' audio at the
	// read point, the media time its video shows, and it is stamped with
	// the tick's output time, as the video is. The program carries no A/V
	// offset whatever the mixer buffers.
	pcm := mixer.ProduceSamples(tick.ReadSample, tick.AudioSamples, tick.AudioPTS)
	mixer.AdvanceCrossfade()

	if len(pcm) == 0 {
		return
	}

	// Ticks tile the sample timeline, so the AAC frames keep counting from
	// the first tick's PTS; after a dropped tick they rebase onto it.
	if !s.audioStamped || tick.AudioStart != s.nextAudioStart {
		pending := uint64(aacBuf.Pending())
		aacBuf.SetBasePTS(tick.AudioPTS - int64(pending)*90000/programSampleRate)
		s.audioStamped = true
	}
	s.nextAudioStart = tick.AudioStart + uint64(tick.AudioSamples)

	frames, err := aacBuf.Feed(pcm)
	if err != nil {
		s.log.Warn("AAC encode error in sync loop", "err", err)
		return
	}

	// Deliver completed AAC frames via callbacks.
	relayCb := s.outputAudioCallback.Load()
	directCb := s.outputAudioCallbackDirect.Load()
	for _, af := range frames {
		// Relay callback: browser viewers via program relay.
		if relayCb != nil {
			(*relayCb)(af)
		} else {
			// Fallback: broadcast directly to program relay (backward compat
			// for tests that don't set the callback).
			s.programRelay.BroadcastAudio(af)
		}
		// Direct callback: MPEG-TS muxer for SRT/recording output.
		if directCb != nil {
			(*directCb)(af)
		}
	}
}

// processVideoTick pops the latest frame from the frame sync buffer,
// runs the pipeline (compositor, keying, encode), and broadcasts.
//
// The pipeline runs on the sync-loop goroutine. When it has an encode
// node, that node's onEncoded callback broadcasts the result; without a
// pipeline the loop broadcasts the raw frame data itself, for tests and
// configurations without an encoder.
func (s *Switcher) processVideoTick(tick clock.Tick) {
	hs := s.hotState.Load()
	fs := s.frameSync
	programSource := hs.programSource
	previewSource := hs.previewSource
	engine := hs.transEngine
	inTrans := hs.state.isInTransition()

	if fs == nil || programSource == "" {
		return
	}

	// === TRANSITION PATH ===
	if inTrans && engine != nil {
		s.processTransitionTick(tick, fs, programSource, previewSource, engine, hs.transTypeOrig)
		return
	}

	// === FTB HOLD ===
	// Faded to black: the program stays black until FTB is lifted.
	if hs.state == StateFTB {
		w, h := s.pipelineSize()
		s.programRepeats.Add(1)
		s.runPipelineAndBroadcast(s.makeBroadcastBlack(w, h, tick.VideoPTS), tick, "", tick.VideoPTS)
		return
	}

	// === NORMAL (non-transition) PATH ===

	// The newest program frame due at this tick: media time at or before
	// tick.ReadPTS, or the last one again, or black.
	pf, fresh := s.popProgramFrame(fs, programSource, tick)
	if pf == nil {
		s.repeatLastVideoFrame(tick.VideoPTS)
		return
	}
	if !fresh {
		s.programRepeats.Add(1)
	}
	srcPTS := pf.PTS
	pf = s.normalizeResolution(pf)
	pf.PTS = tick.VideoPTS
	pf.DTS = tick.VideoPTS

	// Feed the CPU key bridge / layout compositor with the program frame
	// this tick shows.
	s.feedSourceSubsystems(programSource, pf)

	s.runPipelineAndBroadcast(pf, tick, programSource, srcPTS)
}

// pipelineSize returns the pipeline frame size.
func (s *Switcher) pipelineSize() (int, int) {
	if pf := s.pipelineFormat.Load(); pf != nil {
		return pf.Width, pf.Height
	}
	return 320, 240
}

// processTransitionTick handles a video tick during an active transition.
// Pops frames from both program and preview sources, blends them, and
// broadcasts the result. Auto-advances the transition position.
//
// transTypeOrig is the original transition type string captured from the
// hotState snapshot loaded by the caller (processVideoTick). It must be passed
// in rather than re-read from s.transTypeOrig: that plain field is written
// under configMu by StartTransition/handleTransitionComplete on API-handler
// goroutines, so a lockless read here would be a data race (torn string
// header). The snapshot value is published to hotState by both writers, so it
// is consistent and lock-free.
func (s *Switcher) processTransitionTick(tick clock.Tick, fs *FrameSynchronizer, programSource, previewSource string, engine *transition.Engine, transTypeOrig string) {
	// Determine the transition's from/to sources.
	fromSource := engine.FromSource()
	toSource := engine.ToSource()

	// Pop frames from both sources.
	fromFrame, _ := s.popProgramFrame(fs, fromSource, tick)
	var toFrame *ProcessingFrame
	if toSource != "" {
		toFrame, _ = s.popProgramFrame(fs, toSource, tick)
	}

	// Use broadcast black for missing frames.
	pf := s.pipelineFormat.Load()
	w, h := 320, 240
	if pf != nil {
		w, h = pf.Width, pf.Height
	}

	if fromFrame == nil {
		fromFrame = s.makeBroadcastBlack(w, h, tick.VideoPTS)
	}
	if toFrame == nil && toSource != "" {
		toFrame = s.makeBroadcastBlack(w, h, tick.VideoPTS)
	}

	// Health is recorded in IngestRawVideo for all sources.

	// Normalize resolutions to pipeline format.
	fromFrame = s.normalizeResolution(fromFrame)
	if toFrame != nil {
		toFrame = s.normalizeResolution(toFrame)
	}

	// Auto-advance the transition position.
	frameDurationMs := float64(tick.FrameDuration) / float64(time.Millisecond)
	pos, complete := engine.AutoAdvancePosition(frameDurationMs)
	if ah := s.audioHandlers.Load().audioTransition; ah != nil {
		ah.OnTransitionPosition(pos)
	}

	// GPU-native transition path: blend on GPU using cached source frames,
	// bypassing CPU blend + upload entirely. RunTransition reads both sources
	// from the GPU source manager cache and blends them with GPU kernels.
	if h := s.gpuRunner.Load(); h != nil && h.runner != nil {
		tt := transition.Type(transTypeOrig)
		if tt == "" {
			tt = engine.TransitionType()
		}
		wipeDir := s.transitionWipeDirToGPU(engine.WipeDirection())

		// Clamp position to [0, 1].
		blendPos := pos
		if blendPos > 1.0 {
			blendPos = 1.0
		}

		// Extract stinger frame data if applicable.
		var stinger *GPUStingerFrame
		if tt == transition.Stinger {
			yuv, alpha, sw, sh, cutPt := engine.StingerFrameAt(blendPos)
			if yuv != nil && alpha != nil {
				stinger = &GPUStingerFrame{
					YUV: yuv, Alpha: alpha,
					Width: sw, Height: sh, CutPoint: cutPt,
				}
			}
		}

		pts := tick.VideoPTS
		if err := h.runner.RunTransition(fromSource, toSource, string(tt), wipeDir, blendPos, pts, stinger); err != nil {
			s.log.Debug("sync loop: GPU transition failed, falling back to CPU blend",
				"err", err, "from", fromSource, "to", toSource)
		} else {
			s.gpuTransitionFrames.Add(1)
			goto transitionDone
		}
	}

	// CPU fallback: blend on CPU and upload to GPU pipeline.
	{
		// Guard against nil/empty Data — can happen if the source is flapping.
		if len(fromFrame.Data) == 0 {
			fromFrame = s.makeBroadcastBlack(w, h, tick.VideoPTS)
		}
		if toFrame != nil && len(toFrame.Data) == 0 {
			toFrame = s.makeBroadcastBlack(w, h, tick.VideoPTS)
		}

		var fromYUV, toYUV []byte
		var fromW, fromH, toW, toH int
		fromYUV = fromFrame.Data
		fromW, fromH = fromFrame.Width, fromFrame.Height
		if toFrame != nil {
			toYUV = toFrame.Data
			toW, toH = toFrame.Width, toFrame.Height
		}

		blended, outW, outH := engine.BlendRawInto(s.transitionBlendBuf, fromYUV, fromW, fromH, toYUV, toW, toH, pos)
		s.transitionBlendBuf = blended
		if blended != nil {
			result := &ProcessingFrame{
				Data:   blended,
				Width:  outW,
				Height: outH,
				PTS:    tick.VideoPTS,
				DTS:    tick.VideoPTS,
			}
			if pfmt := s.pipelineFormat.Load(); pfmt != nil {
				result.Format, result.Codec = pfmt.Color.ColorFormat, pfmt.Color.Codec.String()
			}
			// Use the transition-specific broadcast that skips RunFromCache
			// and uploads the CPU-blended frame via RunWithUpload instead.
			s.runPipelineAndBroadcastTransition(result, tick)
		}
	}

transitionDone:

	// Release the popped (or scaled) source frames. popProgramFrame returns
	// a counted reference; the blend copied the pixels it needed, so the
	// buffers can go back to lastRawVideo's sole ownership (or the pool).
	fromFrame.ReleaseYUV()
	if toFrame != nil {
		toFrame.ReleaseYUV()
	}

	// Handle transition completion (Issue 4: dispatch to correct handler by type).
	if complete {
		ttype := engine.TransitionType()
		switch ttype {
		case transition.FTB:
			s.handleFTBComplete(false)
		case transition.FTBReverse:
			s.handleFTBReverseComplete(false)
		default:
			s.handleTransitionComplete(false)
		}
	}
}

// runPipelineAndBroadcastTransition runs the pipeline for a BLENDED transition frame.
// Unlike runPipelineAndBroadcast, this SKIPS RunFromCache because the GPU source
// manager's cache contains un-blended source frames, not the CPU-blended result.
// The blended YUV must be uploaded to GPU via RunWithUpload.
func (s *Switcher) runPipelineAndBroadcastTransition(pf *ProcessingFrame, tick clock.Tick) {
	start := time.Now()

	gpuUsed := false
	if h := s.gpuRunner.Load(); h != nil && h.runner != nil {
		// Skip RunFromCache — cache has raw source frames, not the blend.
		// Upload the CPU-blended frame directly.
		if err := h.runner.RunWithUpload(pf.Data, pf.Width, pf.Height, pf.PTS); err == nil {
			gpuUsed = true
			s.gpuFramesProcessed.Add(1)
			s.gpuTransitionFrames.Add(1)
		} else {
			s.gpuFallbackFrames.Add(1)
			s.logGPUErrorOnce("RunWithUpload (transition) failed", "err", err,
				"dataLen", len(pf.Data), "w", pf.Width, "h", pf.Height)
		}
	}

	if !gpuUsed {
		var hasPipeline bool
		pf, hasPipeline = s.runPipelineGuarded(pf)
		if !hasPipeline && len(pf.Data) > 0 {
			dataCopy := make([]byte, len(pf.Data))
			copy(dataCopy, pf.Data)
			frame := &media.VideoFrame{
				PTS:        pf.PTS,
				DTS:        pf.DTS,
				WireData:   dataCopy,
				IsKeyframe: true,
			}
			s.broadcastToProgram(frame)
		}
	}

	dur := time.Since(start).Nanoseconds()
	s.videoProcLastNano.Store(dur)
	s.videoProcCount.Add(1)
	atomicutil.UpdateMax(&s.videoProcMaxNano, dur)
	s.recordTickLag(tick)
}

// runPipelineAndBroadcast runs the processing pipeline on a frame and broadcasts
// the result. Used for normal (non-transition) tick paths.
//
// Routes through the GPU pipeline when available, falling back to the CPU
// pipeline. This mirrors the videoProcessingLoop's GPU routing logic:
//   - RunFromCache (zero-upload from GPU source manager cache)
//   - RunWithUpload (CPU→GPU upload fallback)
//   - CPU pipeline.Run (final fallback)
//
// When the GPU pipeline succeeds, its encode node handles broadcasting
// internally via its own callback — the CPU pipeline is skipped entirely.
//
// sourceKey and srcPTS name the source frame pf was made from: the GPU
// path uses that source's cached GPU frame only when it is this frame,
// and uploads pf otherwise (a repeat, black, or a newer frame cached).
// An empty sourceKey always uploads.
func (s *Switcher) runPipelineAndBroadcast(pf *ProcessingFrame, tick clock.Tick, sourceKey string, srcPTS int64) {
	start := time.Now()

	// Route through GPU pipeline if available, fall back to CPU.
	gpuUsed := false
	if h := s.gpuRunner.Load(); h != nil && h.runner != nil {
		// Try zero-upload path first: use pre-cached GPU frame from source manager.
		if sourceKey != "" {
			if err := h.runner.RunFromCache(sourceKey, srcPTS, pf.PTS); err == nil {
				gpuUsed = true
				s.gpuFramesProcessed.Add(1)
			} else {
				// A repeat older than the cached frames, or black: upload.
				s.gpuCacheMisses.Add(1)
			}
		}
		// Fall back to CPU→GPU upload if cache miss or no source key.
		if !gpuUsed {
			if err := h.runner.RunWithUpload(pf.Data, pf.Width, pf.Height, pf.PTS); err == nil {
				gpuUsed = true
				s.gpuFramesProcessed.Add(1)
			} else {
				s.logGPUErrorOnce("RunWithUpload failed", "err", err,
					"dataLen", len(pf.Data), "w", pf.Width, "h", pf.Height)
			}
		}
	}

	if !gpuUsed {
		if h := s.gpuRunner.Load(); h != nil {
			// GPU was available but failed — this is a fallback.
			s.gpuFallbackFrames.Add(1)
		}

		// Run the CPU pipeline (key → layout → compositor → encode).
		// When the pipeline includes an encode node, it handles broadcasting
		// via the onEncoded callback. When no pipeline is configured, we
		// broadcast the raw YUV data as a video frame directly.
		var hasPipeline bool
		pf, hasPipeline = s.runPipelineGuarded(pf)

		s.captureProgramThumbnail(pf)

		// When no pipeline is configured (no encoder), broadcast raw frame
		// data directly. This covers test scenarios and encoder-less configs.
		if !hasPipeline && len(pf.Data) > 0 {
			// Make a copy of the frame data for the broadcast since the underlying
			// buffer is owned by the frame sync (ss.lastRawVideo) and may be
			// reused on the next tick.
			dataCopy := make([]byte, len(pf.Data))
			copy(dataCopy, pf.Data)
			frame := &media.VideoFrame{
				PTS:        pf.PTS,
				DTS:        pf.DTS,
				WireData:   dataCopy,
				IsKeyframe: true,
			}
			s.broadcastToProgram(frame)
		}
	}

	dur := time.Since(start).Nanoseconds()
	s.videoProcLastNano.Store(dur)
	s.videoProcCount.Add(1)
	atomicutil.UpdateMax(&s.videoProcMaxNano, dur)
	if dur > s.frameBudgetNs.Load() {
		s.deadlineViolations.Add(1)
	}
	if pf.ArrivalNano > 0 {
		s.recordE2E(time.Now().UnixNano() - pf.ArrivalNano)
	}
	s.recordTickLag(tick)

	// Store for freeze-repeat, taking ownership of the caller's reference.
	// Release the previously stored frame — it held the reference from its
	// own tick (popProgramFrame returns counted references). Both the store
	// and the release happen on the sync-loop goroutine, so no reader can
	// hold the old frame concurrently.
	if old := s.lastEncodedFrame.Swap(pf); old != nil && old != pf {
		old.ReleaseYUV()
	}
}

// popProgramFrame returns the frame a source shows at this tick: the
// newest one whose media time is at or before tick.ReadPTS (less the
// source's delay), as a counted reference the caller releases. fresh is
// false for a repeat of the source's last frame, which turns to black once
// that source has delivered nothing for sourceLossTimeout, and for black
// when it has never delivered. Each source keeps its own loss time, so a
// cut never inherits another source's. Frames newer than the read point
// stay buffered for later ticks.
func (s *Switcher) popProgramFrame(fs *FrameSynchronizer, sourceKey string, tick clock.Tick) (pf *ProcessingFrame, fresh bool) {
	ss := fs.source(sourceKey)
	if ss == nil {
		return nil, false
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()

	if newest := ss.popAt(tick.ReadPTS - ss.delay.Load()); newest != nil {
		if ss.lastRawVideo != nil && ss.lastRawVideo != newest {
			ss.lastRawVideo.ReleaseYUV()
		}
		ss.lastRawVideo = newest
		if !ss.lostAt.IsZero() {
			if gap := time.Since(ss.lostAt); gap > 100*time.Millisecond {
				s.log.Info("source recovered", "source", sourceKey, "gap", gap)
			}
			ss.lostAt = time.Time{}
		}
		if newest.ArrivalNano > 0 {
			s.lastSyncWaitNs.Store(time.Now().UnixNano() - newest.ArrivalNano)
		}
		if newest.ArrivalNano > 0 && newest.DecodeStartNano > 0 {
			s.lastDecodeQueueNs.Store(newest.DecodeStartNano - newest.ArrivalNano)
		}
		if newest.DecodeStartNano > 0 && newest.DecodeEndNano > 0 {
			s.lastDecodeNs.Store(newest.DecodeEndNano - newest.DecodeStartNano)
		}
		// A reference of its own, shared with lastRawVideo: a consumer that
		// releases or writes to it cannot touch the retained frame.
		return borrowRawVideoRef(newest), true
	}

	w, h := s.pipelineSize()
	if ss.lastRawVideo == nil {
		return s.makeBroadcastBlack(w, h, tick.VideoPTS), false
	}
	// Nothing new. A source slower than the program rate repeats its frame
	// on the ticks in between; it is lost only after sourceLossTimeout.
	if ss.lostAt.IsZero() {
		ss.lostAt = time.Now()
	}
	if gap := time.Since(ss.lostAt); gap > sourceLossTimeout {
		s.logSourceLostOnce(sourceKey, gap)
		return s.makeBroadcastBlack(w, h, tick.VideoPTS), false
	}
	return borrowRawVideoRef(ss.lastRawVideo), false
}

// recordTickLag records how long after the start of its grain a tick's
// pipeline run ended: by then the raw sinks, the MXL writer among them,
// have the program frame.
func (s *Switcher) recordTickLag(tick clock.Tick) {
	s.lastTickLagNs.Store(clock.TAINow() - clock.TimestampOf(tick.VideoPTS))
}

// recordE2E records the time from a frame's arrival to its program output.
func (s *Switcher) recordE2E(ns int64) {
	s.lastE2ENs.Store(ns)
	atomicutil.UpdateMax(&s.maxE2ENs, ns)
}

// logSourceLostOnce logs a lost program source at most once per second.
func (s *Switcher) logSourceLostOnce(sourceKey string, gap time.Duration) {
	now := time.Now()
	if now.Sub(s.lastSourceLostLog) < time.Second {
		return
	}
	s.lastSourceLostLog = now
	s.log.Warn("source lost, showing black", "source", sourceKey, "duration", gap)
}

// captureProgramThumbnail encodes at most one program thumbnail a second,
// from the final 8-bit YUV420 frame.
func (s *Switcher) captureProgramThumbnail(pf *ProcessingFrame) {
	if s.thumbnailCache == nil || !s.thumbnailCache.ShouldCapture() || pf == nil ||
		pf.Format != ColorYUV420_8bit || len(pf.Data) == 0 ||
		time.Since(s.lastProgramThumbTime) < time.Second {
		return
	}
	s.lastProgramThumbTime = time.Now()
	yuvCopy := make([]byte, len(pf.Data))
	copy(yuvCopy, pf.Data)
	s.thumbnailCache.SpawnEncode("program_output", yuvCopy, pf.Width, pf.Height)
}

// borrowRawVideoRef returns a value copy of a frame retained by frame_sync
// (ring newest / lastRawVideo). The caller owns the copy and must release it
// exactly once (directly via ReleaseYUV, through normalizeResolution's scale
// path, or by transferring ownership, e.g. to lastEncodedFrame).
//
// Managed frames (refs != nil): the copy shares the refs pointer and takes
// its own reference, so a caller-side ReleaseYUV merely drops that reference
// while lastRawVideo keeps the buffer alive, and MakeWritable sees refs > 1
// and copies before any in-place mutation.
//
// Unmanaged frames (refs == nil): Ref() is a no-op while ReleaseYUV frees
// immediately (see the invariant on ProcessingFrame.Ref), so a caller-side
// release of the copy would return the pool buffer while lastRawVideo still
// aliases it — use-after-release now, double release when lastRawVideo is
// replaced. Detach the copy from the pool so frame_sync remains the single
// owner; the caller's ReleaseYUV then only nils the copy's Data field.
// Producers should ingest pool-backed frames as managed (SetRefs(1)); this
// guard keeps any remaining unmanaged producer safe rather than fast.
func borrowRawVideoRef(src *ProcessingFrame) *ProcessingFrame {
	cp := *src
	if cp.refs == nil {
		cp.pool = nil
	}
	cp.Ref()
	return &cp
}

// makeBroadcastBlack returns a broadcast-standard black frame (BT.709
// limited-range: Y=16, Cb=128, Cr=128) at the given resolution and PTS.
// The underlying buffer is cached and reused across calls at the same resolution
// (Issue 11). Caller must not modify the returned YUV data.
func (s *Switcher) makeBroadcastBlack(width, height int, pts int64) *ProcessingFrame {
	cf, codec := ColorYUV420_8bit, ""
	if pf := s.pipelineFormat.Load(); pf != nil {
		cf, codec = pf.Color.ColorFormat, pf.Color.Codec.String()
	}
	cached := s.cachedBlackFrame.Load()
	if cached == nil || cached.Width != width || cached.Height != height || cached.Format != cf {
		buf := make([]byte, cf.FrameSize(width, height))
		video.FillBlack(buf, cf, width, height)
		cached = &ProcessingFrame{Data: buf, Width: width, Height: height, Format: cf, Codec: codec, IsKeyframe: true}
		s.cachedBlackFrame.Store(cached)
	}
	// Return a value copy with updated PTS (avoids mutating the cache).
	cp := *cached
	cp.PTS = pts
	cp.DTS = pts
	return &cp
}

// repeatLastVideoFrame keeps downstream decoders fed during overload, or
// when the program source has no frame, by sending the last broadcast
// frame again under pts. The CPU pipeline's encoder emits its frames on
// its own goroutine, after the tick that queued them, so the repeat goes
// through its queue: sent from here it would overtake a frame of an
// earlier tick still encoding, and the program PTS would step back. The
// GPU pipeline and an encoder-less configuration emit on this goroutine,
// so their repeat is sent here.
func (s *Switcher) repeatLastVideoFrame(pts int64) {
	queued := false
	if s.gpuRunner.Load() == nil {
		s.withPipeline(func(p *Pipeline) {
			if enc := p.asyncEncoder(); enc != nil {
				queued = enc.queueRepeat(pts)
			}
		})
	}
	if !queued {
		s.emitRepeat(pts)
	}
}

// emitRepeat sends the last broadcast frame again under pts. Only a keyframe is repeated: it decodes on its
// own, while a second copy of a predicted frame would apply its slice to
// the picture twice. After a predicted frame nothing is sent and decoders
// hold the last picture; re-encoding the raw frame instead would cost the
// encode an overloaded tick skips. With no frame broadcast yet it sends
// the last raw frame of an encoder-less configuration, or broadcast black.
func (s *Switcher) emitRepeat(pts int64) {
	if last := s.lastBroadcastVFrame.Load(); last != nil && len(last.WireData) > 0 {
		if !last.IsKeyframe {
			return
		}
		// Copy WireData to avoid aliasing the stored frame.
		wireCopy := make([]byte, len(last.WireData))
		copy(wireCopy, last.WireData)
		frame := &media.VideoFrame{
			PTS:        pts,
			DTS:        pts,
			WireData:   wireCopy,
			IsKeyframe: true,
		}
		s.broadcastToProgram(frame)
		s.syncLoopRepeats.Add(1)
		return
	}

	// Fallback: try the last raw YUV frame (for encoder-less test configs).
	last := s.lastEncodedFrame.Load()
	if last != nil && len(last.Data) > 0 {
		dataCopy := make([]byte, len(last.Data))
		copy(dataCopy, last.Data)
		frame := &media.VideoFrame{
			PTS:        pts,
			DTS:        pts,
			WireData:   dataCopy,
			IsKeyframe: true,
		}
		s.broadcastToProgram(frame)
		s.syncLoopRepeats.Add(1)
		return
	}

	// No last frame (startup) — generate broadcast black.
	pf := s.pipelineFormat.Load()
	w, h := 320, 240
	if pf != nil {
		w, h = pf.Width, pf.Height
	}
	black := s.makeBroadcastBlack(w, h, pts)
	dataCopy := make([]byte, len(black.Data))
	copy(dataCopy, black.Data)
	frame := &media.VideoFrame{
		PTS:        pts,
		DTS:        pts,
		WireData:   dataCopy,
		IsKeyframe: true,
	}
	s.broadcastToProgram(frame)
	s.syncLoopRepeats.Add(1)
}

// feedSourceSubsystems updates the CPU fill paths (key bridge, layout/DVE
// compositor) with the program frame a tick shows, the frame-sync choice
// at the tick's read point. It does nothing with a GPU source manager.
//
// The GPU source manager is deliberately not updated here: it is already
// fed by the producer goroutine via IngestRawVideo (switcher.go writes
// pf.Data + pf.PTS into entry.current atomically on every push). Feeding
// it a second time from the sync-loop tick created a two-writer race on
// entry.current — a freeze-repeat frame from popProgramFrame could
// overwrite a newer frame the producer had just pushed, and the next
// RunFromCache would then read that stale content back under the tick's
// (newer) PTS. The built-in color bars sprite showed this as burst
// reorder/jumping on program output: frame F:N was encoded after F:N+k.
func (s *Switcher) feedSourceSubsystems(sourceKey string, pf *ProcessingFrame) {
	if h := s.gpuSourceMgr.Load(); h != nil && h.mgr != nil {
		return
	}

	s.configMu.Lock()
	keyBridge := s.keyBridge
	dveComp := s.dveCompositor
	s.configMu.Unlock()

	if pf.Format == ColorYUV422_10bit {
		if keyBridge != nil {
			keyBridge.IngestFillYUV10(sourceKey, pf.Data, pf.Width, pf.Height)
		}
	} else {
		if keyBridge != nil {
			keyBridge.IngestFillYUV(sourceKey, pf.Data, pf.Width, pf.Height)
		}
		if dveComp != nil && dveComp.NeedsSource(sourceKey) {
			dveComp.IngestSourceFrame(sourceKey, pf.Data, pf.Width, pf.Height, pf.PTS)
		}
	}
}

// logOverload logs overload drops at most once per second to avoid flooding.
func (s *Switcher) logOverload() {
	now := time.Now()
	if now.Sub(s.lastOverloadLog) >= time.Second {
		s.lastOverloadLog = now
		s.log.Warn("sync loop overload — skipping video",
			"ticksBehind", s.ticksBehind,
			"totalDrops", s.overloadDrops.Load())
	}
}

// logGPUErrorOnce logs a GPU pipeline error at most once per second.
func (s *Switcher) logGPUErrorOnce(msg string, args ...any) {
	now := time.Now()
	if now.Sub(s.lastGPUErrorLog) >= time.Second {
		s.lastGPUErrorLog = now
		s.log.Warn(msg, args...)
	}
}

// SetReferenceClock sets the clock that drives the sync loop. Tests pass a
// non-realtime one; Start makes a realtime one at the pipeline rate when
// none is set.
func (s *Switcher) SetReferenceClock(rc *clock.ReferenceClock) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	s.refClock = rc
}

// Start runs the program: the reference clock and the sync loop it drives,
// the one goroutine that produces program video and audio.
func (s *Switcher) Start() {
	s.configMu.Lock()
	s.clockStopped = false
	if s.refClock == nil {
		pf := s.pipelineFormat.Load()
		s.refClock = clock.NewReferenceClock(pf.FPSNum, pf.FPSDen, programSampleRate, clock.PTSBase, true)
	}
	rc := s.refClock
	s.configMu.Unlock()
	s.StartSyncLoop()
	rc.Start()
}

// stopClock stops the reference clock and waits for the sync loop to end.
func (s *Switcher) stopClock() {
	s.configMu.Lock()
	s.clockStopped = true
	rc := s.refClock
	s.configMu.Unlock()
	if rc == nil {
		return
	}
	rc.Stop()
	s.syncLoopWg.Wait()
	s.syncLoopRunning.Store(false)
}

// restartClockAt replaces a realtime clock whose rate is not the pipeline's.
// It never waits for the sync loop, so it is safe on the loop goroutine
// (a timed format change): the new clock is installed and started before
// the old one stops, and the loop moves over to it when the old clock's
// Ticks channel closes.
func (s *Switcher) restartClockAt(fpsNum, fpsDen int) {
	s.configMu.Lock()
	old := s.refClock
	if old == nil || !old.Realtime() || s.clockStopped {
		s.configMu.Unlock()
		return
	}
	if n, d := old.Rate(); n == fpsNum && d == fpsDen {
		s.configMu.Unlock()
		return
	}
	s.refClock = clock.NewReferenceClock(fpsNum, fpsDen, programSampleRate, clock.PTSBase, true)
	if s.syncLoopRunning.Load() {
		s.refClock.Start()
	}
	s.configMu.Unlock()
	old.Stop()
}

// programSampleRate is the program audio sample rate.
const programSampleRate = 48000

// SetAACBuffer attaches an AAC frame buffer for the synchronous audio path.
func (s *Switcher) SetAACBuffer(buf *clock.AACFrameBuffer) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	s.aacBuffer = buf
}

// SetSyncMixer attaches an audio mixer for the synchronous processing loop.
// This is the mixer whose ProduceSamples method is called on each tick.
func (s *Switcher) SetSyncMixer(m syncMixerInterface) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	s.syncMixer = m
}

// syncMixerInterface is the subset of audio.Mixer that the sync loop needs.
type syncMixerInterface interface {
	ProduceSamples(start uint64, sampleCount int, pts int64) []float32
	AdvanceCrossfade()
}

// sourceDelayMixer is a mixer that delays a source's audio by its source
// delay (audio.Mixer).
type sourceDelayMixer interface {
	SetSourceDelay(sourceKey string, delayMs int)
}

// SetOutputAudioCallback registers a callback invoked for each AAC frame
// produced by the sync loop. Intended for broadcasting to the program relay
// (browser viewers). Only used in reference clock mode.
func (s *Switcher) SetOutputAudioCallback(fn func(*media.AudioFrame)) {
	if fn != nil {
		s.outputAudioCallback.Store(&fn)
	} else {
		s.outputAudioCallback.Store(nil)
	}
}

// SetOutputAudioCallbackDirect registers a callback invoked for each AAC
// frame produced by the sync loop, for direct delivery to the MPEG-TS muxer
// (SRT/recording output). Only used in reference clock mode.
func (s *Switcher) SetOutputAudioCallbackDirect(fn func(*media.AudioFrame)) {
	if fn != nil {
		s.outputAudioCallbackDirect.Store(&fn)
	} else {
		s.outputAudioCallbackDirect.Store(nil)
	}
}

// StartSyncLoop starts the synchronous processing loop. Must be called
// after SetReferenceClock. The reference clock must be started separately.
// Safe to call multiple times — second call is a no-op (Issue 10).
func (s *Switcher) StartSyncLoop() {
	s.configMu.Lock()
	rc := s.refClock
	s.configMu.Unlock()
	if rc == nil {
		return
	}
	if s.syncLoopRunning.Swap(true) {
		return // already running
	}
	s.syncLoopWg.Add(1)
	go s.synchronousProcessingLoop(rc)
}
