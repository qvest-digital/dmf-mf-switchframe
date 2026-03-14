package clip

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"time"
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

	// RawVideoOutput sends decoded YUV420 frame data to the switcher pipeline.
	// Called for every output frame (including duplicates for slow-mo).
	RawVideoOutput func(yuv []byte, w, h int, pts int64)

	// AudioOutput sends audio frame data during playback.
	AudioOutput func(data []byte, pts int64, sampleRate, channels int)

	// OnDone is called when playback ends (non-loop clip finishes) or is stopped.
	OnDone func()

	// OnReady is called when the player is ready for playback.
	OnReady func()

	// OnVideoInfo signals codec parameters (SPS/PPS) and dimensions.
	OnVideoInfo func(sps, pps []byte, width, height int)
}

// Player plays back a pre-demuxed clip with support for pause, seek,
// variable speed (0.25x-2.0x), hold-last-frame, and loop.
type Player struct {
	config   PlayerConfig
	state    atomic.Value  // PlayerState
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

	clip := p.config.Clip
	if len(clip) == 0 {
		if p.config.OnDone != nil {
			p.config.OnDone()
		}
		return
	}

	// Estimate source FPS from clip PTS values.
	sourceFPS := estimateFPSFromClipFrames(clip)

	p.state.Store(StatePlaying)

	outputPTS := p.config.InitialPTS
	p.outputAudioPTS = p.config.InitialPTS

	for {
		cancelled := p.playClip(ctx, clip, sourceFPS, &outputPTS)
		if cancelled {
			return
		}

		if !p.config.Loop {
			// Non-loop: fire OnDone, then enter hold state.
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

		// Decode H.264 to YUV420 (once per source frame, reused for all dups).
		frameData := f.wireData
		fw, fh := p.config.Width, p.config.Height
		if p.config.DecodeFrame != nil {
			decoded, dw, dh, err := p.config.DecodeFrame(f.wireData)
			if err != nil {
				// Skip frames the decoder is buffering (e.g., B-frame reorder).
				frameIdx++
				continue
			}
			// Deep-copy: decoder may reuse its output buffer.
			frameData = make([]byte, len(decoded))
			copy(frameData, decoded)
			fw, fh = dw, dh
		}

		// Output frames with duplication for slow-mo.
		for dup := 0; dup < dupCount; dup++ {
			if p.outputFrame(ctx, frameData, fw, fh, outputPTS, ptsPerFrame, frameDuration, timer, playbackStart, &pacingIdx) {
				return true
			}

			// Save decoded YUV as hold frame (deep-copy to prevent aliasing).
			p.holdMu.Lock()
			p.holdFrame = make([]byte, len(frameData))
			copy(p.holdFrame, frameData)
			p.holdW = fw
			p.holdH = fh
			p.holdPTS = *outputPTS
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

	// Mark final progress.
	p.progress.Store(1000)

	return false
}

// outputFrame outputs a single decoded frame, handling pause and pacing.
// Returns true if context was cancelled.
func (p *Player) outputFrame(
	ctx context.Context,
	frameData []byte, fw, fh int,
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

	// Output decoded YUV video.
	if p.config.RawVideoOutput != nil {
		p.config.RawVideoOutput(frameData, fw, fh, *outputPTS)
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
	p.holdMu.Unlock()

	if holdData == nil || p.config.RawVideoOutput == nil {
		return
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.config.RawVideoOutput(holdData, holdW, holdH, *outputPTS)
			*outputPTS += 90000 // 1 second in 90kHz PTS
		}
	}
}

// estimateFPSFromClipFrames estimates FPS from buffered frame PTS values.
func estimateFPSFromClipFrames(clip []bufferedFrame) float64 {
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
