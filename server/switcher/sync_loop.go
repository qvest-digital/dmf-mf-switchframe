package switcher

import (
	"runtime"
	"time"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/internal/atomicutil"
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
func (s *Switcher) synchronousProcessingLoop() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer s.syncLoopWg.Done()

	for {
		tick, ok := <-s.refClock.Ticks()
		if !ok {
			return
		}
		s.processTick(tick)
	}
}

// processTick handles one reference clock tick: audio first (never skipped),
// then video (may be skipped under overload to maintain audio continuity).
func (s *Switcher) processTick(tick clock.Tick) {
	frameStart := time.Now()

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

	// === OVERLOAD CHECK ===
	elapsed := time.Since(frameStart)
	if elapsed > tick.FrameDuration {
		s.ticksBehind = int(elapsed / tick.FrameDuration)
	}
}

// processAudioTick produces mixed PCM samples and feeds them to the AAC frame buffer.
func (s *Switcher) processAudioTick(tick clock.Tick) {
	s.mu.RLock()
	mixer := s.syncMixer
	aacBuf := s.aacBuffer
	s.mu.RUnlock()

	if mixer == nil || aacBuf == nil {
		return
	}

	// Produce exactly the number of samples the reference clock computed
	// for this frame period (Bresenham-distributed across frames).
	pcm := mixer.ProduceSamples(tick.AudioSamples)
	mixer.AdvanceCrossfade()

	if len(pcm) == 0 {
		return
	}

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
// In the sync loop, video output follows a simpler path than the async
// videoProcessingLoop. The pipeline runs synchronously, then the loop
// broadcasts the result to the program relay. When a pipeline with an
// encode node is configured, the encode node's onEncoded callback handles
// broadcasting (same as the async path). When no pipeline is configured,
// the sync loop broadcasts the raw frame data directly — useful for tests
// and configurations without an H.264 encoder.
func (s *Switcher) processVideoTick(tick clock.Tick) {
	s.mu.RLock()
	fs := s.frameSync
	programSource := s.programSource
	previewSource := s.previewSource
	engine := s.transEngine
	inTrans := s.state.isInTransition()
	s.mu.RUnlock()

	if fs == nil || programSource == "" {
		return
	}

	// === TRANSITION PATH ===
	if inTrans && engine != nil {
		s.processTransitionTick(tick, fs, programSource, previewSource, engine)
		return
	}

	// === NORMAL (non-transition) PATH ===

	// Pop the most recent raw video frame from the program source's ring buffer.
	pf := s.popProgramFrame(fs, programSource, tick.VideoPTS)
	if pf == nil {
		// No frame available — repeat last if we have one.
		s.repeatLastVideoFrame(tick.VideoPTS)
		return
	}

	// Stamp reference clock PTS onto the frame.
	pf.PTS = tick.VideoPTS
	pf.DTS = tick.VideoPTS

	// Record arrival for clock recovery (program source only).
	if cr := s.clockRecovery; cr != nil {
		changed := cr.RecordArrival(time.Now())
		if changed {
			newNum, newDen := cr.Rate()
			s.log.Info("clock recovery: rate changed",
				"fpsNum", newNum, "fpsDen", newDen,
				"measuredFPS", cr.MeasuredFPS())
			if s.refClock != nil {
				s.refClock.SetRate(newNum, newDen)
			}
		}
	}

	s.runPipelineAndBroadcast(pf, tick)
}

// processTransitionTick handles a video tick during an active transition.
// Pops frames from both program and preview sources, blends them, and
// broadcasts the result. Auto-advances the transition position.
func (s *Switcher) processTransitionTick(tick clock.Tick, fs *FrameSynchronizer, programSource, previewSource string, engine *transition.Engine) {
	// Determine the transition's from/to sources.
	fromSource := engine.FromSource()
	toSource := engine.ToSource()

	// Pop frames from both sources.
	fromFrame := s.popProgramFrame(fs, fromSource, tick.VideoPTS)
	var toFrame *ProcessingFrame
	if toSource != "" {
		toFrame = s.popProgramFrame(fs, toSource, tick.VideoPTS)
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

	// Normalize resolutions to pipeline format.
	fromFrame = s.normalizeResolution(fromFrame)
	if toFrame != nil {
		toFrame = s.normalizeResolution(toFrame)
	}

	// Auto-advance the transition position.
	frameDurationMs := float64(tick.FrameDuration) / float64(time.Millisecond)
	pos, complete := engine.AutoAdvancePosition(frameDurationMs)

	// Blend frames.
	var fromYUV, toYUV []byte
	var fromW, fromH, toW, toH int
	fromYUV = fromFrame.YUV
	fromW, fromH = fromFrame.Width, fromFrame.Height
	if toFrame != nil {
		toYUV = toFrame.YUV
		toW, toH = toFrame.Width, toFrame.Height
	}

	blended, outW, outH := engine.BlendRaw(fromYUV, fromW, fromH, toYUV, toW, toH, pos)
	if blended != nil {
		// Build a ProcessingFrame from the blended result.
		blendCopy := make([]byte, len(blended))
		copy(blendCopy, blended)
		result := &ProcessingFrame{
			YUV:    blendCopy,
			Width:  outW,
			Height: outH,
			PTS:    tick.VideoPTS,
			DTS:    tick.VideoPTS,
		}
		s.runPipelineAndBroadcast(result, tick)
	}

	// Handle transition completion.
	if complete {
		s.handleTransitionComplete(false)
	}
}

// runPipelineAndBroadcast runs the processing pipeline on a frame and broadcasts
// the result. Shared between normal and transition tick paths.
func (s *Switcher) runPipelineAndBroadcast(pf *ProcessingFrame, tick clock.Tick) {
	start := time.Now()

	// Run the CPU pipeline (key → layout → compositor → encode).
	// When the pipeline includes an encode node, it handles broadcasting
	// via the onEncoded callback. When no pipeline is configured, we
	// broadcast the raw YUV data as a video frame directly.
	hasPipeline := false
	if p := s.pipeline.Load(); p != nil {
		hasPipeline = true
		pf = p.Run(pf)
	}

	dur := time.Since(start).Nanoseconds()
	s.videoProcLastNano.Store(dur)
	s.videoProcCount.Add(1)
	atomicutil.UpdateMax(&s.videoProcMaxNano, dur)
	if dur > s.frameBudgetNs.Load() {
		s.deadlineViolations.Add(1)
	}

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

	// Store for freeze-repeat.
	s.lastEncodedFrame.Store(pf)
}

// popProgramFrame extracts the newest raw video frame from the given
// source's frame sync buffer. Returns a value copy safe for processing.
// The returned frame's YUV data is valid until the next popProgramFrame call
// for the same source.
//
// Source loss behavior (Task 14):
//   - Fresh frame available: clear sourceLostAt, return frame.
//   - No fresh frame, lastRawVideo exists: freeze (return copy of last frame).
//     If sourceLostAt is zero, set it to now.
//     If more than sourceLossTimeout has elapsed, return broadcast black.
//   - No fresh frame, no lastRawVideo: return broadcast black.
func (s *Switcher) popProgramFrame(fs *FrameSynchronizer, sourceKey string, videoPTS int64) *ProcessingFrame {
	fs.mu.Lock()
	ss, ok := fs.sources[sourceKey]
	fs.mu.Unlock()
	if !ok {
		return nil
	}

	ss.mu.Lock()
	defer ss.mu.Unlock()

	newest := ss.popNewestRawVideo()
	if newest != nil {
		// Fresh frame: update lastRawVideo, release old one.
		if ss.lastRawVideo != nil && ss.lastRawVideo != newest {
			ss.lastRawVideo.ReleaseYUV()
		}
		ss.lastRawVideo = newest

		// Clear source loss tracking — fresh frame recovered.
		if !s.sourceLostAt.IsZero() && sourceKey == s.programSourceAtomic() {
			s.log.Info("source recovered", "source", sourceKey)
			s.sourceLostAt = time.Time{}
		}

		// Return a value copy. The underlying YUV buffer is held alive by
		// ss.lastRawVideo's ref until the next fresh frame replaces it.
		cp := *newest
		return &cp
	}

	// No fresh frame — source loss path.
	if ss.lastRawVideo != nil {
		// Track when source loss began (only for program source).
		if s.sourceLostAt.IsZero() && sourceKey == s.programSourceAtomic() {
			s.sourceLostAt = time.Now()
			s.log.Warn("source lost — freezing", "source", sourceKey)
		}

		// After timeout, transition from freeze to black.
		if !s.sourceLostAt.IsZero() && time.Since(s.sourceLostAt) > sourceLossTimeout {
			pf := s.pipelineFormat.Load()
			w, h := 320, 240
			if pf != nil {
				w, h = pf.Width, pf.Height
			}
			return s.makeBroadcastBlack(w, h, videoPTS)
		}

		// Freeze-repeat: use last delivered frame.
		cp := *ss.lastRawVideo
		return &cp
	}

	// No last frame at all — return broadcast black.
	pf := s.pipelineFormat.Load()
	w, h := 320, 240
	if pf != nil {
		w, h = pf.Width, pf.Height
	}
	return s.makeBroadcastBlack(w, h, videoPTS)
}

// programSourceAtomic reads the program source under RLock.
func (s *Switcher) programSourceAtomic() string {
	s.mu.RLock()
	ps := s.programSource
	s.mu.RUnlock()
	return ps
}

// makeBroadcastBlack generates a broadcast-standard black frame (BT.709
// limited-range: Y=16, Cb=128, Cr=128) at the given resolution and PTS.
func (s *Switcher) makeBroadcastBlack(width, height int, pts int64) *ProcessingFrame {
	ySize := width * height
	uvSize := (width / 2) * (height / 2)
	buf := make([]byte, ySize+2*uvSize)
	// Y plane: BT.709 limited-range black = 16
	for i := 0; i < ySize; i++ {
		buf[i] = 16
	}
	// Cb and Cr planes: neutral chroma = 128
	for i := ySize; i < ySize+2*uvSize; i++ {
		buf[i] = 128
	}
	return &ProcessingFrame{
		YUV:    buf,
		Width:  width,
		Height: height,
		PTS:    pts,
		DTS:    pts,
	}
}

// repeatLastVideoFrame rebroadcasts the most recently encoded frame with
// an updated PTS. This keeps downstream decoders fed during overload.
// If no last frame exists (startup), generates broadcast black.
func (s *Switcher) repeatLastVideoFrame(pts int64) {
	last := s.lastEncodedFrame.Load()
	if last != nil && len(last.YUV) > 0 {
		// Deep-copy the last encoded frame and update PTS.
		cp := last.DeepCopy()
		cp.PTS = pts
		cp.DTS = pts

		// Broadcast through program relay.
		yuvCopy := make([]byte, len(cp.YUV))
		copy(yuvCopy, cp.YUV)
		frame := &media.VideoFrame{
			PTS:        pts,
			DTS:        pts,
			WireData:   yuvCopy,
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
	yuvCopy := make([]byte, len(black.YUV))
	copy(yuvCopy, black.YUV)
	frame := &media.VideoFrame{
		PTS:        pts,
		DTS:        pts,
		WireData:   yuvCopy,
		IsKeyframe: true,
	}
	s.broadcastToProgram(frame)
	s.syncLoopRepeats.Add(1)
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

// SetReferenceClock configures the Switcher to use a reference clock for
// synchronous A/V processing. Must be called before the switcher starts
// processing frames. When set, the synchronous processing loop is used
// instead of the async videoProcessingLoop.
func (s *Switcher) SetReferenceClock(rc *clock.ReferenceClock) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refClock = rc
	s.useRefClock = true
}

// SetClockRecovery attaches a clock recovery instance to the switcher.
// When set, program source frame arrivals are tracked and the reference
// clock rate is adjusted if a rate change is detected.
func (s *Switcher) SetClockRecovery(cr *clock.ClockRecovery) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clockRecovery = cr
}

// SetAACBuffer attaches an AAC frame buffer for the synchronous audio path.
func (s *Switcher) SetAACBuffer(buf *clock.AACFrameBuffer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.aacBuffer = buf
}

// SetSyncMixer attaches an audio mixer for the synchronous processing loop.
// This is the mixer whose ProduceSamples method is called on each tick.
func (s *Switcher) SetSyncMixer(m syncMixerInterface) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.syncMixer = m
}

// syncMixerInterface is the subset of audio.Mixer that the sync loop needs.
type syncMixerInterface interface {
	ProduceSamples(sampleCount int) []float32
	AdvanceCrossfade()
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
func (s *Switcher) StartSyncLoop() {
	if s.refClock == nil {
		return
	}
	s.syncLoopWg.Add(1)
	go s.synchronousProcessingLoop()
}
