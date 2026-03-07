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
	Speed          float64
	Loop           bool
	DecoderFactory transition.DecoderFactory
	EncoderFactory transition.EncoderFactory
	Output         func(frame *media.VideoFrame)
	OnDone         func()
	OnReady        func() // Called when first GOP decoded and encoder created.
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
	config   PlayerConfig
	cancel   context.CancelFunc
	done     chan struct{}
	once     sync.Once
	progress atomic.Int64 // 0–1000 representing 0.0–1.0 playback progress
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

	// Decode first GOP to determine dimensions and FPS.
	firstDecoded, err := decodeGOP(gops[0], p.config.DecoderFactory)
	if err != nil {
		slog.Error("replay player: decode first GOP failed", "err", err)
		return
	}
	if len(firstDecoded) == 0 {
		return
	}

	// Sort within GOP for B-frame display order.
	sort.Slice(firstDecoded, func(i, j int) bool {
		return firstDecoded[i].pts < firstDecoded[j].pts
	})

	// Estimate source FPS from all clip frames' PTS values.
	sourceFPS := estimateFPSFromClip(clip)
	ptsPerFrame := int64(90000 / sourceFPS)

	// Create encoder from first decoded frame dimensions.
	w, h := firstDecoded[0].width, firstDecoded[0].height
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

	for {
		outputPTS := int64(0)
		firstFrame := true
		outputIdx := 0

		for gopIdx, gop := range gops {
			// Decode this GOP (reuse firstDecoded for GOP 0 on first iteration).
			var decoded []decodedFrame
			if gopIdx == 0 && firstDecoded != nil {
				decoded = firstDecoded
			} else {
				var decErr error
				decoded, decErr = decodeGOP(gop, p.config.DecoderFactory)
				if decErr != nil {
					slog.Error("replay player: decode GOP failed", "gop", gopIdx, "err", decErr)
					return
				}
				// Sort within GOP for B-frame display order.
				sort.Slice(decoded, func(i, j int) bool {
					return decoded[i].pts < decoded[j].pts
				})
			}

			if p.outputGOP(ctx, decoded, encoder, dupCount, ptsPerFrame, frameDuration, timer, &outputPTS, &firstFrame, &outputIdx, totalFrames) {
				return
			}
		}
		// Clear firstDecoded after first full pass so re-decode on loop.
		firstDecoded = nil

		if !p.config.Loop {
			return
		}
	}
}

// outputGOP encodes and outputs decoded frames with pacing and slow-motion
// duplication. Returns true if context was cancelled and caller should return.
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
) bool {
	for _, df := range decoded {
		for dup := 0; dup < dupCount; dup++ {
			select {
			case <-ctx.Done():
				return true
			default:
			}

			forceIDR := *firstFrame
			*firstFrame = false

			encoded, isKeyframe, encErr := encoder.Encode(df.yuv, forceIDR)
			if encErr != nil {
				slog.Error("replay player: encode failed", "err", encErr)
				return true
			}

			// Convert Annex B encoder output to AVC1 for relay.
			avc1 := codec.AnnexBToAVC1(encoded)
			if len(avc1) == 0 {
				avc1 = encoded // Fallback if already AVC1
			}

			frame := &media.VideoFrame{
				PTS:        *outputPTS,
				IsKeyframe: isKeyframe,
				WireData:   avc1,
				Codec:      "avc1.42C01E",
			}

			p.config.Output(frame)
			*outputPTS += ptsPerFrame
			*outputIdx++

			if totalFrames > 0 {
				p.progress.Store(int64(*outputIdx * 1000 / totalFrames))
			}

			// Pace output at source FPS.
			timer.Reset(frameDuration)
			select {
			case <-ctx.Done():
				return true
			case <-timer.C:
			}
		}
	}
	return false
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
