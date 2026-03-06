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

	// Phase 1: Decode all clip frames.
	decoded, err := p.decodeClip(clip)
	if err != nil {
		slog.Error("replay player: decode failed", "err", err)
		return
	}
	if len(decoded) == 0 {
		return
	}

	// Sort by PTS for display order (B-frame extraction).
	sort.Slice(decoded, func(i, j int) bool {
		return decoded[i].pts < decoded[j].pts
	})

	// Estimate source FPS from PTS span.
	sourceFPS := estimateFPS(decoded)
	ptsPerFrame := int64(90000 / sourceFPS)

	// Phase 2: Create encoder.
	w, h := decoded[0].width, decoded[0].height
	bitrate := estimateBitrate(w, h)
	encoder, err := p.config.EncoderFactory(w, h, bitrate, float32(sourceFPS))
	if err != nil {
		slog.Error("replay player: encoder creation failed", "err", err)
		return
	}
	defer encoder.Close()

	// Phase 3: Output frames with duplication for slow-motion.
	dupCount := int(math.Ceil(1.0 / p.config.Speed))
	frameDuration := time.Duration(float64(time.Second) / sourceFPS)
	totalFrames := len(decoded) * dupCount

	// Reuse a single timer for pacing output (R1.9).
	timer := time.NewTimer(frameDuration)
	defer timer.Stop()
	// Drain the initial fire so we can Reset on first use.
	if !timer.Stop() {
		<-timer.C
	}

	for {
		outputPTS := int64(0)
		firstFrame := true
		outputIdx := 0

		for _, df := range decoded {
			for dup := 0; dup < dupCount; dup++ {
				select {
				case <-ctx.Done():
					return
				default:
				}

				forceIDR := firstFrame
				firstFrame = false

				encoded, isKeyframe, encErr := encoder.Encode(df.yuv, forceIDR)
				if encErr != nil {
					slog.Error("replay player: encode failed", "err", encErr)
					return
				}

				// Convert Annex B encoder output to AVC1 for relay.
				avc1 := codec.AnnexBToAVC1(encoded)
				if len(avc1) == 0 {
					avc1 = encoded // Fallback if already AVC1
				}

				frame := &media.VideoFrame{
					PTS:        outputPTS,
					IsKeyframe: isKeyframe,
					WireData:   avc1,
					Codec:      "avc1.42C01E",
				}

				p.config.Output(frame)
				outputPTS += ptsPerFrame
				outputIdx++

				// Update progress (R1.4).
				if totalFrames > 0 {
					p.progress.Store(int64(outputIdx * 1000 / totalFrames))
				}

				// Pace output at source FPS.
				timer.Reset(frameDuration)
				select {
				case <-ctx.Done():
					return
				case <-timer.C:
				}
			}
		}

		if !p.config.Loop {
			return
		}
	}
}

// decodeClip decodes all frames in the clip and returns decoded YUV frames.
func (p *replayPlayer) decodeClip(clip []bufferedFrame) ([]decodedFrame, error) {
	decoder, err := p.config.DecoderFactory()
	if err != nil {
		return nil, err
	}
	defer decoder.Close()

	var decoded []decodedFrame
	for _, bf := range clip {
		// Convert AVC1 to Annex B for decoder, prepending SPS/PPS for keyframes.
		annexB := codec.AVC1ToAnnexB(bf.wireData)
		if len(annexB) == 0 {
			continue
		}

		if bf.isKeyframe && len(bf.sps) > 0 {
			var buf []byte
			buf = append(buf, 0x00, 0x00, 0x00, 0x01)
			buf = append(buf, bf.sps...)
			buf = append(buf, 0x00, 0x00, 0x00, 0x01)
			buf = append(buf, bf.pps...)
			buf = append(buf, annexB...)
			annexB = buf
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

// estimateFPS estimates the source FPS from decoded frame PTS values.
func estimateFPS(frames []decodedFrame) float64 {
	if len(frames) < 2 {
		return 30.0 // Default assumption.
	}
	ptsSpan := frames[len(frames)-1].pts - frames[0].pts
	if ptsSpan <= 0 {
		return 30.0
	}
	fps := float64(len(frames)-1) * 90000.0 / float64(ptsSpan)
	// Clamp to reasonable range.
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
