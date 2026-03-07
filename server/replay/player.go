package replay

import (
	"context"
	"log/slog"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/prism/media"
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
	OnReady        func() // Called when first GOP decoded and encoder created.
	OnVideoInfo    func(sps, pps []byte, width, height int) // Called once on first encoded keyframe.
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
	audioIdx       int
	outputAudioPTS int64 // Separate monotonic PTS for audio frames.

	// Absolute-time pacing: playbackStart anchors the output timeline so
	// frame N's deadline is playbackStart + N*frameDuration, preventing
	// per-frame drift from encode overhead accumulation.
	playbackStart time.Time
}

// newReplayPlayer creates a player for the given clip and configuration.
func newReplayPlayer(cfg PlayerConfig) *replayPlayer {
	return &replayPlayer{
		config: cfg,
		done:   make(chan struct{}),
	}
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

func (p *replayPlayer) run(ctx context.Context) {
	defer close(p.done)
	defer p.config.OnDone()

	clip := p.config.Clip
	if len(clip) == 0 {
		return
	}

	// Split clip into GOPs for batch decoding.
	gops := splitIntoGOPs(clip)
	if len(gops) == 0 {
		return
	}

	// Pre-decode ALL GOPs upfront to eliminate inline decode delays
	// that cause jitter at GOP boundaries during playback.
	var allDecoded [][]decodedFrame
	for gopIdx, gop := range gops {
		decoded, err := decodeGOP(gop, p.config.DecoderFactory)
		if err != nil {
			slog.Error("replay player: decode GOP failed", "gop", gopIdx, "err", err)
			return
		}
		// Sort within GOP for B-frame display order.
		sort.Slice(decoded, func(i, j int) bool {
			return decoded[i].pts < decoded[j].pts
		})
		allDecoded = append(allDecoded, decoded)
	}
	if len(allDecoded) == 0 || len(allDecoded[0]) == 0 {
		return
	}

	// Estimate source FPS from all clip frames' PTS values.
	sourceFPS := estimateFPSFromClip(clip)
	ptsPerFrame := int64(90000 / sourceFPS)

	// Create encoder from first decoded frame dimensions.
	w, h := allDecoded[0][0].width, allDecoded[0][0].height
	bitrate := estimateBitrate(w, h)
	encoder, err := p.config.EncoderFactory(w, h, bitrate, float32(sourceFPS))
	if err != nil {
		slog.Error("replay player: encoder creation failed", "err", err)
		return
	}
	defer encoder.Close()

	// Signal that decoding is ready and playback is about to begin.
	if p.config.OnReady != nil {
		p.config.OnReady()
	}

	// Count total frames for progress tracking.
	totalClipFrames := len(clip)
	dupCount := int(math.Ceil(1.0 / p.config.Speed))
	totalFrames := totalClipFrames * dupCount
	frameDuration := time.Duration(float64(time.Second) / sourceFPS)

	// Create timer once for frame pacing. Immediately Stop+drain because
	// NewTimer fires immediately on creation, and we need a clean state
	// for the first Reset() call in the output loop.
	timer := time.NewTimer(frameDuration)
	defer timer.Stop()
	if !timer.Stop() {
		<-timer.C
	}

	codecStr := "avc1.42C01E" // Fallback; overwritten on first keyframe from encoder SPS.
	var groupID uint32        // MoQ group ID — incremented on each keyframe.

	interpolator := newInterpolator(p.config.Interpolation)

	outputPTS := p.config.InitialPTS
	p.outputAudioPTS = p.config.InitialPTS
	p.playbackStart = time.Now()
	var pacingIdx int // Monotonic frame counter for absolute-time pacing (never resets).
	for {
		firstFrame := true
		outputIdx := 0
		p.audioIdx = 0

		for _, decoded := range allDecoded {
			if p.outputGOP(ctx, decoded, encoder, dupCount, ptsPerFrame, frameDuration, timer, &outputPTS, &firstFrame, &outputIdx, totalFrames, &codecStr, &groupID, interpolator, &pacingIdx) {
				return
			}
		}

		if !p.config.Loop {
			return
		}
	}
}

// outputGOP encodes and outputs decoded frames with pacing and slow-motion
// duplication or blending. Returns true if context was cancelled and caller should return.
func (p *replayPlayer) outputGOP(
	ctx context.Context,
	decoded []decodedFrame,
	encoder transition.VideoEncoder,
	dupCount int,
	ptsPerFrame int64,
	frameDuration time.Duration,
	timer *time.Timer,
	outputPTS *int64,
	firstFrame *bool,
	outputIdx *int,
	totalFrames int,
	codecStr *string,
	groupID *uint32,
	interpolator FrameInterpolator,
	pacingIdx *int,
) bool {
	for di, df := range decoded {
		for dup := 0; dup < dupCount; dup++ {
			select {
			case <-ctx.Done():
				return true
			default:
			}

			// Force IDR on: (1) very first frame of playback,
			// (2) first frame of each GOP (di==0, dup==0) for MoQ group boundaries.
			forceIDR := *firstFrame || (di == 0 && dup == 0)
			*firstFrame = false

			// Determine which YUV data to encode. When an interpolator is
			// available, dupCount > 1, and this is not the first copy (dup 0),
			// blend between the current frame and the next frame.
			yuvToEncode := df.yuv
			if interpolator != nil && dupCount > 1 && dup > 0 {
				nextIdx := di + 1
				if nextIdx < len(decoded) {
					next := decoded[nextIdx]
					// Only blend if dimensions match.
					if next.width == df.width && next.height == df.height {
						alpha := float64(dup) / float64(dupCount)
						yuvToEncode = interpolator.Interpolate(df.yuv, next.yuv, df.width, df.height, alpha)
					}
				}
				// If no next frame or dimension mismatch, fall back to duplication (yuvToEncode stays as df.yuv).
			}

			encoded, isKeyframe, encErr := encoder.Encode(yuvToEncode, forceIDR)
			if encErr != nil {
				slog.Error("replay player: encode failed", "err", encErr)
				return true
			}
			// Multi-threaded encoders may return nil during pipeline warmup (EAGAIN).
			if encoded == nil {
				continue
			}

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
						*codecStr = codec.ParseSPSCodecString(nalu)
					case 8:
						ppsNALU = nalu
					}
				}
				if !p.videoInfoSent && p.config.OnVideoInfo != nil && spsNALU != nil && ppsNALU != nil {
					p.videoInfoSent = true
					p.config.OnVideoInfo(spsNALU, ppsNALU, df.width, df.height)
				}
			}

			if isKeyframe {
				*groupID++
			}

			frame := &media.VideoFrame{
				PTS:        *outputPTS,
				IsKeyframe: isKeyframe,
				WireData:   avc1,
				Codec:      *codecStr,
				GroupID:    *groupID,
				SPS:        spsNALU,
				PPS:        ppsNALU,
			}

			p.config.Output(frame)

			// Emit audio frames whose source PTS falls within this video
			// frame's source PTS range. Uses source PTS (not wall time)
			// to correctly handle B-frame reordering.
			var nextSourcePTS int64
			if di+1 < len(decoded) {
				nextSourcePTS = decoded[di+1].pts
			} else {
				nextSourcePTS = df.pts + ptsPerFrame
			}
			p.emitAudioForFrame(df.pts, nextSourcePTS, dup)

			*outputPTS += ptsPerFrame
			*outputIdx++
			*pacingIdx++

			if totalFrames > 0 {
				p.progress.Store(int64(*outputIdx * 1000 / totalFrames))
			}

			// Pace output using absolute time deadlines to prevent
			// per-frame drift from encode overhead accumulation.
			// pacingIdx never resets across loops, so deadlines are
			// always in the future relative to playbackStart.
			deadline := p.playbackStart.Add(time.Duration(*pacingIdx) * frameDuration)
			wait := time.Until(deadline)
			if wait > 0 {
				timer.Reset(wait)
				select {
				case <-ctx.Done():
					return true
				case <-timer.C:
				}
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

	// Only emit audio on the first duplicate of each source frame to
	// avoid repeating audio frames during slow-motion duplication.
	if dup != 0 {
		return
	}

	// Emit all audio frames whose source PTS falls within [sourcePTS, nextSourcePTS).
	for p.audioIdx < len(audioClip) {
		af := &audioClip[p.audioIdx]
		if af.pts < sourcePTS {
			// Audio frame precedes this video frame — skip it.
			p.audioIdx++
			continue
		}
		if af.pts >= nextSourcePTS {
			// Audio frame is at or after the next video frame — stop.
			break
		}

		// Each audio frame gets its own monotonically advancing PTS.
		// AAC frames are 1024 samples; PTS increment = 1024 * 90000 / sampleRate.
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

// decodeGOP decodes a single GOP and returns decoded YUV frames. A fresh
// decoder is created per GOP to avoid cross-GOP state artifacts.
func decodeGOP(gop []bufferedFrame, factory transition.DecoderFactory) ([]decodedFrame, error) {
	decoder, err := factory()
	if err != nil {
		return nil, err
	}
	defer decoder.Close()

	var decoded []decodedFrame
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
			slog.Warn("replay player: decode frame failed", "pts", bf.pts, "err", decErr)
			continue
		}

		// Deep-copy YUV (decoder may reuse its buffer).
		yuvCopy := make([]byte, len(yuv))
		copy(yuvCopy, yuv)

		decoded = append(decoded, decodedFrame{
			yuv:    yuvCopy,
			width:  w,
			height: h,
			pts:    bf.pts,
		})
	}

	return decoded, nil
}

// estimateFPSFromClip estimates the source FPS from buffered frame PTS values.
func estimateFPSFromClip(clip []bufferedFrame) float64 {
	if len(clip) < 2 {
		return 30.0
	}
	ptsSpan := clip[len(clip)-1].pts - clip[0].pts
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
		return 8_000_000
	case pixels >= 1280*720:
		return 4_000_000
	default:
		return 2_000_000
	}
}
