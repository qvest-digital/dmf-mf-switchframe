package replay

import (
	"cmp"
	"context"
	"log/slog"
	"math"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/transition"
)

// PlayerConfig configures a replay player instance.
type PlayerConfig struct {
	Clip           []bufferedFrame
	AudioClip      []bufferedAudioFrame
	Speed          float64
	Loop           bool
	InitialPTS     int64 // Starting PTS for output (anchors to program timeline).
	Interpolation  InterpolationMode
	DecoderFactory transition.DecoderFactory
	EncoderFactory transition.EncoderFactory
	Output         func(frame *media.VideoFrame)
	AudioOutput    func(frame *media.AudioFrame)
	OnDone         func()
	OnReady        func()                                   // Called when first GOP decoded and encoder created.
	OnVideoInfo    func(sps, pps []byte, width, height int) // Called once on first encoded keyframe.

	// RawVideoOutput sends decoded YUV directly to the switcher pipeline.
	// Called for every output frame (including slow-mo duplicates/interpolations).
	RawVideoOutput func(yuv []byte, w, h int, pts int64)

	// RawMonitorOutput sends raw YUV to a monitoring relay (e.g. "replay-raw").
	// Optional — only set when raw program monitor is enabled.
	RawMonitorOutput func(yuv []byte, w, h int, pts int64)

	// AudioDecoderFactory creates an AAC decoder for WSOLA pre-processing.
	// Required when Speed < 1.0 for pitch-preserved slow-motion audio.
	AudioDecoderFactory audio.DecoderFactory

	// AudioEncoderFactory creates an AAC encoder for WSOLA post-processing.
	AudioEncoderFactory audio.EncoderFactory
}

// decodedFrame is a decoded YUV frame with original PTS for display ordering.
type decodedFrame struct {
	yuv    []byte
	width  int
	height int
	pts    int64 // Original source PTS for display-order sorting.
}

// replayPlayer decodes a clip, optionally duplicates frames for slow-motion,
// re-encodes, and outputs to a relay. Created per-Play, destroyed on complete.
type replayPlayer struct {
	config        PlayerConfig
	cancel        context.CancelFunc
	done          chan struct{}
	once          sync.Once
	progress      atomic.Int64 // 0–1000 representing 0.0–1.0 playback progress
	videoInfoSent bool         // true after OnVideoInfo callback has been called

	// Audio tracking: index into AudioClip for interleaved output.
	audioIdx          int
	outputAudioPTS    int64 // Separate monotonic PTS for audio frames.
	audioPreStretched bool  // true when audio has been WSOLA-stretched
	totalOutputFrames int   // total output video frames (for proportional audio distribution)
	audioCallCount    int   // counts emitAudioForFrame calls (for proportional distribution)

	// Absolute-time pacing: playbackStart anchors the output timeline so
	// frame N's deadline is playbackStart + N*frameDuration, preventing
	// per-frame drift from encode overhead accumulation.
	playbackStart time.Time

	// Pause mechanism (mirrors clip/player.go pattern).
	pauseCh atomic.Pointer[chan struct{}]
	pauseMu sync.Mutex
	state   atomic.Value // PlayerState

	// Seek: send desired progress (0.0-1.0) via seekCh.
	seekCh chan float64

	// Speed changes mid-playback.
	speed atomic.Value // float64

	// Stored for speed-change recalculation.
	sourceFPS       float64
	totalClipFrames int
}

// newReplayPlayer creates a player for the given clip and configuration.
func newReplayPlayer(cfg PlayerConfig) *replayPlayer {
	p := &replayPlayer{
		config: cfg,
		done:   make(chan struct{}),
		seekCh: make(chan float64, 1),
	}
	p.speed.Store(cfg.Speed)

	// Initialize pause channel as closed (not paused).
	ch := make(chan struct{})
	close(ch)
	p.pauseCh.Store(&ch)

	p.state.Store(PlayerPlaying)
	return p
}

// Start begins playback in a background goroutine.
func (p *replayPlayer) Start(ctx context.Context) {
	ctx, p.cancel = context.WithCancel(ctx)
	go p.run(ctx)
}

// Stop cancels playback.
func (p *replayPlayer) Stop() {
	p.once.Do(func() {
		if p.cancel != nil {
			p.cancel()
		}
	})
}

// Wait blocks until the player finishes.
func (p *replayPlayer) Wait() {
	<-p.done
}

// Progress returns the current playback progress as a float64 between 0.0 and 1.0.
func (p *replayPlayer) Progress() float64 {
	return float64(p.progress.Load()) / 1000.0
}

// State returns the current player state.
func (p *replayPlayer) State() PlayerState {
	v := p.state.Load()
	if v == nil {
		return PlayerIdle
	}
	return v.(PlayerState)
}

// Speed returns the current playback speed.
func (p *replayPlayer) Speed() float64 {
	v := p.speed.Load()
	if v == nil {
		return 1.0
	}
	return v.(float64)
}

// Pause pauses playback, holding the current frame.
func (p *replayPlayer) Pause() {
	p.pauseMu.Lock()
	defer p.pauseMu.Unlock()
	if p.State() != PlayerPlaying {
		return
	}
	ch := make(chan struct{})
	p.pauseCh.Store(&ch)
	p.state.Store(PlayerPaused)
}

// Resume resumes playback from a paused state.
func (p *replayPlayer) Resume() {
	p.pauseMu.Lock()
	defer p.pauseMu.Unlock()
	if p.State() != PlayerPaused {
		return
	}
	p.state.Store(PlayerPlaying)
	chPtr := p.pauseCh.Load()
	if chPtr != nil {
		select {
		case <-*chPtr:
		default:
			close(*chPtr)
		}
	}
}

// SetSpeed changes the playback speed. Clamped to 0.25-1.0.
func (p *replayPlayer) SetSpeed(speed float64) {
	if speed < 0.25 {
		speed = 0.25
	}
	if speed > 1.0 {
		speed = 1.0
	}
	p.speed.Store(speed)
}

// Seek seeks to the given position (0.0-1.0) within the clip.
func (p *replayPlayer) Seek(position float64) {
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

// gopLookahead is the number of GOPs to keep decoded ahead of the current
// playback position. A small window (2 GOPs) eliminates jitter at GOP
// boundaries while keeping memory usage proportional to GOP size rather
// than total clip length.
const gopLookahead = 2

// gopOutputState holds the mutable state threaded through outputGOP calls,
// grouping per-GOP parameters into a single struct for readability.
type gopOutputState struct {
	encoder       transition.VideoEncoder
	interpolator  FrameInterpolator
	timer         *time.Timer
	dupCount      int
	ptsPerFrame   int64
	frameDuration time.Duration
	totalFrames   int
	outputPTS     int64
	firstFrame    bool
	outputIdx     int
	codecStr      string
	groupID       uint32
	pacingIdx     int
}

func (p *replayPlayer) run(ctx context.Context) {
	defer close(p.done)
	defer p.config.OnDone()

	clip := p.config.Clip
	if len(clip) == 0 {
		return
	}

	// Split clip into GOPs for streaming decode.
	gops := splitIntoGOPs(clip)
	if len(gops) == 0 {
		return
	}

	// Pre-decode a small window of GOPs (up to gopLookahead) to determine
	// frame dimensions and eliminate jitter at the first GOP boundary.
	// Remaining GOPs are decoded on-demand during playback.
	prefetchCount := gopLookahead
	if prefetchCount > len(gops) {
		prefetchCount = len(gops)
	}
	decodedWindow := make([][]decodedFrame, prefetchCount)
	for i := 0; i < prefetchCount; i++ {
		decoded, err := decodeGOP(gops[i], p.config.DecoderFactory)
		if err != nil {
			slog.Error("replay player: decode GOP failed", "gop", i, "err", err)
			return
		}
		slices.SortFunc(decoded, func(a, b decodedFrame) int {
			return cmp.Compare(a.pts, b.pts)
		})
		decodedWindow[i] = decoded
	}
	if len(decodedWindow) == 0 || len(decodedWindow[0]) == 0 {
		return
	}

	// Signal that decoding is ready and playback is about to begin.
	if p.config.OnReady != nil {
		p.config.OnReady()
	}
	p.state.Store(PlayerPlaying)

	// Pre-stretch audio for slow-motion if needed.
	p.preStretchAudio()

	// Estimate source FPS from all clip frames' PTS values.
	sourceFPS := estimateFPSFromClip(clip)
	fpsNum, fpsDen := fpsToRational(sourceFPS)
	ptsPerFrame := int64(90000) * int64(fpsDen) / int64(fpsNum)

	// Create encoder (optional — only needed when H.264 Output callback is set).
	w, h := decodedWindow[0][0].width, decodedWindow[0][0].height
	var encoder transition.VideoEncoder
	if p.config.EncoderFactory != nil && p.config.Output != nil {
		bitrate := estimateBitrate(w, h)
		var err error
		encoder, err = p.config.EncoderFactory(w, h, bitrate, fpsNum, fpsDen)
		if err != nil {
			slog.Error("replay player: encoder creation failed", "err", err)
			return
		}
		defer encoder.Close()
	}

	// Count total frames for progress tracking.
	totalClipFrames := len(clip)
	p.sourceFPS = sourceFPS
	p.totalClipFrames = totalClipFrames
	dupCount, frameDuration := computeReplayTiming(p.config.Speed, sourceFPS, totalClipFrames)
	totalFrames := totalClipFrames * dupCount

	// Set total output frames for proportional audio distribution.
	// Both WSOLA-stretched and non-stretched paths use proportional
	// distribution to avoid "pop pop pop" audio gaps.
	p.totalOutputFrames = totalFrames

	// Create timer once for frame pacing. Immediately Stop+drain because
	// NewTimer fires immediately on creation, and we need a clean state
	// for the first Reset() call in the output loop.
	timer := time.NewTimer(frameDuration)
	defer timer.Stop()
	if !timer.Stop() {
		<-timer.C
	}

	// Initialize mutable GOP output state, threaded through outputGOP calls.
	gs := &gopOutputState{
		encoder:       encoder,
		interpolator:  newInterpolator(p.config.Interpolation),
		timer:         timer,
		dupCount:      dupCount,
		ptsPerFrame:   ptsPerFrame,
		frameDuration: frameDuration,
		totalFrames:   totalFrames,
		outputPTS:     p.config.InitialPTS,
		firstFrame:    true,
		codecStr:      "avc1.42C01E", // Fallback; overwritten on first keyframe from encoder SPS.
	}

	p.outputAudioPTS = p.config.InitialPTS
	p.playbackStart = time.Now()
	for {
		p.outputAudioPTS = gs.outputPTS // resync audio PTS to video PTS at loop boundary
		gs.firstFrame = true
		gs.outputIdx = 0
		p.audioIdx = 0
		p.audioCallCount = 0

		// nextDecoded tracks how many GOPs have been decoded (across all
		// loop iterations). For the first pass this starts at prefetchCount;
		// on subsequent loop iterations it resets along with decodedWindow.
		nextDecoded := prefetchCount

		for gopIdx := 0; gopIdx < len(gops); gopIdx++ {
			// Ensure decodedWindow[0] is the current GOP's decoded frames.
			// windowIdx maps gopIdx into the sliding window.
			windowIdx := gopIdx - (nextDecoded - len(decodedWindow))
			if windowIdx < 0 || windowIdx >= len(decodedWindow) {
				// Should not happen — decode ahead should keep us covered.
				slog.Error("replay player: GOP window miss", "gopIdx", gopIdx, "windowIdx", windowIdx)
				return
			}

			decoded := decodedWindow[windowIdx]

			// Decode ahead: ensure the next GOP is ready if not already decoded.
			if nextDecoded < len(gops) && windowIdx+1 >= len(decodedWindow) {
				ahead, err := decodeGOP(gops[nextDecoded], p.config.DecoderFactory)
				if err != nil {
					slog.Error("replay player: decode GOP failed", "gop", nextDecoded, "err", err)
					return
				}
				slices.SortFunc(ahead, func(a, b decodedFrame) int {
					return cmp.Compare(a.pts, b.pts)
				})
				decodedWindow = append(decodedWindow, ahead)
				nextDecoded++
			}

			// Check for seek requests.
			select {
			case pos := <-p.seekCh:
				targetGOP := int(pos * float64(len(gops)))
				if targetGOP >= len(gops) {
					targetGOP = len(gops) - 1
				}
				if targetGOP < 0 {
					targetGOP = 0
				}
				// Reset output state for the new position.
				gs.outputIdx = 0
				for i := 0; i < targetGOP; i++ {
					gs.outputIdx += len(gops[i]) * gs.dupCount
				}
				gs.firstFrame = true
				p.playbackStart = time.Now()
				gs.pacingIdx = 0
				// Re-decode the window starting at the target GOP.
				decodedWindow = decodedWindow[:0]
				endGOP := targetGOP + prefetchCount
				if endGOP > len(gops) {
					endGOP = len(gops)
				}
				for i := targetGOP; i < endGOP; i++ {
					dec, err := decodeGOP(gops[i], p.config.DecoderFactory)
					if err != nil {
						slog.Error("replay player: decode GOP on seek failed", "gop", i, "err", err)
						return
					}
					slices.SortFunc(dec, func(a, b decodedFrame) int {
						return cmp.Compare(a.pts, b.pts)
					})
					decodedWindow = append(decodedWindow, dec)
				}
				nextDecoded = endGOP
				// Reset audio index proportionally.
				if len(p.config.AudioClip) > 0 {
					p.audioIdx = gs.outputIdx * len(p.config.AudioClip) / gs.totalFrames
					p.audioCallCount = gs.outputIdx
				}
				gopIdx = targetGOP - 1 // -1 because loop increments
				continue
			default:
			}

			// Output the current GOP.
			if p.outputGOP(ctx, decoded, gs) {
				return
			}

			// Release consumed GOP frames to free memory. Slide the window
			// forward by dropping the front entry now that it's been played.
			decodedWindow[windowIdx] = nil // allow GC
			if windowIdx == 0 && len(decodedWindow) > 1 {
				decodedWindow = decodedWindow[1:]
			}
		}

		if !p.config.Loop {
			return
		}

		// On loop restart, re-decode the initial window from the beginning.
		decodedWindow = decodedWindow[:0]
		for i := 0; i < prefetchCount; i++ {
			decoded, err := decodeGOP(gops[i], p.config.DecoderFactory)
			if err != nil {
				slog.Error("replay player: decode GOP failed on loop", "gop", i, "err", err)
				return
			}
			slices.SortFunc(decoded, func(a, b decodedFrame) int {
				return cmp.Compare(a.pts, b.pts)
			})
			decodedWindow = append(decodedWindow, decoded)
		}

		// Reset pacing anchor so the new loop's deadlines are relative to
		// now, not the original playback start. Without this, pacingIdx
		// accumulates across loops and all second+ loop deadlines fall in
		// the past, causing frames to burst out without pacing.
		p.playbackStart = time.Now()
		gs.pacingIdx = 0
	}
}

// outputGOP encodes and outputs decoded frames with pacing and slow-motion
// duplication or blending. Returns true if context was cancelled and caller should return.
func (p *replayPlayer) outputGOP(
	ctx context.Context,
	decoded []decodedFrame,
	gs *gopOutputState,
) bool {
	for di, df := range decoded {
		// Check for speed changes.
		newSpeed := p.speed.Load().(float64)
		if newSpeed != p.config.Speed {
			p.config.Speed = newSpeed
			gs.dupCount, gs.frameDuration = computeReplayTiming(newSpeed, p.sourceFPS, p.totalClipFrames)
			gs.totalFrames = p.totalClipFrames * gs.dupCount
			p.totalOutputFrames = gs.totalFrames
			p.playbackStart = time.Now()
			gs.pacingIdx = 0
		}

		for dup := 0; dup < gs.dupCount; dup++ {
			select {
			case <-ctx.Done():
				return true
			default:
			}

			// Check for pause. If paused, block until resumed or cancelled.
			chPtr := p.pauseCh.Load()
			if chPtr != nil {
				select {
				case <-*chPtr:
					// Not paused or resumed — continue.
				case <-ctx.Done():
					return true
				}
			}

			// If deadline is far in the past (e.g. after pause/resume), reset pacing.
			deadline := p.playbackStart.Add(time.Duration(gs.pacingIdx) * gs.frameDuration)
			if time.Until(deadline) < -gs.frameDuration*2 {
				p.playbackStart = time.Now()
				gs.pacingIdx = 0
			}

			// Force IDR only on the very first frame of playback.
			// Subsequent keyframes come from the encoder's natural GOP
			// interval. Forcing IDR at every source GOP boundary created
			// excessive MoQ groups (separate QUIC streams), causing
			// inter-stream frame reordering in the browser.
			forceIDR := gs.firstFrame
			gs.firstFrame = false

			// Determine which YUV data to encode. When an interpolator is
			// available, dupCount > 1, and this is not the first copy (dup 0),
			// blend between the current frame and the next frame.
			yuvToEncode := df.yuv
			if gs.interpolator != nil && gs.dupCount > 1 && dup > 0 {
				nextIdx := di + 1
				if nextIdx < len(decoded) {
					next := decoded[nextIdx]
					// Only blend if dimensions match.
					if next.width == df.width && next.height == df.height {
						alpha := float64(dup) / float64(gs.dupCount)
						yuvToEncode = gs.interpolator.Interpolate(df.yuv, next.yuv, df.width, df.height, alpha)
					}
				}
				// If no next frame or dimension mismatch, fall back to duplication (yuvToEncode stays as df.yuv).
			}

			// Primary output: raw YUV to switcher pipeline.
			if p.config.RawVideoOutput != nil {
				p.config.RawVideoOutput(yuvToEncode, df.width, df.height, gs.outputPTS)
			}

			// Raw monitoring output (e.g. "replay-raw" relay).
			if p.config.RawMonitorOutput != nil {
				p.config.RawMonitorOutput(yuvToEncode, df.width, df.height, gs.outputPTS)
			}

			// Pace BEFORE output: wait until the deadline, then emit
			// the frame right at the target time. This ensures uniform
			// output intervals regardless of variable encode durations.
			deadline = p.playbackStart.Add(time.Duration(gs.pacingIdx) * gs.frameDuration)
			wait := time.Until(deadline)
			if wait > 0 {
				gs.timer.Reset(wait)
				select {
				case <-ctx.Done():
					return true
				case <-gs.timer.C:
				}
			}

			// H.264 monitoring output (optional — only when encoder is available).
			if gs.encoder != nil {
				encoded, isKeyframe, encErr := gs.encoder.Encode(yuvToEncode, gs.outputPTS, forceIDR)
				if encErr != nil {
					slog.Error("replay player: encode failed", "err", encErr)
					return true
				}
				// Multi-threaded encoders may return nil during pipeline warmup (EAGAIN).
				if encoded != nil {
					// Convert Annex B encoder output to AVC1 for relay.
					avc1 := codec.AnnexBToAVC1(encoded)
					if len(avc1) == 0 {
						avc1 = encoded // Fallback if already AVC1
					}

					// Derive codec string from encoder's SPS on keyframes,
					// and fire OnVideoInfo callback once with SPS/PPS.
					var spsNALU, ppsNALU []byte
					if isKeyframe {
						for _, nalu := range codec.ExtractNALUs(avc1) {
							if len(nalu) == 0 {
								continue
							}
							switch nalu[0] & 0x1F {
							case 7:
								spsNALU = nalu
								gs.codecStr = codec.ParseSPSCodecString(nalu)
							case 8:
								ppsNALU = nalu
							}
						}
						if !p.videoInfoSent && p.config.OnVideoInfo != nil && spsNALU != nil && ppsNALU != nil {
							p.videoInfoSent = true
							p.config.OnVideoInfo(spsNALU, ppsNALU, df.width, df.height)
						}
					}

					// Only start a new MoQ group on the very first keyframe.
					if isKeyframe && gs.groupID == 0 {
						gs.groupID++
					}

					frame := &media.VideoFrame{
						PTS:        gs.outputPTS,
						IsKeyframe: isKeyframe,
						WireData:   avc1,
						Codec:      gs.codecStr,
						GroupID:    gs.groupID,
						SPS:        spsNALU,
						PPS:        ppsNALU,
					}
					p.config.Output(frame)
				}
			}

			// Emit audio frames whose source PTS falls within this video
			// frame's source PTS range. Uses source PTS (not wall time)
			// to correctly handle B-frame reordering.
			var nextSourcePTS int64
			if di+1 < len(decoded) {
				nextSourcePTS = decoded[di+1].pts
			} else {
				nextSourcePTS = df.pts + gs.ptsPerFrame
			}
			p.emitAudioForFrame(df.pts, nextSourcePTS, dup)

			gs.outputPTS += gs.ptsPerFrame
			gs.outputIdx++
			gs.pacingIdx++

			if gs.totalFrames > 0 {
				p.progress.Store(int64(gs.outputIdx * 1000 / gs.totalFrames))
			}
		}
	}
	return false
}

// emitAudioForFrame emits audio frames from the audio clip whose source PTS
// falls within [sourcePTS, nextSourcePTS). Uses source PTS matching instead
// of wall time to correctly handle B-frame reordering (where PTS display
// order differs from wall-time arrival order).
func (p *replayPlayer) emitAudioForFrame(sourcePTS, nextSourcePTS int64, dup int) {
	audioClip := p.config.AudioClip
	if len(audioClip) == 0 || p.config.AudioOutput == nil {
		return
	}

	if p.audioPreStretched {
		// WSOLA-stretched audio fills the full slow-mo duration. The stretched
		// clip's PTS values don't align with source video PTS (they span 1/speed
		// times the original range), so PTS matching doesn't work. Instead,
		// distribute stretched audio frames proportionally across all output
		// video frames to maintain continuous playback.
		p.audioCallCount++
		targetAudioIdx := p.audioCallCount * len(audioClip) / p.totalOutputFrames
		if targetAudioIdx > len(audioClip) {
			targetAudioIdx = len(audioClip)
		}
		for p.audioIdx < targetAudioIdx {
			af := &audioClip[p.audioIdx]
			outFrame := &media.AudioFrame{
				PTS:        p.outputAudioPTS,
				Data:       af.data,
				SampleRate: af.sampleRate,
				Channels:   af.channels,
			}
			p.config.AudioOutput(outFrame)
			if af.sampleRate > 0 {
				p.outputAudioPTS += int64(1024) * 90000 / int64(af.sampleRate)
			}
			p.audioIdx++
		}
		return
	}

	// Without WSOLA stretching, distribute original audio frames
	// proportionally across output video frames. The audio will play at
	// normal speed and end before the video (at 0.25x, audio covers only
	// 25% of playback), but while it plays it'll be continuous without pops.
	p.audioCallCount++
	targetAudioIdx := p.audioCallCount * len(audioClip) / p.totalOutputFrames
	if targetAudioIdx > len(audioClip) {
		targetAudioIdx = len(audioClip)
	}
	for p.audioIdx < targetAudioIdx {
		af := &audioClip[p.audioIdx]
		outFrame := &media.AudioFrame{
			PTS:        p.outputAudioPTS,
			Data:       af.data,
			SampleRate: af.sampleRate,
			Channels:   af.channels,
		}
		p.config.AudioOutput(outFrame)
		if af.sampleRate > 0 {
			p.outputAudioPTS += int64(1024) * 90000 / int64(af.sampleRate)
		}
		p.audioIdx++
	}
}

// preStretchAudio decodes all AAC frames, time-stretches for slow-motion
// speed (phase vocoder → WSOLA → linear fallback), re-encodes to AAC,
// and replaces the audio clip. This produces a continuous audio stream
// that fills the full slow-mo duration without gaps.
func (p *replayPlayer) preStretchAudio() {
	audioClip := p.config.AudioClip
	if len(audioClip) == 0 {
		slog.Warn("replay: audio time-stretch skipped — no audio frames in clip")
		return
	}
	if p.config.AudioDecoderFactory == nil || p.config.AudioEncoderFactory == nil {
		slog.Warn("replay: audio time-stretch skipped — audio codec factories not set")
		return
	}
	if p.config.Speed >= 1.0 {
		return
	}

	sampleRate := audioClip[0].sampleRate
	channels := audioClip[0].channels
	if sampleRate == 0 || channels == 0 {
		slog.Warn("replay: audio time-stretch skipped — invalid sampleRate/channels",
			"sampleRate", sampleRate, "channels", channels)
		return
	}

	slog.Info("replay: audio time-stretch starting",
		"audio_frames", len(audioClip),
		"sampleRate", sampleRate,
		"channels", channels,
		"speed", p.config.Speed)

	// Decode all AAC frames to PCM.
	dec, err := p.config.AudioDecoderFactory(sampleRate, channels)
	if err != nil {
		slog.Error("replay: audio decoder creation failed", "err", err)
		return
	}
	defer func() { _ = dec.Close() }()

	var allPCM []float32
	var decodeErrors int
	for _, af := range audioClip {
		// Ensure ADTS framing — source streams carry raw AAC AUs
		// (without ADTS headers) in MPEG-TS PES packets. The FDK
		// decoder requires ADTS sync headers.
		adtsFrame := codec.EnsureADTS(af.data, af.sampleRate, af.channels)
		pcm, err := dec.Decode(adtsFrame)
		if err != nil {
			decodeErrors++
			continue
		}
		allPCM = append(allPCM, pcm...)
	}
	if len(allPCM) == 0 {
		slog.Warn("replay: audio time-stretch failed — all audio frames failed to decode",
			"total_frames", len(audioClip), "decode_errors", decodeErrors)
		return
	}
	if decodeErrors > 0 {
		slog.Warn("replay: audio decoded with errors",
			"decoded_samples", len(allPCM), "decode_errors", decodeErrors)
	}

	// Time-stretch: try phase vocoder first (best quality), fall back to
	// WSOLA, then linear interpolation.
	stretched := PhaseVocoderTimeStretch(allPCM, channels, sampleRate, p.config.Speed)
	if len(stretched) == 0 {
		slog.Warn("replay: phase vocoder produced empty output, falling back to WSOLA")
		stretched = WSOLATimeStretch(allPCM, channels, sampleRate, p.config.Speed)
	}
	if len(stretched) == 0 {
		slog.Warn("replay: WSOLA produced empty output, falling back to linear stretch")
		stretched = linearTimeStretch(allPCM, channels, p.config.Speed)
	}
	if len(stretched) == 0 {
		slog.Warn("replay: all time-stretch methods failed")
		return
	}

	slog.Info("replay: audio time-stretch complete",
		"input_samples", len(allPCM),
		"output_samples", len(stretched),
		"ratio", float64(len(stretched))/float64(len(allPCM)))

	// Re-encode: segment into 1024-sample AAC frames.
	enc, err := p.config.AudioEncoderFactory(sampleRate, channels)
	if err != nil {
		slog.Error("replay: audio encoder creation failed", "err", err)
		return
	}
	defer func() { _ = enc.Close() }()

	samplesPerFrame := 1024 * channels
	// Use the first audio frame's PTS as base for stretched audio PTS.
	basePTS := audioClip[0].pts
	ptsPerFrame := int64(1024) * 90000 / int64(sampleRate)

	var newClip []bufferedAudioFrame
	var encodeErrors int
	for i := 0; i+samplesPerFrame <= len(stretched); i += samplesPerFrame {
		chunk := stretched[i : i+samplesPerFrame]
		encoded, err := enc.Encode(chunk)
		if err != nil {
			encodeErrors++
			continue
		}
		if len(encoded) == 0 {
			continue // Encoder priming frame
		}
		frameIdx := len(newClip)
		newClip = append(newClip, bufferedAudioFrame{
			data:       encoded,
			pts:        basePTS + int64(frameIdx)*ptsPerFrame,
			sampleRate: sampleRate,
			channels:   channels,
			wallTime:   audioClip[0].wallTime, // approximate
		})
	}

	if len(newClip) > 0 {
		p.config.AudioClip = newClip
		p.audioPreStretched = true
		slog.Info("replay: audio pre-stretched",
			"original_frames", len(audioClip),
			"stretched_frames", len(newClip),
			"encode_errors", encodeErrors,
			"speed", p.config.Speed)
	} else {
		slog.Warn("replay: audio re-encode produced no frames",
			"stretched_samples", len(stretched), "encode_errors", encodeErrors)
	}
}

// splitIntoGOPs splits a clip into groups of pictures, where each group starts
// with a keyframe. Frames before the first keyframe are dropped.
func splitIntoGOPs(clip []bufferedFrame) [][]bufferedFrame {
	var gops [][]bufferedFrame
	for _, f := range clip {
		if f.isKeyframe {
			gops = append(gops, []bufferedFrame{f})
		} else if len(gops) > 0 {
			gops[len(gops)-1] = append(gops[len(gops)-1], f)
		}
	}
	return gops
}

// frameDrainer is implemented by decoders that buffer frames internally
// (e.g., for B-frame reordering) and need explicit draining.
type frameDrainer interface {
	SendEOS() error
	ReceiveFrame() ([]byte, int, int, error)
}

// decodeGOP decodes a single GOP and returns decoded YUV frames. A fresh
// decoder is created per GOP to avoid cross-GOP state artifacts.
//
// The FFmpeg decoder buffers frames for B-frame reordering, so:
//   - EAGAIN ("buffering") is expected and not an error
//   - After feeding all input, we drain remaining buffered frames via SendEOS/ReceiveFrame
//   - The decoder outputs in display order; we assign sorted source PTS
func decodeGOP(gop []bufferedFrame, factory transition.DecoderFactory) ([]decodedFrame, error) {
	decoder, err := factory()
	if err != nil {
		return nil, err
	}
	defer decoder.Close()

	// Collect source PTS sorted into display order for assignment to
	// decoded frames (decoder outputs in display order, not decode order).
	sortedPTS := make([]int64, len(gop))
	for i, bf := range gop {
		sortedPTS[i] = bf.pts
	}
	slices.Sort(sortedPTS)

	var decoded []decodedFrame
	collectFrame := func(yuv []byte, w, h int) {
		yuvCopy := make([]byte, len(yuv))
		copy(yuvCopy, yuv)
		// Assign PTS from sorted source PTS (display order).
		pts := int64(0)
		if len(decoded) < len(sortedPTS) {
			pts = sortedPTS[len(decoded)]
		}
		decoded = append(decoded, decodedFrame{
			yuv:    yuvCopy,
			width:  w,
			height: h,
			pts:    pts,
		})
	}

	// Check if this decoder supports draining (FFmpeg does, mocks don't).
	drainer, canDrain := decoder.(frameDrainer)

	for _, bf := range gop {
		// Convert AVC1 to Annex B for decoder, prepending SPS/PPS for keyframes.
		annexB := codec.AVC1ToAnnexB(bf.wireData)
		if len(annexB) == 0 {
			continue
		}

		if bf.isKeyframe {
			annexB = codec.PrependSPSPPS(bf.sps, bf.pps, annexB)
		}

		yuv, w, h, decErr := decoder.Decode(annexB)
		if decErr != nil {
			// EAGAIN ("buffering") is expected for B-frame reordering.
			// The frame is consumed by the decoder and will be output later.
			if !strings.Contains(decErr.Error(), "buffering") {
				slog.Warn("replay player: decode frame failed", "pts", bf.pts, "err", decErr)
				// Non-EAGAIN error: frame is dropped, not buffered.
				// Remove its PTS from sortedPTS so remaining decoded
				// frames get correct PTS assignments.
				if idx, found := slices.BinarySearch(sortedPTS, bf.pts); found {
					sortedPTS = slices.Delete(sortedPTS, idx, idx+1)
				}
			}
			continue
		}
		collectFrame(yuv, w, h)

		// Try to receive additional frames — the decoder may have
		// multiple frames ready after resolving B-frame dependencies.
		if canDrain {
			for {
				yuv2, w2, h2, err2 := drainer.ReceiveFrame()
				if err2 != nil {
					break
				}
				collectFrame(yuv2, w2, h2)
			}
		}
	}

	// Drain remaining buffered frames (B-frame reordering tail).
	if canDrain {
		if err := drainer.SendEOS(); err == nil {
			for {
				yuv, w, h, err := drainer.ReceiveFrame()
				if err != nil {
					break
				}
				collectFrame(yuv, w, h)
			}
		}
	}

	return decoded, nil
}

// estimateFPSFromClip estimates the source FPS from buffered frame PTS values.
func estimateFPSFromClip(clip []bufferedFrame) float64 {
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

// estimateBitrate returns a reasonable bitrate for the given resolution.
func estimateBitrate(w, h int) int {
	pixels := w * h
	switch {
	case pixels >= 1920*1080:
		return 15_000_000
	case pixels >= 1280*720:
		return 8_000_000
	default:
		return 4_000_000
	}
}

// computeReplayTiming computes frame duplication and pacing for slow-motion replay.
// It uses math.Round (not Ceil) to avoid A/V drift at fractional speeds, and adjusts
// frameDuration so that total video time matches the audio stretch factor exactly.
func computeReplayTiming(speed, sourceFPS float64, totalClipFrames int) (dupCount int, frameDuration time.Duration) {
	dupCount = int(math.Round(1.0 / speed))
	if dupCount < 1 {
		dupCount = 1
	}
	// Adjust frame duration so total video matches audio stretch exactly:
	// totalVideoTime = totalFrames * frameDuration = originalDuration / speed
	// frameDuration = originalDuration / (speed * totalFrames)
	//              = (totalClipFrames / sourceFPS) / (speed * totalClipFrames * dupCount)
	//              = 1.0 / (sourceFPS * speed * dupCount)
	frameDuration = time.Duration(float64(time.Second) / (sourceFPS * speed * float64(dupCount)))
	return
}

// fpsToRational converts a float64 FPS to a rational fpsNum/fpsDen pair.
// Snaps to standard broadcast rates (23.976, 24, 25, 29.97, 30, 50, 59.94, 60).
func fpsToRational(fps float64) (int, int) {
	type rate struct {
		num, den int
		nominal  float64
	}
	standards := []rate{
		{24000, 1001, 23.976},
		{24, 1, 24},
		{25, 1, 25},
		{30000, 1001, 29.97},
		{30, 1, 30},
		{50, 1, 50},
		{60000, 1001, 59.94},
		{60, 1, 60},
	}
	bestNum, bestDen := 30000, 1001
	bestDist := math.Abs(fps - 29.97)
	for _, s := range standards {
		d := math.Abs(fps - s.nominal)
		if d < bestDist {
			bestDist = d
			bestNum = s.num
			bestDen = s.den
		}
	}
	return bestNum, bestDen
}
