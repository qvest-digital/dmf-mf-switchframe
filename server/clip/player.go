package clip

import (
	"context"
	"log/slog"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/switchframe/server/codec"
)

// PlayerConfig configures a clip player instance.
type PlayerConfig struct {
	Clip       []bufferedFrame
	AudioClip  []bufferedAudioFrame
	Speed      float64
	Loop       bool
	InitialPTS int64
	Width      int // frame width (from clip metadata)
	Height     int // frame height (from clip metadata)

	// DecodeFrame decodes H.264 wire data to raw YUV420 planar bytes.
	// If nil, wireData is passed through unchanged (for testing).
	DecodeFrame func(h264 []byte) (yuv []byte, width, height int, err error)

	// Decoder is the underlying decoder instance for drain support.
	// If it implements DrainableDecoder, remaining buffered frames are
	// drained after playback via SendEOS + ReceiveFrame.
	Decoder VideoDecoder

	// EncoderFactory creates a VideoEncoder for re-encoding decoded frames
	// to browser-compatible 8-bit H.264. If nil, original wire data is sent
	// to the browser relay (only works for 8-bit clips).
	// fpsNum/fpsDen express the frame rate as a rational number
	// (e.g. 30000/1001 for 29.97fps, 25000/1000 for 25fps).
	EncoderFactory func(w, h, fpsNum, fpsDen int) (VideoEncoder, error)

	// RawVideoOutput sends decoded YUV420 frame data to the switcher pipeline.
	// Called for every output frame (including duplicates for slow-mo).
	RawVideoOutput func(yuv []byte, w, h int, pts int64, isKeyframe bool)

	// VideoOutput forwards H.264 wire data (AVC1 format) for browser relay.
	// When EncoderFactory is set, this receives re-encoded 8-bit data.
	// Otherwise, it receives original wire data from the clip file.
	// Called for every output frame (including duplicates for slow-mo).
	// sps/pps are set on keyframes, nil on non-keyframes.
	VideoOutput func(wireData []byte, pts int64, isKeyframe bool, sps, pps []byte)

	// AudioOutput sends audio frame data during playback.
	AudioOutput func(data []byte, pts int64, sampleRate, channels int)

	// OnDone is called when playback ends (non-loop clip finishes) or is stopped.
	OnDone func()

	// OnVideoInfo signals codec parameters (SPS/PPS) and dimensions.
	// Called once on the first keyframe with SPS/PPS.
	OnVideoInfo func(sps, pps []byte, width, height int)
}

// Player plays back a pre-demuxed clip with support for pause, seek,
// variable speed (0.25x-2.0x), hold-last-frame, and loop.
type Player struct {
	config   PlayerConfig
	state    atomic.Value // PlayerState
	cancel   context.CancelFunc
	done     chan struct{}
	once     sync.Once
	progress atomic.Int64 // 0-1000 (milliprogress for atomic storage)

	// Pause mechanism: pauseCh is a channel that blocks while paused.
	// When unpaused, it is a closed channel (non-blocking receive).
	// When paused, it is replaced with a new open channel.
	pauseCh atomic.Pointer[chan struct{}]
	pauseMu sync.Mutex

	// Seek: send desired position (0.0-1.0) via seekCh.
	seekCh chan float64

	// Speed changes mid-playback.
	speed atomic.Value // float64

	// Hold frame: the last output frame for hold/pause output.
	holdMu    sync.Mutex
	holdFrame []byte
	holdW     int
	holdH     int
	holdPTS   int64

	// Last keyframe wire data for hold mode browser relay output.
	// The browser decoder needs a keyframe to start, so we always send
	// the last keyframe's wire data (not the last frame which may be non-IDR).
	holdKeyWireData []byte
	holdKeySPS      []byte
	holdKeyPPS      []byte

	// videoInfoSent tracks whether OnVideoInfo has been called.
	videoInfoSent bool

	// Re-encoding for browser relay (8-bit H.264 output).
	encoder  VideoEncoder
	encoderW int // dimensions encoder was created with
	encoderH int

	sourceFPS float64

	// Reusable scratch buffers for decode/re-encode hot path.
	// annexBBuf and prependBuf must be separate allocations because
	// PrependSPSPPS reads from annexBBuf while writing to prependBuf.
	annexBBuf  []byte // AVC1→Annex B conversion
	prependBuf []byte // SPS/PPS prepend (separate buffer, never aliased with annexBBuf)
	avc1Buf    []byte // Annex B→AVC1 re-encode output

	// Decode stats.
	decodeAttempts   int
	decodeSuccesses  int
	decodeBuffering  int // EAGAIN count (expected for B-frame reordering)
	decodeErrors     int
	decodeErrorFirst string

	// Audio tracking for proportional distribution.
	audioIdx          int
	audioCallCount    int
	totalOutputFrames int
	outputAudioPTS    int64
}

// NewPlayer creates a new clip player. The player starts in StateLoaded
// (or StateEmpty if the clip is nil/empty).
func NewPlayer(config PlayerConfig) *Player {
	// Default dimensions if not specified.
	if config.Width <= 0 {
		config.Width = 1920
	}
	if config.Height <= 0 {
		config.Height = 1080
	}

	p := &Player{
		config: config,
		done:   make(chan struct{}),
		seekCh: make(chan float64, 1),
	}

	// Initialize speed.
	speed := config.Speed
	if speed <= 0 {
		speed = 1.0
	}
	p.speed.Store(speed)

	// Initialize pause channel as closed (not paused).
	ch := make(chan struct{})
	close(ch)
	p.pauseCh.Store(&ch)

	// Set initial state.
	if len(config.Clip) == 0 {
		p.state.Store(StateEmpty)
	} else {
		p.state.Store(StateLoaded)
	}

	return p
}

// Start begins playback in a background goroutine.
func (p *Player) Start(ctx context.Context) {
	ctx, p.cancel = context.WithCancel(ctx)
	go p.run(ctx)
}

// Stop cancels playback.
func (p *Player) Stop() {
	p.once.Do(func() {
		if p.cancel != nil {
			p.cancel()
		}
	})
}

// Wait blocks until the player finishes.
func (p *Player) Wait() {
	<-p.done
}

// State returns the current player state.
func (p *Player) State() PlayerState {
	v := p.state.Load()
	if v == nil {
		return StateEmpty
	}
	return v.(PlayerState)
}

// Progress returns the current playback progress as a float64 between 0.0 and 1.0.
func (p *Player) Progress() float64 {
	return float64(p.progress.Load()) / 1000.0
}

// Pause pauses playback, holding the current frame.
func (p *Player) Pause() {
	p.pauseMu.Lock()
	defer p.pauseMu.Unlock()

	state := p.State()
	if state != StatePlaying {
		return
	}

	ch := make(chan struct{})
	p.pauseCh.Store(&ch)
	p.state.Store(StatePaused)
}

// Resume resumes playback from a paused state.
func (p *Player) Resume() {
	p.pauseMu.Lock()
	defer p.pauseMu.Unlock()

	state := p.State()
	if state != StatePaused {
		return
	}

	p.state.Store(StatePlaying)
	// Close the pause channel to unblock the playback loop.
	chPtr := p.pauseCh.Load()
	if chPtr != nil {
		select {
		case <-*chPtr:
			// Already closed.
		default:
			close(*chPtr)
		}
	}
}

// Seek seeks to the given position (0.0-1.0) within the clip.
func (p *Player) Seek(position float64) {
	if position < 0.0 {
		position = 0.0
	}
	if position > 1.0 {
		position = 1.0
	}
	// Non-blocking send; if a seek is already pending, replace it.
	select {
	case p.seekCh <- position:
	default:
		// Drain and resend.
		select {
		case <-p.seekCh:
		default:
		}
		select {
		case p.seekCh <- position:
		default:
		}
	}
}

// SetSpeed changes the playback speed. Valid range is 0.25 to 2.0.
func (p *Player) SetSpeed(speed float64) {
	if speed < 0.25 {
		speed = 0.25
	}
	if speed > 2.0 {
		speed = 2.0
	}
	p.speed.Store(speed)
}

// run is the main playback goroutine.
func (p *Player) run(ctx context.Context) {
	defer close(p.done)
	defer func() {
		if p.encoder != nil {
			p.encoder.Close()
			p.encoder = nil
		}
	}()

	clip := p.config.Clip
	if len(clip) == 0 {
		if p.config.OnDone != nil {
			p.config.OnDone()
		}
		return
	}

	// Estimate source FPS from clip PTS values.
	sourceFPS := estimateFPSFromClipFrames(clip)
	p.sourceFPS = sourceFPS

	p.state.Store(StatePlaying)

	outputPTS := p.config.InitialPTS
	p.outputAudioPTS = p.config.InitialPTS

	for {
		cancelled := p.playClip(ctx, clip, sourceFPS, &outputPTS)
		if cancelled {
			return
		}

		if !p.config.Loop {
			// Non-loop: transition to holding, notify, then hold loop.
			// State must be set before OnDone so callers reading State()
			// after OnDone see StateHolding, not StatePlaying.
			p.state.Store(StateHolding)
			if p.config.OnDone != nil {
				p.config.OnDone()
			}
			p.holdLoop(ctx, &outputPTS)
			return
		}

		// Loop: reset frame index, PTS continues monotonically.
		p.audioIdx = 0
		p.audioCallCount = 0
	}
}

// playClip plays through all frames once. Returns true if context was cancelled.
func (p *Player) playClip(ctx context.Context, clip []bufferedFrame, sourceFPS float64, outputPTS *int64) bool {
	totalClipFrames := len(clip)

	speed := p.speed.Load().(float64)
	dupCount, ptsPerFrame := computeClipTiming(speed, sourceFPS)
	frameDuration := computeFrameDuration(speed, sourceFPS, dupCount)

	// Compute total output frames for progress tracking.
	totalFrames := totalClipFrames * dupCount
	p.totalOutputFrames = totalFrames

	// Create pacing timer.
	timer := time.NewTimer(frameDuration)
	defer timer.Stop()
	if !timer.Stop() {
		<-timer.C
	}

	playbackStart := time.Now()
	pacingIdx := 0
	outputIdx := 0

	for frameIdx := 0; frameIdx < totalClipFrames; {
		// Check for seek requests (non-blocking).
		select {
		case pos := <-p.seekCh:
			targetIdx := int(pos * float64(totalClipFrames))
			if targetIdx >= totalClipFrames {
				targetIdx = totalClipFrames - 1
			}
			if targetIdx < 0 {
				targetIdx = 0
			}
			frameIdx = targetIdx
			outputIdx = targetIdx * dupCount
			// Reset pacing to avoid a burst of catch-up frames.
			playbackStart = time.Now()
			pacingIdx = 0
			// Update audio index proportionally.
			if len(p.config.AudioClip) > 0 {
				p.audioIdx = targetIdx * len(p.config.AudioClip) / totalClipFrames
				p.audioCallCount = outputIdx
			}
			continue
		default:
		}

		// Check for speed changes.
		newSpeed := p.speed.Load().(float64)
		if newSpeed != speed {
			speed = newSpeed
			dupCount, ptsPerFrame = computeClipTiming(speed, sourceFPS)
			frameDuration = computeFrameDuration(speed, sourceFPS, dupCount)
			totalFrames = totalClipFrames * dupCount
			p.totalOutputFrames = totalFrames
			outputIdx = frameIdx * dupCount
			playbackStart = time.Now()
			pacingIdx = 0
		}

		f := clip[frameIdx]

		// Decode H.264 to YUV420 for the switcher pipeline.
		// Wire data forwarding to the browser relay is independent of decode
		// success — the browser has its own decoder and always gets frames.
		var frameData []byte // nil when decode fails (browser relay still works)
		fw, fh := p.config.Width, p.config.Height
		if p.config.DecodeFrame != nil {
			// Convert AVC1 wire data to Annex B for the decoder (reuses buffer).
			p.annexBBuf = codec.AVC1ToAnnexBInto(f.wireData, p.annexBBuf[:0])
			if len(p.annexBBuf) > 0 {
				// Prepend SPS/PPS for keyframes so the decoder can initialize.
				// Use prependBuf as a separate destination to avoid aliasing
				// (PrependSPSPPS reads from annexBBuf while writing to dst).
				decoderInput := p.annexBBuf
				if f.isKeyframe && len(f.sps) > 0 {
					p.prependBuf = codec.PrependSPSPPSInto(f.sps, f.pps, p.annexBBuf, p.prependBuf[:0])
					decoderInput = p.prependBuf
				}

				p.decodeAttempts++
				decoded, dw, dh, err := p.config.DecodeFrame(decoderInput)
				if err == nil {
					p.decodeSuccesses++
					// Deep-copy: decoder may reuse its output buffer.
					frameData = make([]byte, len(decoded))
					copy(frameData, decoded)
					fw, fh = dw, dh
				} else {
					errMsg := err.Error()
					// EAGAIN ("buffering") is expected for B-frame reordering —
					// the decoder needs reference frames before it can output.
					if strings.Contains(errMsg, "buffering") {
						p.decodeBuffering++
					} else {
						p.decodeErrors++
						if p.decodeErrorFirst == "" {
							p.decodeErrorFirst = errMsg
							slog.Warn("clip: decode error",
								"error", err,
								"frameIdx", frameIdx,
								"isKeyframe", f.isKeyframe,
								"wireDataLen", len(f.wireData),
								"annexBLen", len(p.annexBBuf),
								"hasSPS", len(f.sps) > 0,
							)
						}
					}
				}
			}
		} else {
			// No decoder (testing) — pass wire data through as frame data.
			frameData = f.wireData
		}

		// Re-encode decoded frame to browser-compatible 8-bit H.264.
		// Falls back to original wire data when no encoder factory is set.
		browserWireData := f.wireData
		browserKeyframe := f.isKeyframe
		browserSPS := f.sps
		browserPPS := f.pps
		if frameData != nil {
			if wd, kf, s, pp := p.reencodeForBrowser(frameData, fw, fh, *outputPTS, f.isKeyframe); wd != nil {
				browserWireData = wd
				browserKeyframe = kf
				browserSPS = s
				browserPPS = pp
			}
		}

		// Signal codec parameters on first keyframe with SPS/PPS.
		if browserKeyframe && !p.videoInfoSent && p.config.OnVideoInfo != nil {
			if browserSPS != nil && browserPPS != nil {
				p.config.OnVideoInfo(browserSPS, browserPPS, fw, fh)
				p.videoInfoSent = true
			}
		}

		// Output frames with duplication for slow-mo.
		for dup := 0; dup < dupCount; dup++ {
			if p.outputFrame(ctx, frameData, fw, fh, f.isKeyframe, browserWireData, browserKeyframe, browserSPS, browserPPS, outputPTS, ptsPerFrame, frameDuration, timer, playbackStart, &pacingIdx) {
				return true
			}

			// Save hold frame state.
			p.holdMu.Lock()
			if frameData != nil {
				// Deep-copy YUV (prevents aliasing with decoder buffer).
				// Reuse existing holdFrame buffer when capacity is sufficient.
				if cap(p.holdFrame) >= len(frameData) {
					p.holdFrame = p.holdFrame[:len(frameData)]
				} else {
					p.holdFrame = make([]byte, len(frameData))
				}
				copy(p.holdFrame, frameData)
				p.holdW = fw
				p.holdH = fh
			}
			p.holdPTS = *outputPTS
			// Track last keyframe wire data for hold mode browser relay.
			// The browser decoder needs a keyframe to start decoding.
			if browserKeyframe && browserSPS != nil && browserPPS != nil {
				p.holdKeyWireData = append(p.holdKeyWireData[:0], browserWireData...)
				p.holdKeySPS = browserSPS
				p.holdKeyPPS = browserPPS
			}
			p.holdMu.Unlock()

			outputIdx++
			if totalFrames > 0 {
				prog := int64(outputIdx * 1000 / totalFrames)
				if prog > 1000 {
					prog = 1000
				}
				p.progress.Store(prog)
			}

			// Emit audio proportionally.
			p.emitAudioForVideoFrame(outputIdx, totalFrames)
		}
		frameIdx++
	}

	// Drain remaining buffered frames from the decoder (B-frame reordering).
	if p.config.Decoder != nil {
		if drainable, ok := p.config.Decoder.(DrainableDecoder); ok {
			_ = drainable.SendEOS()
			for {
				decoded, dw, dh, err := drainable.ReceiveFrame()
				if err != nil {
					break
				}
				p.decodeSuccesses++
				frameData := make([]byte, len(decoded))
				copy(frameData, decoded)
				if p.config.RawVideoOutput != nil {
					p.config.RawVideoOutput(frameData, dw, dh, *outputPTS, false)
				}
				num, den := fpsToRational(p.sourceFPS)
				*outputPTS += int64(90000) * int64(den) / int64(num)
			}
		}
	}

	// Log decode summary.
	slog.Info("clip: playback complete",
		"frames", len(clip),
		"fps", p.sourceFPS,
		"decodeAttempts", p.decodeAttempts,
		"decodeSuccesses", p.decodeSuccesses,
		"decodeBuffering", p.decodeBuffering,
		"decodeErrors", p.decodeErrors,
		"decodeErrorFirst", p.decodeErrorFirst,
	)

	// Mark final progress.
	p.progress.Store(1000)

	return false
}

// outputFrame outputs a single decoded frame, handling pause and pacing.
// It sends both decoded YUV (for switcher pipeline) and H.264 wire data
// (for browser relay) if the respective callbacks are set.
// srcKeyframe is the source frame's keyframe flag (for the pipeline encoder).
// wireData/isKeyframe/sps/pps may be re-encoded (8-bit) for browser compatibility.
// Returns true if context was cancelled.
func (p *Player) outputFrame(
	ctx context.Context,
	frameData []byte, fw, fh int, srcKeyframe bool,
	wireData []byte, isKeyframe bool, sps, pps []byte,
	outputPTS *int64,
	ptsPerFrame int64,
	frameDuration time.Duration,
	timer *time.Timer,
	playbackStart time.Time,
	pacingIdx *int,
) bool {
	// Check for pause. If paused, this blocks until resumed or cancelled.
	chPtr := p.pauseCh.Load()
	if chPtr != nil {
		select {
		case <-*chPtr:
			// Not paused or just resumed.
		case <-ctx.Done():
			return true
		}
	}

	// Check context before output.
	select {
	case <-ctx.Done():
		return true
	default:
	}

	// Output decoded YUV video to switcher pipeline (only when decode succeeded).
	if frameData != nil && p.config.RawVideoOutput != nil {
		p.config.RawVideoOutput(frameData, fw, fh, *outputPTS, srcKeyframe)
	}

	// Forward H.264 wire data to browser relay (re-encoded or original).
	if p.config.VideoOutput != nil {
		p.config.VideoOutput(wireData, *outputPTS, isKeyframe, sps, pps)
	}

	// Pace using absolute time to prevent drift from callback overhead.
	deadline := playbackStart.Add(time.Duration(*pacingIdx) * frameDuration)
	wait := time.Until(deadline)
	if wait > 0 {
		timer.Reset(wait)
		select {
		case <-ctx.Done():
			return true
		case <-timer.C:
		}
	}

	*outputPTS += ptsPerFrame
	*pacingIdx++

	return false
}

// emitAudioForVideoFrame distributes audio frames proportionally across
// video output frames using the same pattern as the replay player.
func (p *Player) emitAudioForVideoFrame(outputIdx, totalFrames int) {
	audioClip := p.config.AudioClip
	if len(audioClip) == 0 || p.config.AudioOutput == nil {
		return
	}

	p.audioCallCount = outputIdx
	targetAudioIdx := p.audioCallCount * len(audioClip) / p.totalOutputFrames
	if targetAudioIdx > len(audioClip) {
		targetAudioIdx = len(audioClip)
	}

	for p.audioIdx < targetAudioIdx {
		af := &audioClip[p.audioIdx]
		p.config.AudioOutput(af.data, p.outputAudioPTS, af.sampleRate, af.channels)
		if af.sampleRate > 0 {
			p.outputAudioPTS += int64(1024) * 90000 / int64(af.sampleRate)
		}
		p.audioIdx++
	}
}

// holdLoop transitions to StateHolding and outputs the last frame at 1fps
// to keep the source alive for health checks. Blocks until context is done.
func (p *Player) holdLoop(ctx context.Context, outputPTS *int64) {
	p.state.Store(StateHolding)

	p.holdMu.Lock()
	holdData := p.holdFrame
	holdW := p.holdW
	holdH := p.holdH
	// Use last keyframe for browser relay (decoder needs IDR to start).
	holdKeyWire := p.holdKeyWireData
	holdKeySPS := p.holdKeySPS
	holdKeyPPS := p.holdKeyPPS
	p.holdMu.Unlock()

	// Nothing to output.
	if holdData == nil && holdKeyWire == nil {
		return
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if holdData != nil && p.config.RawVideoOutput != nil {
				p.config.RawVideoOutput(holdData, holdW, holdH, *outputPTS, true)
			}
			// Send last keyframe wire data so late-joining browsers can decode.
			if holdKeyWire != nil && p.config.VideoOutput != nil {
				p.config.VideoOutput(holdKeyWire, *outputPTS, true, holdKeySPS, holdKeyPPS)
			}
			*outputPTS += 90000 // 1 second in 90kHz PTS
		}
	}
}

// reencodeForBrowser re-encodes a decoded YUV frame to 8-bit H.264 for
// browser relay output. Creates the encoder lazily on first call.
// Returns nil wireData if no encoder factory is set or encoding fails.
func (p *Player) reencodeForBrowser(yuv []byte, w, h int, pts int64, isKeyframe bool) (wireData []byte, isKF bool, sps, pps []byte) {
	if p.config.EncoderFactory == nil {
		return nil, false, nil, nil
	}

	// Recreate encoder if dimensions changed (e.g., variable-resolution clip).
	if p.encoder != nil && (w != p.encoderW || h != p.encoderH) {
		p.encoder.Close()
		p.encoder = nil
	}

	// Lazy-create encoder on first decoded frame or after dimension change.
	if p.encoder == nil {
		fpsNum, fpsDen := fpsToRational(p.sourceFPS)
		enc, err := p.config.EncoderFactory(w, h, fpsNum, fpsDen)
		if err != nil {
			slog.Warn("clip: failed to create browser encoder", "error", err, "w", w, "h", h)
			return nil, false, nil, nil
		}
		p.encoder = enc
		p.encoderW = w
		p.encoderH = h
	}

	encoded, encKF, err := p.encoder.Encode(yuv, pts, isKeyframe)
	if err != nil || len(encoded) == 0 {
		return nil, false, nil, nil
	}

	// FFmpeg encoder returns Annex B — convert to AVC1 for the relay (reuses buffer).
	p.avc1Buf = codec.AnnexBToAVC1Into(encoded, p.avc1Buf[:0])
	// Deep-copy: downstream consumers (BroadcastVideo) retain frame references
	// in the GOP cache and send them asynchronously via channels. Without a copy,
	// the next encode cycle overwrites the aliased scratch buffer.
	avc1 := make([]byte, len(p.avc1Buf))
	copy(avc1, p.avc1Buf)

	// Extract SPS/PPS from keyframes for relay VideoInfo.
	if encKF {
		for _, nalu := range codec.ExtractNALUs(avc1) {
			if len(nalu) == 0 {
				continue
			}
			switch nalu[0] & 0x1F {
			case 7: // SPS
				sps = append([]byte(nil), nalu...)
			case 8: // PPS
				pps = append([]byte(nil), nalu...)
			}
		}
	}

	return avc1, encKF, sps, pps
}

// estimateFPSFromClipFrames estimates FPS from buffered frame PTS values.
// Frames may be in decode order (not PTS order) due to B-frames, so we
// find the actual min/max PTS rather than assuming first/last ordering.
func estimateFPSFromClipFrames(clip []bufferedFrame) float64 {
	if len(clip) < 2 {
		return 30.0
	}
	minPTS, maxPTS := clip[0].pts, clip[0].pts
	for _, f := range clip[1:] {
		if f.pts < minPTS {
			minPTS = f.pts
		}
		if f.pts > maxPTS {
			maxPTS = f.pts
		}
	}
	ptsSpan := maxPTS - minPTS
	if ptsSpan <= 0 {
		return 30.0
	}
	fps := float64(len(clip)-1) * 90000.0 / float64(ptsSpan)
	if fps < 10 {
		fps = 10
	}
	if fps > 120 {
		fps = 120
	}
	return fps
}

// computeClipTiming computes frame duplication count and PTS increment per
// output frame. For speeds > 1.0, dupCount is 1 (pacing handles speed).
// For speeds <= 1.0, frames are duplicated.
func computeClipTiming(speed, sourceFPS float64) (dupCount int, ptsPerFrame int64) {
	if speed >= 1.0 {
		dupCount = 1
	} else {
		dupCount = int(math.Round(1.0 / speed))
		if dupCount < 1 {
			dupCount = 1
		}
	}
	ptsPerFrame = int64(90000.0 / (sourceFPS * float64(dupCount)))
	return
}

// computeFrameDuration computes the wall-clock duration between output frames.
func computeFrameDuration(speed, sourceFPS float64, dupCount int) time.Duration {
	return time.Duration(float64(time.Second) / (sourceFPS * speed * float64(dupCount)))
}
