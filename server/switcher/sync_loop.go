package switcher

import (
	"runtime"
	"time"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/internal/atomicutil"
)

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

	// Broadcast completed AAC frames to the program relay.
	for _, af := range frames {
		s.programRelay.BroadcastAudio(af)
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
	s.mu.RUnlock()

	if fs == nil || programSource == "" {
		return
	}

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

	start := time.Now()

	// Normalize to pipeline format resolution.
	pf = s.normalizeResolution(pf)

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
	if !hasPipeline && len(pf.YUV) > 0 {
		// Make a copy of the YUV data for the broadcast since the underlying
		// buffer is owned by the frame sync (ss.lastRawVideo) and may be
		// reused on the next tick.
		yuvCopy := make([]byte, len(pf.YUV))
		copy(yuvCopy, pf.YUV)
		frame := &media.VideoFrame{
			PTS:        pf.PTS,
			DTS:        pf.DTS,
			WireData:   yuvCopy,
			IsKeyframe: true,
		}
		s.broadcastToProgram(frame)
	}

	// Store for freeze-repeat.
	s.lastEncodedFrame.Store(pf)

	// NOTE: We do NOT call pf.ReleaseYUV() here. The frame from popProgramFrame
	// is a value copy sharing YUV data with ss.lastRawVideo in the frame sync.
	// The frame sync owns that buffer's lifecycle. If normalizeResolution
	// created a scaled copy, it already released the source via pf.ReleaseYUV()
	// and the new frame is unmanaged (allocated inline, not from pool).
}

// popProgramFrame extracts the newest raw video frame from the program
// source's frame sync buffer. Returns a value copy safe for processing.
// The returned frame's YUV data is valid until the next popProgramFrame call.
// Returns nil if no frame is available (no fresh frame and no last frame).
func (s *Switcher) popProgramFrame(fs *FrameSynchronizer, programSource string, videoPTS int64) *ProcessingFrame {
	fs.mu.Lock()
	ss, ok := fs.sources[programSource]
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
		// Return a value copy. The underlying YUV buffer is held alive by
		// ss.lastRawVideo's ref until the next fresh frame replaces it.
		cp := *newest
		return &cp
	}

	// Freeze-repeat: use last delivered frame.
	if ss.lastRawVideo != nil {
		cp := *ss.lastRawVideo
		return &cp
	}

	return nil
}

// repeatLastVideoFrame rebroadcasts the most recently encoded frame with
// an updated PTS. This keeps downstream decoders fed during overload.
func (s *Switcher) repeatLastVideoFrame(pts int64) {
	last := s.lastEncodedFrame.Load()
	if last == nil {
		return
	}
	// Create a shallow copy with updated PTS for the program relay.
	// The underlying YUV data is not needed — the encode node already
	// produced the H.264 output. We just need to signal that a frame
	// was produced at this PTS for downstream timing.
	s.syncLoopRepeats.Add(1)
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

// StartSyncLoop starts the synchronous processing loop. Must be called
// after SetReferenceClock. The reference clock must be started separately.
func (s *Switcher) StartSyncLoop() {
	if s.refClock == nil {
		return
	}
	s.syncLoopWg.Add(1)
	go s.synchronousProcessingLoop()
}

