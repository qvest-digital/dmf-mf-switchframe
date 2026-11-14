package clip

import (
	"context"
	"log/slog"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/switchframe/server/clock"
	"github.com/zsiec/switchframe/server/codec"
)

// PlayerConfig configures a clip player instance.
type PlayerConfig struct {
	Clip       []BufferedFrame
	AudioClip  []BufferedAudioFrame
	Speed      float64
	Loop       bool
	InitialPTS int64
	// OutputFPSNum/OutputFPSDen is the rate the player outputs at, normally
	// the pipeline's. Each output slot shows the picture whose time span
	// holds the slot, so a clip at another rate gets a fixed cadence, such
	// as 2,2,1 for 30 fps on 50 fps. Zero outputs at the clip's own rate.
	OutputFPSNum int
	OutputFPSDen int
	Width        int // frame width (from clip metadata)
	Height       int // frame height (from clip metadata)

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

	// PCMOutput, on a Timed player, hands over the clip's audio decoded to
	// PCM at AudioRate (48 kHz when 0), with the encoder priming and the
	// padding taken off: clip sample 0 is stamped with the PTS of the
	// first picture, and every sample after it on from there, so the
	// audio lies on the pictures to the sample. start marks the first PCM
	// of the playback. A loop plays each pass of the audio for as long as
	// a pass of the pictures lasts, the next pass following the last
	// sample without a gap.
	PCMOutput func(pcm []float32, pts int64, channels int, start bool)
	AudioRate int
	PCMCodec  PCMCodec

	// OnDone is called when playback ends (non-loop clip finishes) or is stopped.
	OnDone func()

	// OnVideoInfo signals codec parameters (SPS/PPS) and dimensions.
	// Called once on the first keyframe with SPS/PPS.
	OnVideoInfo func(sps, pps []byte, width, height int)

	// Timed places the output on the engine timeline (package clock) at
	// the output rate, replacing InitialPTS: the first frame takes the
	// read point of the next program tick, so that tick shows it, and
	// every frame goes out when its PTS is due. The audio is a stream of
	// its own, stamped frame after frame and handed over ahead of the
	// video it plays with, so a source placed by these PTS never runs dry
	// and never steps. It plays at speed 1 only.
	Timed bool

	// Cued makes the player decode up to its first picture and hold it
	// until Release or StartAt: the picture goes out once a second
	// meanwhile, so the source shows what a start puts on air, and the
	// start has no decode to wait for. Requires Timed.
	Cued bool

	// OnStart reports the first video frame (media "video") and the first
	// audio (media "audio") of the playback: the time since Release, or
	// Start for a player not cued, and the pre-roll, "ready" when the first
	// picture was decoded before the start, "waited" when the start waited
	// for it and "missed" for a player not cued.
	OnStart func(media string, delay time.Duration, preroll string)
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

	// Loop changes mid-playback.
	loop atomic.Bool

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

	// runOn is set when the clip starts with an IDR picture: the decoder
	// then runs on from the end of a pass into the next one.
	runOn bool

	// Pacing: slot paceIdx is due at paceStart + paceIdx frame periods. A
	// pass that starts with paceIdx 0 anchors paceStart at its first
	// output; a loop pass whose first frames the previous pass already fed
	// to the decoder carries both over, so the wrap keeps the cadence.
	paceStart time.Time
	paceIdx   int
	// clock is the time pacing reads and waits on; nil is the wall clock.
	clock paceClock

	// pts advances the output PTS by one output period per slot.
	pts ptsStep

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

	// Start (Cued, Timed). release is closed by Release; releasedAt is
	// when, in Unix nanoseconds.
	release     chan struct{}
	releaseOnce sync.Once
	releasedAt  atomic.Int64
	started     bool   // the first frame went out
	audioOut    bool   // the first audio went out
	preroll     string // how the first picture was had, for OnStart
	rate        clock.Rate

	// StartAt runs on another goroutine than the playback; startMu orders
	// the two. held is the first slot a cued player holds while it waits
	// for its start; onTick is set when StartAt put that slot out, and
	// tickNext is then the PTS of the slot after it.
	startMu  sync.Mutex
	held     *heldSlot
	onTick   bool
	tickNext int64

	// Timed audio. audioRem carries the remainder of the 90 kHz audio
	// steps; audioPassStart is where the pass of the audio clip under way
	// started; audioResync re-places the audio onto the video after a
	// seek or a speed change.
	audioRem       int64
	audioPassStart int64
	audioResync    bool

	// PCM audio (PCMOutput). pcmBase is the PTS of the stream's first
	// sample and pcmPut the samples handed over since; pcmFirst asks the
	// next emission to start the stream at the first picture, pcmResync
	// to place it on the video again. pcmVideoEnd and pcmAudioEnd are
	// where, in clip samples from the start of the stream's first pass,
	// the passes of the video and of the audio under way end; pcmAcc
	// carries the remainder of the video's pass lengths.
	pcmClip     []BufferedAudioFrame
	pcm         *PCMStream
	pcmBase     int64
	pcmPut      int64
	pcmFirst    bool
	pcmResync   bool
	pcmStarted  bool
	pcmEnded    bool // the stream ran out
	pcmVideoEnd int64
	pcmAudioEnd int64
	pcmAcc      int64
	// pcmCue is the start of the stream a cued player decodes ahead of
	// StartAt, as it was with the loop setting pcmCueLoop.
	pcmCue     []float32
	pcmCueLoop bool
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

	// The AAC goes out as a stream carries it, the PCM is decoded from
	// every frame.
	pcmClip := config.AudioClip
	config.AudioClip = PresentedAudio(config.AudioClip)
	p := &Player{
		pcmClip: pcmClip,
		config:  config,
		done:    make(chan struct{}),
		seekCh:  make(chan float64, 1),
		release: make(chan struct{}),
		preroll: "missed",
	}
	if config.Cued {
		p.preroll = "ready"
	}

	// Initialize speed.
	speed := config.Speed
	if speed <= 0 {
		speed = 1.0
	}
	p.speed.Store(speed)

	// Initialize loop.
	p.loop.Store(config.Loop)

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

// Start begins playback in a background goroutine. A cued player decodes
// its first picture and holds it until Release.
func (p *Player) Start(ctx context.Context) {
	ctx, p.cancel = context.WithCancel(ctx)
	if !p.config.Cued {
		p.Release()
	}
	go p.run(ctx)
}

// Release starts the playback of a cued player: its first picture goes
// out at once, or as soon as it is decoded. The player reports
// StatePlaying from here on.
func (p *Player) Release() {
	p.releaseOnce.Do(func() {
		p.releasedAt.Store(time.Now().UnixNano())
		p.pauseMu.Lock()
		if p.State() == StateLoaded {
			p.state.Store(StatePlaying)
		}
		p.pauseMu.Unlock()
		close(p.release)
	})
}

// StartAt starts a cued, timed player on the program tick whose read
// point is readPTS, from that tick: the held first picture, and the audio
// up to the slot after it, go out stamped from readPTS before StartAt
// returns, so the tick shows the picture and plays the clip's first
// sample whenever in a period the start was asked for. The playback goes
// on from the slot after it. A player whose first picture is not decoded
// yet starts as on Release. It reports whether it started the player;
// one started already, by Release or StartAt, is left alone.
func (p *Player) StartAt(readPTS int64) bool {
	p.startMu.Lock()
	defer p.startMu.Unlock()
	if p.released() {
		return false
	}
	p.Release()
	if h := p.held; h != nil && p.config.Timed {
		p.startOnTick(h, readPTS)
	}
	return true
}

// heldSlot is a cued player's first output slot: its picture, and what
// the slot's audio is emitted with.
type heldSlot struct {
	frameData   []byte
	fw, fh      int
	srcKeyframe bool
	wireData    []byte
	isKeyframe  bool
	sps, pps    []byte
	speed       float64
	pic, total  int
}

// putOut puts the picture of h out stamped pts.
func (p *Player) putOut(h *heldSlot, pts int64) {
	if h.frameData != nil && p.config.RawVideoOutput != nil {
		p.config.RawVideoOutput(h.frameData, h.fw, h.fh, pts, h.srcKeyframe)
	}
	if h.wireData != nil && p.config.VideoOutput != nil {
		p.config.VideoOutput(h.wireData, pts, h.isKeyframe, h.sps, h.pps)
	}
}

// startOnTick puts the held slot h out stamped readPTS, with the audio
// the playback goroutine emits after a slot. The goroutine waits in
// awaitRelease meanwhile, and goes on from the slot after it. Caller
// holds startMu.
func (p *Player) startOnTick(h *heldSlot, readPTS int64) {
	p.placeAudio(readPTS)
	p.noteStart("video")
	p.putOut(h, readPTS)
	next := readPTS + p.pts.next()
	p.emitTimedAudio(h.speed, h.pic, h.total, next)
	// Only the start tick's PCM, decoded with the cue: decoding and
	// resampling it here costs the tick milliseconds. The goroutine hands
	// over the rest as it goes on with the slot after, which the next tick
	// reads with it.
	if p.pcmCue != nil && p.pcmCueLoop == p.loop.Load() {
		p.pcmFirst = false
		p.config.PCMOutput(p.pcmCue, readPTS, p.pcm.Channels(), !p.pcmStarted)
		p.pcmStarted = true
		p.pcmPut = int64(len(p.pcmCue) / p.pcm.Channels())
	} else {
		p.emitTimedPCM(h.speed, h.pic, h.total, next, next)
	}
	p.pcmCue = nil
	p.onTick, p.tickNext = true, next
}

// placeAudio starts a timed player's audio with its first frame, at pts.
func (p *Player) placeAudio(pts int64) {
	p.outputAudioPTS, p.audioPassStart, p.audioRem = pts, pts, 0
	p.pcmBase, p.pcmFirst = pts, true
}

// released reports whether Release was called.
func (p *Player) released() bool {
	select {
	case <-p.release:
		return true
	default:
		return false
	}
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

// SetLoop enables or disables loop mode. Safe to call during playback.
func (p *Player) SetLoop(loop bool) {
	p.loop.Store(loop)
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

	if p.config.Timed && p.config.PCMOutput != nil && len(p.pcmClip) > 0 {
		s, err := NewPCMStream(p.pcmClip, p.audioRate(), p.config.PCMCodec)
		if err != nil {
			slog.Warn("clip: audio does not decode, playing without it", "error", err)
		} else {
			p.pcm = s
			defer s.Close()
		}
	}

	// Estimate source FPS from clip PTS values.
	sourceFPS := estimateFPSFromClipFrames(clip)
	p.sourceFPS = sourceFPS
	p.runOn = startsWithIDR(clip[0])

	outputPTS := p.config.InitialPTS
	p.outputAudioPTS = p.config.InitialPTS

	startFrame := 0
	for {
		cancelled, fedNext := p.playClip(ctx, clip, sourceFPS, &outputPTS, startFrame)
		if cancelled {
			return
		}
		startFrame = fedNext

		if !p.loop.Load() {
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

		// Loop: unless the pass already fed the next one's first frames,
		// flush the decoder, then reset indices.
		if startFrame == 0 {
			if fd, ok := p.config.Decoder.(FlushableDecoder); ok {
				fd.Flush()
			}
		}
		// Reset decode error tracking so first-error logging triggers again.
		p.decodeErrors = 0
		p.decodeErrorFirst = ""
		p.decodeAttempts = 0
		p.decodeSuccesses = 0
		p.decodeBuffering = 0
		// Reset videoInfoSent so browser gets fresh SPS/PPS on loop restart.
		p.videoInfoSent = false

		if !p.config.Timed {
			p.audioIdx = 0
		}
		p.audioCallCount = 0
	}
}

// playClip plays through all frames once, starting with frame startFrame:
// the frames before it the previous pass has already fed to the decoder.
// Returns true if the context was cancelled, and how many frames of the next
// pass it fed to the decoder to get its own last pictures out.
func (p *Player) playClip(ctx context.Context, clip []BufferedFrame, sourceFPS float64, outputPTS *int64, startFrame int) (cancelled bool, fedNext int) {
	totalClipFrames := len(clip)

	srcNum, srcDen := clipRational(sourceFPS)
	outNum, outDen := p.config.OutputFPSNum, p.config.OutputFPSDen
	if outNum <= 0 || outDen <= 0 {
		outNum, outDen = srcNum, srcDen
	}
	ptsStep := &p.pts
	ptsStep.num, ptsStep.den = int64(outNum), int64(outDen)
	p.rate = clock.Rate{Num: int64(outNum), Den: int64(outDen)}
	frameDuration := time.Second * time.Duration(outDen) / time.Duration(outNum)

	speed := p.speed.Load().(float64)
	cad := newCadence(outNum, outDen, srcNum, srcDen, speed)

	// Compute total output frames for progress tracking.
	totalFrames := cad.start(totalClipFrames)
	p.totalOutputFrames = totalFrames

	// Create pacing timer.
	timer := time.NewTimer(frameDuration)
	defer timer.Stop()
	if !timer.Stop() {
		<-timer.C
	}

	if startFrame == 0 {
		p.paceIdx = 0
	}
	outputIdx := 0
	// Frames fed to the decoder whose pictures have not come out; the
	// previous pass fed this one's first startFrame frames.
	held := startFrame
	pic := 0 // pictures of this pass output or skipped, in presentation order

	// -1 = no rewind in flight; otherwise exclusive end of the decode-only
	// window that primes the decoder after a seek.
	primeUntilIdx := -1

	// emit outputs one decoded picture in its n paced slots, each followed
	// by its share of the audio. The browser relay gets the picture once.
	// Returns true if the context was cancelled.
	emit := func(n int, frameData []byte, fw, fh int, srcKeyframe bool, browserWireData []byte, browserKeyframe bool, browserSPS, browserPPS []byte) bool {
		for slot := 0; slot < n; slot++ {
			if slot == 1 {
				srcKeyframe, browserWireData, browserKeyframe = false, nil, false
			}
			// Pacing starts at the first frame output, not when decoding
			// starts: a threaded decoder returns its first picture several
			// calls in, and slots counted from before that would be spent
			// already and go out back to back.
			if p.paceIdx == 0 {
				p.paceStart = p.paceClock().now()
			}
			onTick := false
			if !p.started {
				h := &heldSlot{frameData, fw, fh, srcKeyframe, browserWireData, browserKeyframe, browserSPS, browserPPS, speed, pic, totalClipFrames}
				var cancelled bool
				if onTick, cancelled = p.start(ctx, h, outputPTS); cancelled {
					return true
				}
			}
			if onTick {
				// StartAt put the slot out on its tick.
				p.paceIdx++
				if p.awaitDue(ctx, *outputPTS, timer) {
					return true
				}
			} else if p.outputFrame(ctx, frameData, fw, fh, srcKeyframe, browserWireData, browserKeyframe, browserSPS, browserPPS, outputPTS, ptsStep, frameDuration, timer, &p.paceStart, &p.paceIdx) {
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

			// Emit audio proportionally, or, timed, ahead of the slot.
			if p.config.Timed {
				p.emitTimedAudio(speed, pic, totalClipFrames, *outputPTS)
				p.emitTimedPCM(speed, pic, totalClipFrames, *outputPTS, clock.NowPTS()+pcmLead.Milliseconds()*90)
			} else {
				p.emitAudioForVideoFrame(outputIdx, totalFrames)
			}
		}
		return false
	}

	for frameIdx := startFrame; frameIdx < totalClipFrames; {
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
			// Walk backwards to the nearest keyframe so the decoder sees a
			// valid reference chain before the target. Without this, feeding
			// a mid-GOP P-frame produces green/garbage output until the next
			// natural keyframe.
			keyframeIdx := targetIdx
			for keyframeIdx > 0 && !clip[keyframeIdx].isKeyframe {
				keyframeIdx--
			}
			// Flush the decoder so its internal reference-frame state does
			// not carry stale pre-seek frames into the rewound decode.
			if fd, ok := p.config.Decoder.(FlushableDecoder); ok {
				fd.Flush()
			}
			held = 0
			frameIdx = keyframeIdx
			primeUntilIdx = targetIdx
			pic = targetIdx
			outputIdx = cad.start(pic)
			// Reset pacing to avoid a burst of catch-up frames.
			p.paceIdx = 0
			// Update audio index proportionally.
			if len(p.config.AudioClip) > 0 {
				p.audioIdx = targetIdx * len(p.config.AudioClip) / totalClipFrames
				p.audioCallCount = outputIdx
			}
			p.audioResync = true
			p.pcmResync = true
			continue
		default:
		}

		// Check for speed changes. Defer while priming: outputIdx was
		// pinned to the target's first slot by the seek branch; recomputing
		// from a primer frameIdx would rewind the progress/audio counters
		// below their intended post-seek value.
		if frameIdx >= primeUntilIdx {
			newSpeed := p.speed.Load().(float64)
			if newSpeed != speed {
				speed = newSpeed
				cad = newCadence(outNum, outDen, srcNum, srcDen, speed)
				totalFrames = cad.start(totalClipFrames)
				p.totalOutputFrames = totalFrames
				outputIdx = cad.start(pic)
				p.paceIdx = 0
				p.audioResync = true
				p.pcmResync = true
			}
		}

		f := clip[frameIdx]

		// Decode video to YUV420 for the switcher pipeline.
		// Wire data forwarding to the browser relay is independent of decode
		// success -- the browser has its own decoder and always gets frames.
		frameData, fw, fh, buffered := p.decodeClipFrame(f, frameIdx)
		if buffered {
			held++
		}

		// A frame the decoder kept back comes out of a later Decode call or
		// the drain below. It gets its output slot then: giving it one now
		// would hold the previous picture for a slot here and push the
		// drained frames out unpaced at the end, with the audio, which is
		// emitted per slot, running that many frames ahead of the video.
		if buffered {
			frameIdx++
			continue
		}

		// A picture that falls between two output slots is skipped before
		// it is re-encoded: the browser relay's stream must hold only the
		// pictures it is sent.
		priming := frameIdx < primeUntilIdx
		slots := 1
		if !priming {
			slots = cad.slots(pic)
			pic++
			if slots == 0 {
				frameIdx++
				continue
			}
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

		// Signal codec parameters on first keyframe with SPS/PPS. This
		// must run before the priming guard so a rewound keyframe that is
		// the first one we've seen (e.g. pre-Start seek) still registers
		// codec params with the consumer.
		if browserKeyframe && !p.videoInfoSent && p.config.OnVideoInfo != nil {
			if browserSPS != nil && browserPPS != nil {
				p.config.OnVideoInfo(browserSPS, browserPPS, fw, fh)
				p.videoInfoSent = true
			}
		}

		// Priming: emit to the browser relay (its decoder needs IDR+chain
		// too) and advance outputPTS so the GOP cache sees unique,
		// monotonic timestamps, but skip the switcher pipeline and pacing.
		if priming {
			select {
			case <-ctx.Done():
				return true, 0
			default:
			}
			if p.config.VideoOutput != nil {
				p.config.VideoOutput(browserWireData, *outputPTS, browserKeyframe, browserSPS, browserPPS)
			}
			*outputPTS += ptsStep.next()
			frameIdx++
			continue
		}

		if emit(slots, frameData, fw, fh, f.isKeyframe, browserWireData, browserKeyframe, browserSPS, browserPPS) {
			return true, 0
		}
		frameIdx++
	}

	// The last pictures are still in the decoder. Looping, feed it the next
	// pass's first frames until they come out, so the wrap neither drains
	// nor restarts the decoder: a restarted frame-threaded decoder returns
	// nothing for its first calls, which held the last picture and left a
	// gap in the audio at every loop. The next pass starts after the frames
	// fed here. Only an IDR resets picture order: run on into an open-GOP
	// I frame, the decoder lost the last picture of the pass and put out
	// wrong ones for the first of the next, so such a clip drains instead.
	if p.loop.Load() && p.runOn && held > 0 && p.config.DecodeFrame != nil && totalClipFrames > 2*held {
		for held > 0 && fedNext < totalClipFrames {
			select {
			case <-ctx.Done():
				return true, 0
			default:
			}
			frameData, fw, fh, buffered := p.decodeClipFrame(clip[fedNext], fedNext)
			fedNext++
			if buffered {
				continue
			}
			if frameData == nil {
				continue // failed: no picture came out
			}
			held--
			slots := cad.slots(pic)
			pic++
			if slots == 0 {
				continue
			}
			wd, kf, sps, pps := p.reencodeForBrowser(frameData, fw, fh, *outputPTS, false)
			if emit(slots, frameData, fw, fh, false, wd, kf, sps, pps) {
				return true, 0
			}
		}
	} else if p.config.Decoder != nil {
		// Drain remaining buffered frames from the decoder (B-frame reordering).
		if drainable, ok := p.config.Decoder.(DrainableDecoder); ok {
			_ = drainable.SendEOS()
			for {
				decoded, dw, dh, err := drainable.ReceiveFrame()
				if err != nil {
					break
				}
				p.decodeSuccesses++
				slots := cad.slots(pic)
				pic++
				if slots == 0 {
					continue
				}
				frameData := make([]byte, len(decoded))
				copy(frameData, decoded)
				// Drained frames take their paced slots like any other;
				// without a browser encoder they reach only the pipeline.
				wd, kf, sps, pps := p.reencodeForBrowser(frameData, dw, dh, *outputPTS, false)
				if emit(slots, frameData, dw, dh, false, wd, kf, sps, pps) {
					return true, 0
				}
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

	return false, fedNext
}

// startsWithIDR reports whether f carries an IDR picture.
func startsWithIDR(f BufferedFrame) bool {
	for _, nalu := range codec.ExtractNALUs(f.wireData) {
		if len(nalu) == 0 {
			continue
		}
		if f.IsHEVC() {
			if t := codec.HEVCNALUType(nalu[0]); t == codec.HEVCNALIDRWRadl || t == codec.HEVCNALIDRNLP {
				return true
			}
		} else if nalu[0]&0x1f == 5 {
			return true
		}
	}
	return false
}

// decodeClipFrame feeds frame f to the decoder and returns the picture it
// gives back: nil when decoding failed, buffered when the decoder kept the
// frame and returned nothing. Without a decoder (tests) the wire data is
// the picture.
func (p *Player) decodeClipFrame(f BufferedFrame, frameIdx int) (frameData []byte, fw, fh int, buffered bool) {
	fw, fh = p.config.Width, p.config.Height
	isHEVC := f.IsHEVC()
	if p.config.DecodeFrame != nil {
		// Convert wire format to Annex B for the decoder (reuses buffer).
		p.annexBBuf = codec.WireToAnnexBInto(f.wireData, p.annexBBuf[:0])
		if len(p.annexBBuf) > 0 {
			// Prepend parameter sets for keyframes so the decoder can initialize.
			// Use prependBuf as a separate destination to avoid aliasing.
			decoderInput := p.annexBBuf
			if f.isKeyframe && len(f.sps) > 0 {
				if isHEVC {
					p.prependBuf = codec.PrependVPSSPSPPSInto(f.vps, f.sps, f.pps, p.annexBBuf, p.prependBuf[:0])
				} else {
					p.prependBuf = codec.PrependSPSPPSInto(f.sps, f.pps, p.annexBBuf, p.prependBuf[:0])
				}
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
				// EAGAIN ("buffering") is expected for B-frame reordering --
				// the decoder needs reference frames before it can output.
				if strings.Contains(errMsg, "buffering") {
					p.decodeBuffering++
					buffered = true
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
							"codec", f.Codec(),
						)
					}
				}
			}
		}
	} else {
		// No decoder (testing) -- pass wire data through as frame data.
		frameData = f.wireData
	}
	return frameData, fw, fh, buffered
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
	pts *ptsStep,
	frameDuration time.Duration,
	timer *time.Timer,
	playbackStart *time.Time,
	pacingIdx *int,
) bool {
	// Check for pause. If paused, this blocks until resumed or cancelled.
	chPtr := p.pauseCh.Load()
	if chPtr != nil {
		select {
		case <-*chPtr:
			// Not paused.
		default:
			select {
			case <-*chPtr:
				// Resumed: the slots due during the pause are not owed,
				// so pacing restarts from this one, and the PCM handed
				// over ahead of the pause has played.
				p.pcmResync = true
				*playbackStart = p.paceClock().now().Add(-time.Duration(*pacingIdx) * frameDuration)
			case <-ctx.Done():
				return true
			}
		}
	}

	// Check context before output.
	select {
	case <-ctx.Done():
		return true
	default:
	}

	if p.config.Timed {
		p.keepOnTimeline(outputPTS)
	}

	// Output decoded YUV video to switcher pipeline (only when decode succeeded).
	if frameData != nil && p.config.RawVideoOutput != nil {
		p.config.RawVideoOutput(frameData, fw, fh, *outputPTS, srcKeyframe)
	}

	// Forward H.264 wire data to browser relay (re-encoded or original).
	if wireData != nil && p.config.VideoOutput != nil {
		p.config.VideoOutput(wireData, *outputPTS, isKeyframe, sps, pps)
	}

	// Timed, the next frame is due at its PTS.
	if p.config.Timed {
		*outputPTS += pts.next()
		*pacingIdx++
		return p.awaitDue(ctx, *outputPTS, timer)
	}

	// Pace using absolute time to prevent drift from callback overhead. The
	// frame just output is on screen until the next slot starts.
	deadline := (*playbackStart).Add(time.Duration(*pacingIdx+1) * frameDuration)
	if wait := deadline.Sub(p.paceClock().now()); wait > 0 && p.paceClock().wait(ctx, timer, wait) {
		return true
	}

	*outputPTS += pts.next()
	*pacingIdx++

	return false
}

// paceClock is the time a player paces its output slots by.
type paceClock interface {
	now() time.Time
	// wait returns after d, on timer, or true as soon as ctx is done.
	wait(ctx context.Context, timer *time.Timer, d time.Duration) bool
}

type wallClock struct{}

func (wallClock) now() time.Time { return time.Now() }

func (wallClock) wait(ctx context.Context, timer *time.Timer, d time.Duration) bool {
	timer.Reset(d)
	select {
	case <-ctx.Done():
		return true
	case <-timer.C:
		return false
	}
}

func (p *Player) paceClock() paceClock {
	if p.clock == nil {
		return wallClock{}
	}
	return p.clock
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
		p.outputAudioPTS += audioStep(&p.audioRem, af.sampleRate)
		p.audioIdx++
	}
}

// awaitDue waits until the slot stamped pts is due. Returns true if the
// context was cancelled.
func (p *Player) awaitDue(ctx context.Context, pts int64, timer *time.Timer) bool {
	wait := time.Duration(clock.TimestampOf(pts) - clock.TAINow())
	if wait <= 0 {
		return false
	}
	timer.Reset(wait)
	select {
	case <-ctx.Done():
		return true
	case <-timer.C:
		return false
	}
}

// start starts the playback on its first slot h. A cued player holds h
// until Release or StartAt; onTick reports that StartAt put h out, which
// leaves *outputPTS on the slot after it.
func (p *Player) start(ctx context.Context, h *heldSlot, outputPTS *int64) (onTick, cancelled bool) {
	if p.config.Cued {
		if onTick, cancelled = p.awaitRelease(ctx, h); cancelled {
			return false, true
		}
	}
	p.started = true
	if onTick {
		*outputPTS = p.tickNext
		return true, false
	}
	if p.config.Timed {
		*outputPTS = timelineStart(p.rate, clock.TAINow())
		p.placeAudio(*outputPTS)
	}
	p.pcmCue = nil
	p.noteStart("video")
	return false, false
}

// cuePCM decodes the PCM a start on a program tick hands over with its
// first picture ahead of the start: a period of it, a 90 kHz tick and a
// sample over, so it reaches the slot after however its PTS rounds.
func (p *Player) cuePCM(speed float64) {
	if p.pcm == nil || speed != 1 {
		return
	}
	p.startPCM(0, 0)
	n := (90000*p.rate.Den/p.rate.Num+1)*int64(p.audioRate())/90000 + 2
	p.pcmCue, p.pcmCueLoop = p.pcm.Read(int(n)), p.loop.Load()
}

// awaitRelease holds a cued player's first slot h until Release or
// StartAt, putting its picture out once a second meanwhile at the read
// point of the program tick running then. onTick reports that StartAt
// put h out.
func (p *Player) awaitRelease(ctx context.Context, h *heldSlot) (onTick, cancelled bool) {
	if !p.released() {
		p.cuePCM(h.speed)
	}
	p.startMu.Lock()
	if p.released() {
		p.preroll = "waited"
		p.startMu.Unlock()
		return false, false
	}
	p.held = h
	p.startMu.Unlock()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		// Under startMu, so no still follows the slot StartAt put out.
		p.startMu.Lock()
		if !p.released() {
			p.putOut(h, timelineRead(p.rate, clock.TAINow()))
		}
		p.startMu.Unlock()
		select {
		case <-ctx.Done():
			cancelled = true
		case <-p.release:
		case <-ticker.C:
			continue
		}
		p.startMu.Lock()
		p.held = nil
		onTick = p.onTick && !cancelled
		p.startMu.Unlock()
		return onTick, cancelled
	}
}

// noteStart reports the first output of media to OnStart.
func (p *Player) noteStart(media string) {
	if p.config.OnStart == nil {
		return
	}
	p.config.OnStart(media, time.Since(time.Unix(0, p.releasedAt.Load())), p.preroll)
}

// keepOnTimeline moves a timed player's PTS, its video and its AAC, onto
// the clock when the frame about to go out is due more than
// ReadDelay periods ago, as after a pause or a decoder that fell
// behind, or more than a period ahead, as after a seek whose decode-only
// frames advanced the PTS: the tick due to show such a frame has passed
// or is still far off. Otherwise the PTS run on unbroken. The PCM is not
// moved: it runs ahead of the video by itself (emitTimedPCM).
func (p *Player) keepOnTimeline(outputPTS *int64) {
	period := 90000 * p.rate.Den / p.rate.Num
	off := clock.NowPTS() - *outputPTS
	if off <= int64(clock.ReadDelay(p.rate))*period && off >= -period {
		return
	}
	*outputPTS += off
	p.outputAudioPTS += off
	p.audioPassStart += off
}

// emitTimedAudio hands over a timed player's audio up to an AAC frame past
// until, the PTS of the next video slot, so it is buffered before the
// read that needs it. pic is the pictures of the pass put out so far, of
// total. The audio runs on from pass to pass of a loop as one stream: a
// pass of it ends at the AAC frame nearest to where the video's pass
// does, so the two never drift apart, and only audio that ends more than
// an AAC frame short of the video's pass is moved onto it. At a speed other
// than 1 no audio goes out; on the return to 1 and after a seek the audio
// is placed on the video again, at the picture's share of it.
func (p *Player) emitTimedAudio(speed float64, pic, total int, until int64) {
	audioClip := p.config.AudioClip
	if len(audioClip) == 0 || p.config.AudioOutput == nil || total == 0 {
		return
	}
	if speed != 1 {
		p.audioResync = true
		return
	}
	if p.audioResync {
		p.audioResync = false
		p.audioIdx = pic * len(audioClip) / total
		ticks, rem := p.audioClipTicks(p.audioIdx)
		p.outputAudioPTS, p.audioPassStart, p.audioRem = until, until-ticks, rem
	}
	passTicks := int64(p.totalOutputFrames) * 90000 * p.rate.Den / p.rate.Num
	frame := aacFrameTicks(audioClip[0])
	for {
		end := p.audioPassStart + passTicks
		loop := p.loop.Load()
		if p.audioIdx >= len(audioClip) || (loop && passTicks > 0 && p.outputAudioPTS+frame/2 >= end) {
			if !loop {
				return
			}
			p.audioIdx = 0
			if d := p.outputAudioPTS - end; d > frame || -d > frame {
				p.outputAudioPTS, p.audioRem = end, 0
			}
			p.audioPassStart = end
		}
		af := &audioClip[p.audioIdx]
		if p.outputAudioPTS >= until+aacFrameTicks(*af) {
			return
		}
		p.config.AudioOutput(af.data, p.outputAudioPTS, af.sampleRate, af.channels)
		if !p.audioOut {
			p.audioOut = true
			p.noteStart("audio")
		}
		p.outputAudioPTS += audioStep(&p.audioRem, af.sampleRate)
		p.audioIdx++
	}
}

// pcmLead is how far ahead of the clock a timed player hands over its
// PCM: a player that stalls for less, as on a slow decode, keeps the
// mixer supplied, and its audio runs on unbroken. The mixer holds 640 ms
// of a source.
const pcmLead = 150 * time.Millisecond

// emitTimedPCM hands over a timed player's PCM up to upto, normally
// pcmLead past the clock, and a sample past it, which a read ending on
// upto may reach for its rounding. pic is the pictures of the pass put
// out so far, of total. The audio runs on by itself: a video that falls
// behind and is put back on the clock (keepOnTimeline) leaves it where
// it is. At a speed other than 1 none goes out; on the return to 1,
// after a seek or a pause, and when the audio itself fell behind the
// read point, the stream starts again at the picture's share of the
// pass, on until, the PTS of the next video slot.
func (p *Player) emitTimedPCM(speed float64, pic, total int, until, upto int64) {
	s := p.pcm
	if s == nil || total == 0 {
		return
	}
	if speed != 1 {
		p.pcmResync = true
		return
	}
	if p.pcmEnded && !p.pcmResync {
		return
	}
	rate := int64(p.audioRate())
	now := clock.NowPTS()
	period := 90000 * p.rate.Den / p.rate.Num
	switch {
	case p.pcmFirst:
		p.pcmFirst, p.pcmResync = false, false
		p.startPCM(p.pcmBase, 0)
	case p.pcmResync, p.pcmBase+p.pcmPut*90000/rate < now-int64(clock.ReadDelay(p.rate))*period:
		p.pcmResync = false
		p.startPCM(until, int64(pic)*p.pcmPass(false)/int64(total))
	}
	want := (upto-p.pcmBase)*rate/90000 + 1
	for p.pcmPut < want {
		pcm := s.Read(int(min(want-p.pcmPut, 1024)))
		if len(pcm) == 0 {
			p.pcmEnded = true
			return
		}
		p.config.PCMOutput(pcm, p.pcmBase+p.pcmPut*90000/rate, s.Channels(), !p.pcmStarted)
		p.pcmStarted = true
		p.pcmPut += int64(len(pcm) / s.Channels())
	}
}

// startPCM starts the PCM stream at clip sample pos of the pass under
// way, its first sample stamped base.
func (p *Player) startPCM(base, pos int64) {
	p.pcmBase, p.pcmPut, p.pcmAcc, p.pcmEnded = base, 0, 0, false
	p.pcmVideoEnd = p.pcmPass(true)
	p.pcmAudioEnd = p.pcmVideoEnd
	if !p.loop.Load() {
		p.pcmAudioEnd = p.pcm.Samples()
	}
	p.pcm.Start(pos, p.pcmAudioEnd, p.nextPCMPass)
}

// pcmPass returns how many clip samples a pass of the video lasts at
// speed 1, carrying the remainder into the next pass when advance is
// set.
func (p *Player) pcmPass(advance bool) int64 {
	acc := p.pcmAcc + int64(p.totalOutputFrames)*int64(p.pcm.SourceRate())*p.rate.Den
	n := acc / p.rate.Num
	if advance {
		p.pcmAcc = acc - n*p.rate.Num
	}
	return n
}

// nextPCMPass gives the PCM stream the length of its next pass: to the
// end of the video's, so audio that ran past the video's last pass, as a
// pass played to the audio's end before the loop was set does, is taken
// off this one and the passes line up again. False when not looping.
func (p *Player) nextPCMPass() (int64, bool) {
	if !p.loop.Load() {
		return 0, false
	}
	p.pcmVideoEnd += p.pcmPass(true)
	n := max(1, p.pcmVideoEnd-p.pcmAudioEnd)
	p.pcmAudioEnd += n
	return n, true
}

// audioRate is the rate of the player's PCM.
func (p *Player) audioRate() int {
	if p.config.AudioRate > 0 {
		return p.config.AudioRate
	}
	return 48000
}

// audioStep returns the 90 kHz duration of the next AAC frame at
// sampleRate, carrying the remainder in rem so a rate that does not
// divide 90000*1024 does not drift. The demuxers give every frame its
// rate.
func audioStep(rem *int64, sampleRate int) int64 {
	*rem += 1024 * 90000
	d := *rem / int64(sampleRate)
	*rem -= d * int64(sampleRate)
	return d
}

// audioClipTicks returns the 90 kHz duration of the first n AAC frames of
// the audio clip, as the steps of a pass over them add up, and the
// remainder carried after them.
func (p *Player) audioClipTicks(n int) (ticks, rem int64) {
	for _, af := range p.config.AudioClip[:n] {
		ticks += audioStep(&rem, af.sampleRate)
	}
	return ticks, rem
}

// timelineStart returns the PTS of a timed player's first frame when it
// starts at TAI now on an output at rate r, on no program tick (StartAt):
// the read point of the next tick, or of the one after it when the next
// starts within half a period. The frame goes out after it is stamped and
// has to reach the source before its tick reads it: a frame that misses
// it is passed over for the next one, while its audio plays. Half a
// period scales that time with the work per frame, which a pipeline at a
// higher rate does in a shorter period anyway.
func timelineStart(r clock.Rate, now int64) int64 {
	i := tickAt(r, now) + 1
	next := clock.IndexToTimestamp(r, i)
	if 2*(next-now) < next-clock.IndexToTimestamp(r, i-1) {
		i++
	}
	return clock.IndexPTS(r, i-clock.ReadDelay(r))
}

// timelineRead returns the read point of the program tick running at TAI
// now, on an output at rate r.
func timelineRead(r clock.Rate, now int64) int64 {
	return clock.IndexPTS(r, tickAt(r, now)-clock.ReadDelay(r))
}

// tickAt returns the program tick running at TAI now: the grain that
// started last, as the reference clock ticks at a grain's start.
func tickAt(r clock.Rate, now int64) uint64 {
	i := clock.TimestampToIndex(r, now)
	if clock.IndexToTimestamp(r, i) > now {
		i--
	}
	return i
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
			// Timed, each repeat is due at the tick running: stamped a
			// second on from the last, it could be due ticks later and
			// leave the source with nothing new to show until then.
			if p.config.Timed {
				*outputPTS = timelineRead(p.rate, clock.TAINow())
			}
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

// reencodeForBrowser re-encodes a decoded YUV frame to browser-compatible
// format for relay output. Creates the encoder lazily on first call.
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

	// FFmpeg encoder returns Annex B — convert to wire format for the relay (reuses buffer).
	p.avc1Buf = codec.AnnexBToWireInto(encoded, p.avc1Buf[:0])
	// Deep-copy: downstream consumers (BroadcastVideo) retain frame references
	// in the GOP cache and send them asynchronously via channels. Without a copy,
	// the next encode cycle overwrites the aliased scratch buffer.
	wire := make([]byte, len(p.avc1Buf))
	copy(wire, p.avc1Buf)

	// Extract SPS/PPS from keyframes for relay VideoInfo.
	// The re-encoder always produces H.264 output for browser compatibility,
	// so we use H.264 NALU type parsing here.
	if encKF {
		for _, nalu := range codec.ExtractNALUs(wire) {
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

	return wire, encKF, sps, pps
}

// estimateFPSFromClipFrames estimates FPS from buffered frame PTS values.
// Frames may be in decode order (not PTS order) due to B-frames, so we
// find the actual min/max PTS rather than assuming first/last ordering.
func estimateFPSFromClipFrames(clip []BufferedFrame) float64 {
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

// ptsStep advances a 90 kHz PTS by one output period of den/num seconds,
// carrying the remainder, so a period that is not a whole number of ticks
// (1501.5 at 59.94 fps) does not drift against the audio.
type ptsStep struct{ num, den, rem int64 }

func (s *ptsStep) next() int64 {
	s.rem += 90000 * s.den
	d := s.rem / s.num
	s.rem -= d * s.num
	return d
}

// cadence maps a pass's pictures onto output slots: a slot shows the
// picture whose time span holds the slot's start, so picture k occupies
// slots start(k) to start(k+1). p/q is output slots per picture.
type cadence struct{ p, q int64 }

// newCadence returns the cadence of a clip at srcNum/srcDen fps played at
// speed on an output at outNum/outDen fps.
func newCadence(outNum, outDen, srcNum, srcDen int, speed float64) cadence {
	milli := int64(math.Round(speed * 1000))
	if milli < 1 {
		milli = 1
	}
	return cadence{p: int64(outNum) * int64(srcDen) * 1000, q: int64(outDen) * int64(srcNum) * milli}
}

// start is the first output slot of picture k.
func (c cadence) start(k int) int { return int((int64(k)*c.p + c.q - 1) / c.q) }

// slots is how many output slots picture k occupies: 0 for a picture
// between two slots, as when 60 fps meets 50.
func (c cadence) slots(k int) int { return c.start(k+1) - c.start(k) }

// clipRational returns the clip's rate as a fraction: the broadcast rate it
// is within 1% of, so 29.97 fps is exactly 30000/1001, else milli-fps.
func clipRational(fps float64) (num, den int) {
	num, den = fpsToRational(fps)
	if math.Abs(float64(num)/float64(den)-fps) <= fps/100 {
		return num, den
	}
	return int(math.Round(fps * 1000)), 1000
}
