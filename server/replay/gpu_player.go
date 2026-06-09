package replay

import (
	"context"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/prism/media"

	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/gpu"
	"github.com/zsiec/switchframe/server/internal/video"
)

// GPUPlayerConfig configures a GPU-accelerated replay player instance.
// The GPU player stays entirely on GPU: NVDEC decode → NVOFA optical flow →
// motion-blur CUDA kernel → NVENC encode. No CPU-side YUV touching in the
// hot path (except the optional RawVideoOutput callback which downloads to CPU).
type GPUPlayerConfig struct {
	Clip         []bufferedFrame
	AudioClip    []bufferedAudioFrame
	Speed        float64
	Loop         bool
	InitialPTS   int64
	ShutterAngle float64 // 0.0-1.0 normalized (0.5 = 180°)

	GPUCtx *gpu.Context
	FRUC   *gpu.FRUC
	Pool   *gpu.FramePool

	Output         func(frame *media.VideoFrame)
	AudioOutput    func(frame *media.AudioFrame)
	RawVideoOutput func(yuv []byte, w, h int, pts int64)
	OnDone         func()
	OnReady        func()
	OnVideoInfo    func(sps, pps []byte, width, height int)
}

// decodedGPUFrame holds a decoded frame uploaded to GPU VRAM.
type decodedGPUFrame struct {
	frame *gpu.GPUFrame
	pts   int64
}

// maxGPUClipFrames bounds the number of decoded frames the GPU player holds
// in VRAM at once. Phase 1 decodes the whole clip up front (seek and loop
// need random access to decoded frames), so VRAM use grows linearly with clip
// length: a 1080p NV12 frame is ~3.1 MB, so 600 frames is ~1.8 GiB — and the
// pool is shared with the live encode pipeline. Replay buffers can hold up to
// 300s (~9000 frames at 30fps, ~28 GiB at 1080p), which would exhaust GPU
// memory. Clips over this budget are routed to the CPU player, which uses a
// 2-GOP sliding window and handles arbitrary lengths with bounded memory.
const maxGPUClipFrames = 600

// useGPUReplayPlayer decides whether a clip should play on the GPU
// (NVOFA motion-blur) player or the CPU sliding-window player. gpuCapable is
// whether an NVOFA-capable FRUC engine is available; clipFrames is the number
// of buffered frames in the clip.
func useGPUReplayPlayer(gpuCapable bool, clipFrames int) bool {
	return gpuCapable && clipFrames <= maxGPUClipFrames
}

// gpuFrameDrainer is implemented by decoders that buffer frames for B-frame
// reordering and support an explicit EOS drain (mirrors the CPU player's
// frameDrainer). The non-CUDA GPUDecoder stub does not implement it, in which
// case draining is skipped.
type gpuFrameDrainer interface {
	SendEOS() error
	ReceiveFrame() ([]byte, int, int, error)
}

// gpuReplayPlayer decodes a clip on NVDEC, applies NVOFA optical-flow
// motion blur via CUDA kernels, encodes on NVENC, and outputs to a relay.
// Created per-Play, destroyed on complete.
type gpuReplayPlayer struct {
	config        GPUPlayerConfig
	cancel        context.CancelFunc
	done          chan struct{}
	once          sync.Once
	progress      atomic.Int64 // 0–1000 representing 0.0–1.0 playback progress
	videoInfoSent bool

	// Audio tracking.
	audioIdx          int
	outputAudioPTS    int64
	totalOutputFrames int
	audioCallCount    int

	// Absolute-time pacing.
	playbackStart time.Time

	// Pause mechanism (mirrors replayPlayer).
	pauseCh atomic.Pointer[chan struct{}]
	pauseMu sync.Mutex
	state   atomic.Value // PlayerState

	// Seek: send desired progress (0.0-1.0).
	seekCh chan float64

	// Speed changes mid-playback.
	speed atomic.Value // float64

	// Shutter angle changes mid-playback.
	shutterAngle atomic.Value // float64
}

// newGPUReplayPlayer creates a GPU-accelerated player for the given clip.
func newGPUReplayPlayer(cfg GPUPlayerConfig) *gpuReplayPlayer {
	p := &gpuReplayPlayer{
		config: cfg,
		done:   make(chan struct{}),
		seekCh: make(chan float64, 1),
	}
	p.speed.Store(cfg.Speed)
	p.shutterAngle.Store(cfg.ShutterAngle)

	// Initialize pause channel as closed (not paused).
	ch := make(chan struct{})
	close(ch)
	p.pauseCh.Store(&ch)

	p.state.Store(PlayerPlaying)
	return p
}

// Start begins playback in a background goroutine.
func (p *gpuReplayPlayer) Start(ctx context.Context) {
	ctx, p.cancel = context.WithCancel(ctx)
	go p.run(ctx)
}

// Stop cancels playback.
func (p *gpuReplayPlayer) Stop() {
	p.once.Do(func() {
		if p.cancel != nil {
			p.cancel()
		}
	})
}

// Wait blocks until the player finishes.
func (p *gpuReplayPlayer) Wait() {
	<-p.done
}

// Progress returns the current playback progress as a float64 between 0.0 and 1.0.
func (p *gpuReplayPlayer) Progress() float64 {
	return float64(p.progress.Load()) / 1000.0
}

// State returns the current player state.
func (p *gpuReplayPlayer) State() PlayerState {
	v := p.state.Load()
	if v == nil {
		return PlayerIdle
	}
	return v.(PlayerState)
}

// Speed returns the current playback speed.
func (p *gpuReplayPlayer) Speed() float64 {
	v := p.speed.Load()
	if v == nil {
		return 1.0
	}
	return v.(float64)
}

// Pause pauses playback, holding the current frame.
func (p *gpuReplayPlayer) Pause() {
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
func (p *gpuReplayPlayer) Resume() {
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
func (p *gpuReplayPlayer) SetSpeed(speed float64) {
	if speed < 0.25 {
		speed = 0.25
	}
	if speed > 1.0 {
		speed = 1.0
	}
	p.speed.Store(speed)
}

// SetShutterAngle changes the motion-blur shutter angle mid-playback.
// Normalized 0.0-1.0 (0.5 = 180°).
func (p *gpuReplayPlayer) SetShutterAngle(angle float64) {
	if angle < 0 {
		angle = 0
	}
	if angle > 1.0 {
		angle = 1.0
	}
	p.shutterAngle.Store(angle)
}

// Seek seeks to the given position (0.0-1.0) within the clip.
func (p *gpuReplayPlayer) Seek(position float64) {
	if position < 0.0 {
		position = 0.0
	}
	if position > 1.0 {
		position = 1.0
	}
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

// run is the main playback goroutine for the GPU player.
//
// Phase 1: Decode all clip frames via NVDEC → CPU YUV → Upload to GPU pool frames.
// Phase 2: Create NVENC encoder once dimensions are known.
// Phase 3: Signal ready, then loop over consecutive frame pairs applying
//
//	NVOFA optical flow + motion-blur kernel + NVENC encode.
func (p *gpuReplayPlayer) run(ctx context.Context) {
	defer close(p.done)
	defer p.config.OnDone()

	clip := p.config.Clip
	if len(clip) == 0 {
		return
	}

	// ----------------------------------------------------------------
	// Phase 1: Decode all frames and upload to GPU.
	// ----------------------------------------------------------------
	gpuCtx := p.config.GPUCtx
	pool := p.config.Pool

	// Create NVDEC decoder.
	decoder, err := gpu.NewGPUDecoder(gpuCtx, 0)
	if err != nil {
		slog.Error("gpu replay: NVDEC decoder creation failed", "err", err)
		return
	}
	defer decoder.Close()

	// Decode each frame and upload to GPU pool frame.
	var decoded []decodedGPUFrame
	defer func() {
		for _, df := range decoded {
			if df.frame != nil {
				df.frame.Release()
			}
		}
	}()

	// Track SPS/PPS from the first keyframe for OnVideoInfo.
	var firstSPS, firstPPS []byte

	// uploadDecoded acquires a pool frame, uploads the CPU YUV to GPU NV12,
	// and appends it to decoded. Returns false on failure (already logged).
	uploadDecoded := func(yuv []byte, w, h int, pts int64) bool {
		gpuFrame, acqErr := pool.Acquire()
		if acqErr != nil {
			slog.Error("gpu replay: pool acquire failed", "err", acqErr)
			return false
		}
		gpuFrame.Width = w
		gpuFrame.Height = h
		gpuFrame.PTS = pts

		if upErr := gpu.Upload(gpuCtx, gpuFrame, yuv, w, h); upErr != nil {
			slog.Error("gpu replay: upload failed", "err", upErr)
			gpuFrame.Release()
			return false
		}

		decoded = append(decoded, decodedGPUFrame{frame: gpuFrame, pts: pts})
		return true
	}

	// PTS of every frame fed to the decoder, used to assign timestamps to
	// frames recovered from the decoder's reorder tail during the EOS drain.
	var fedPTS []int64

	for _, bf := range clip {
		if bf.wireData == nil {
			// Ring-backed frame without wireData — skip (only in-memory supported).
			continue
		}

		// Convert AVC1 to Annex B for decoder, prepending SPS/PPS for keyframes.
		annexB := codec.AVC1ToAnnexB(bf.wireData)
		if len(annexB) == 0 {
			continue
		}
		if bf.isKeyframe {
			annexB = codec.PrependSPSPPS(bf.sps, bf.pps, annexB)
			if firstSPS == nil && len(bf.sps) > 0 {
				firstSPS = make([]byte, len(bf.sps))
				copy(firstSPS, bf.sps)
			}
			if firstPPS == nil && len(bf.pps) > 0 {
				firstPPS = make([]byte, len(bf.pps))
				copy(firstPPS, bf.pps)
			}
		}

		// NVDEC decode → CPU YUV420p.
		fedPTS = append(fedPTS, bf.pts)
		yuv, w, h, decErr := decoder.Decode(annexB)
		if decErr != nil {
			// EAGAIN from B-frame buffering is expected.
			continue
		}

		// Acquire GPU frame and upload CPU YUV → GPU NV12.
		if !uploadDecoded(yuv, w, h, bf.pts) {
			return
		}
	}

	// Drain the decoder's B-frame reorder tail (mirrors the CPU player's
	// decodeGOP SendEOS+ReceiveFrame drain). Without this, frames the
	// decoder buffered for reordering are silently dropped from the end of
	// the replay. The stub GPUDecoder does not implement the drainer.
	if drainer, ok := any(decoder).(gpuFrameDrainer); ok {
		if eosErr := drainer.SendEOS(); eosErr == nil {
			for {
				yuv, w, h, derr := drainer.ReceiveFrame()
				if derr != nil {
					break
				}
				// Assign the PTS of the next fed-but-not-yet-output frame.
				pts := int64(0)
				if idx := len(decoded); idx < len(fedPTS) {
					pts = fedPTS[idx]
				} else if len(fedPTS) > 0 {
					pts = fedPTS[len(fedPTS)-1]
				}
				if !uploadDecoded(yuv, w, h, pts) {
					return
				}
			}
		}
	}

	if len(decoded) == 0 {
		slog.Error("gpu replay: no frames decoded")
		return
	}

	// ----------------------------------------------------------------
	// Phase 2: Create NVENC encoder.
	// ----------------------------------------------------------------
	w, h := decoded[0].frame.Width, decoded[0].frame.Height
	sourceFPS := estimateFPSFromClip(clip)
	fpsNum, fpsDen := fpsToRational(sourceFPS)
	bitrate := estimateBitrate(w, h)

	gpuEnc, err := gpu.NewGPUEncoder(gpuCtx, w, h, fpsNum, fpsDen, bitrate)
	if err != nil {
		slog.Error("gpu replay: NVENC encoder creation failed", "err", err)
		return
	}
	defer gpuEnc.Close()

	// ----------------------------------------------------------------
	// Phase 3: Signal ready.
	// ----------------------------------------------------------------
	if p.config.OnReady != nil {
		p.config.OnReady()
	}
	if p.config.OnVideoInfo != nil && firstSPS != nil && firstPPS != nil {
		p.videoInfoSent = true
		p.config.OnVideoInfo(firstSPS, firstPPS, w, h)
	}
	p.state.Store(PlayerPlaying)

	// ----------------------------------------------------------------
	// Phase 4: Playback loop.
	// ----------------------------------------------------------------
	speed := p.config.Speed
	dupCount := int(math.Ceil(1.0 / speed))
	if dupCount < 1 {
		dupCount = 1
	}
	frameDuration := time.Second / 30 // 30fps output
	var ptsIncrement int64
	// numPairs is the number of (prev,curr) iterations. With N>=2 decoded
	// frames this is N-1 consecutive pairs. With exactly one decoded frame
	// (e.g. a clip/mark range containing a single keyframe, or where all but
	// one frame failed to decode) we still emit that one frame, so clamp to 1
	// — mirroring the CPU player, which iterates frames rather than pairs.
	numPairs := len(decoded) - 1
	if numPairs < 1 {
		numPairs = 1
	}
	totalFrames := numPairs * dupCount // pairs × dups
	if totalFrames <= 0 {
		totalFrames = 1
	}
	p.totalOutputFrames = totalFrames

	// Shutter angle is read from atomic each frame (supports mid-playback changes).

	// Acquire an output frame for motion blur results.
	outputFrame, err := pool.Acquire()
	if err != nil {
		slog.Error("gpu replay: output frame acquire failed", "err", err)
		return
	}
	defer outputFrame.Release()
	outputFrame.Width = w
	outputFrame.Height = h

	// CPU-side YUV buffer for RawVideoOutput (lazily used).
	var cpuYUV []byte
	if p.config.RawVideoOutput != nil {
		cpuYUV = make([]byte, video.YUV420FrameSize(w, h))
	}

	// Create timer for pacing.
	timer := time.NewTimer(frameDuration)
	defer timer.Stop()
	if !timer.Stop() {
		<-timer.C
	}

	outputPTS := p.config.InitialPTS
	p.outputAudioPTS = p.config.InitialPTS
	codecStr := "avc1.42C01E"
	var groupID uint32

	for {
		// Reset per-loop state.
		outputIdx := 0
		p.audioIdx = 0
		p.audioCallCount = 0
		p.playbackStart = time.Now()
		pacingIdx := 0

		// Recompute timing from current speed.
		speed = p.speed.Load().(float64)
		dupCount = int(math.Ceil(1.0 / speed))
		if dupCount < 1 {
			dupCount = 1
		}
		ptsIncrement = int64(3003.0 / speed)
		totalFrames = numPairs * dupCount
		if totalFrames <= 0 {
			totalFrames = 1
		}
		p.totalOutputFrames = totalFrames

		// Iterate over consecutive frame pairs. For a single decoded frame
		// numPairs==1 and curr falls back to prev so the frame is still
		// emitted (duplicated for the slow-mo duration).
		for i := 0; i < numPairs; i++ {
			prev := decoded[i]
			curr := prev
			if i+1 < len(decoded) {
				curr = decoded[i+1]
			}

			// Check for seek.
			select {
			case pos := <-p.seekCh:
				targetPair := int(pos * float64(numPairs))
				if targetPair >= numPairs {
					targetPair = numPairs - 1
				}
				if targetPair < 0 {
					targetPair = 0
				}
				outputIdx = targetPair * dupCount
				p.playbackStart = time.Now()
				pacingIdx = 0
				i = targetPair - 1 // -1 because loop increments
				if i < -1 {
					i = -1
				}
				continue
			default:
			}

			// Compute optical flow once per pair.
			hasFlow := p.config.FRUC.ComputeFlow(prev.frame, curr.frame)

			// Check for speed changes.
			newSpeed := p.speed.Load().(float64)
			if newSpeed != speed {
				speed = newSpeed
				dupCount = int(math.Ceil(1.0 / speed))
				if dupCount < 1 {
					dupCount = 1
				}
				ptsIncrement = int64(3003.0 / speed)
				totalFrames = numPairs * dupCount
				if totalFrames <= 0 {
					totalFrames = 1
				}
				p.totalOutputFrames = totalFrames
				p.playbackStart = time.Now()
				pacingIdx = 0
			}

			for dup := 0; dup < dupCount; dup++ {
				// Check cancellation.
				select {
				case <-ctx.Done():
					return
				default:
				}

				// Check pause.
				chPtr := p.pauseCh.Load()
				if chPtr != nil {
					select {
					case <-*chPtr:
					case <-ctx.Done():
						return
					}
				}

				// If deadline is far in the past (e.g. after pause/resume), reset pacing.
				deadline := p.playbackStart.Add(time.Duration(pacingIdx) * frameDuration)
				if time.Until(deadline) < -frameDuration*2 {
					p.playbackStart = time.Now()
					pacingIdx = 0
				}

				// Compute alpha within the pair.
				alpha := float64(dup+1) / float64(dupCount+1)

				// Force IDR at frame 0 and every 60 frames (2 seconds at 30fps).
				forceIDR := outputIdx == 0 || outputIdx%60 == 0

				// Apply motion blur (or interpolation fallback).
				outputFrame.PTS = outputPTS
				curShutter := p.shutterAngle.Load().(float64)
				var blurErr error
				if hasFlow && curShutter > 0 {
					blurErr = p.config.FRUC.MotionBlur(prev.frame, curr.frame, outputFrame, alpha, curShutter)
				} else {
					blurErr = p.config.FRUC.Interpolate(prev.frame, curr.frame, outputFrame, alpha)
				}
				if blurErr != nil {
					slog.Error("gpu replay: motion blur/interpolate failed", "err", blurErr)
					return
				}

				// NVENC encode.
				encoded, isIDR, encErr := gpuEnc.EncodeGPU(outputFrame, forceIDR)
				if encErr != nil {
					slog.Error("gpu replay: encode failed", "err", encErr)
					return
				}

				if encoded != nil {
					// Convert Annex B → AVC1 for relay.
					avc1 := codec.AnnexBToAVC1(encoded)
					if len(avc1) == 0 {
						avc1 = encoded
					}

					// Extract SPS/PPS from IDR frames.
					var spsNALU, ppsNALU []byte
					if isIDR {
						for _, nalu := range codec.ExtractNALUs(avc1) {
							if len(nalu) == 0 {
								continue
							}
							switch nalu[0] & 0x1F {
							case 7:
								spsNALU = nalu
								codecStr = codec.ParseSPSCodecString(nalu)
							case 8:
								ppsNALU = nalu
							}
						}
						if !p.videoInfoSent && p.config.OnVideoInfo != nil && spsNALU != nil && ppsNALU != nil {
							p.videoInfoSent = true
							p.config.OnVideoInfo(spsNALU, ppsNALU, w, h)
						}
					}

					// Start MoQ group on first keyframe.
					if isIDR && groupID == 0 {
						groupID++
					}

					if p.config.Output != nil {
						frame := &media.VideoFrame{
							PTS:        outputPTS,
							IsKeyframe: isIDR,
							WireData:   avc1,
							Codec:      codecStr,
							GroupID:    groupID,
							SPS:        spsNALU,
							PPS:        ppsNALU,
						}
						p.config.Output(frame)
					}
				}

				// Optional: download GPU frame to CPU for raw pipeline output.
				if p.config.RawVideoOutput != nil {
					dlErr := gpu.Download(gpuCtx, cpuYUV, outputFrame, w, h)
					if dlErr != nil {
						slog.Warn("gpu replay: download for raw output failed", "err", dlErr)
					} else {
						p.config.RawVideoOutput(cpuYUV, w, h, outputPTS)
					}
				}

				// Pace: wait until deadline.
				deadline = p.playbackStart.Add(time.Duration(pacingIdx) * frameDuration)
				wait := time.Until(deadline)
				if wait > 0 {
					timer.Reset(wait)
					select {
					case <-ctx.Done():
						return
					case <-timer.C:
					}
				}

				// Emit interleaved audio.
				p.emitGPUAudioForFrame(outputIdx)

				outputPTS += ptsIncrement
				outputIdx++
				pacingIdx++

				if totalFrames > 0 {
					p.progress.Store(int64(outputIdx * 1000 / totalFrames))
				}
			}
		}

		if !p.config.Loop {
			return
		}
	}
}

// emitGPUAudioForFrame distributes audio frames proportionally across
// output video frames, matching the CPU player's proportional distribution.
func (p *gpuReplayPlayer) emitGPUAudioForFrame(outputIdx int) {
	audioClip := p.config.AudioClip
	if len(audioClip) == 0 || p.config.AudioOutput == nil {
		return
	}

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
