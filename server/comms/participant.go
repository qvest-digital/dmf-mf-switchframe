package comms

import (
	"math"
	"sync"
	"sync/atomic"
)

// opusEncoderAPI is the narrow interface exposed by the Opus encoder wrapper
// (see opus_cgo.go / opus_stub.go). Declared here so participants can be
// constructed with a mock encoder in tests.
type opusEncoderAPI interface {
	Encode(pcm []int16, frameSize int, buf []byte) (int, error)
}

// encoderFactory constructs an encoder for a new participant. The Manager
// holds a factory field so tests can inject tracking encoders; production
// always uses defaultEncoderFactory → newOpusEncoder.
type encoderFactory func() (opusEncoderAPI, error)

func defaultEncoderFactory() (opusEncoderAPI, error) {
	return newOpusEncoder(SampleRate, Channels)
}

// speakingThresholdRMS is roughly -40 dBFS for int16: 32768 * 10^(-40/20).
const speakingThresholdRMS = 328

// pcmQueueSize is the number of decoded PCM frames buffered per participant.
// At 20ms per frame, 8 frames = 160ms of buffer — enough to absorb jitter
// from the browser's ScriptProcessorNode (~85ms bursts of 4 frames).
const pcmQueueSize = 8

// participant represents a single operator in the voice comms channel,
// managing their Opus codec pair, PCM buffer, and speaking state.
type participant struct {
	id   string
	name string

	mu       sync.Mutex
	muted    bool
	speaking bool
	closed   bool
	// closedAtomic mirrors closed so the tracking mock encoder in
	// manager_test can observe the lifecycle bit without taking p.mu.
	// Written under p.mu together with closed so the two stay in lock-step.
	closedAtomic atomic.Bool

	encoder opusEncoderAPI
	decoder *opusDecoder
	pcmBuf  []int16      // scratch buffer for Opus decode
	pcmQ    chan []int16 // queued decoded PCM frames for mix loop

	sendCh chan []byte
}

// newParticipant creates a participant with encoder, decoder, PCM buffer, and send channel.
func newParticipant(id, name string) (*participant, error) {
	return newParticipantWithFactory(id, name, defaultEncoderFactory)
}

// newParticipantWithFactory builds a participant using the supplied encoder
// factory. The Manager uses this form so tests can inject tracking encoders
// that observe post-close call patterns.
func newParticipantWithFactory(id, name string, factory encoderFactory) (*participant, error) {
	enc, err := factory()
	if err != nil {
		return nil, err
	}
	dec, err := newOpusDecoder(SampleRate, Channels)
	if err != nil {
		return nil, err
	}
	return &participant{
		id:      id,
		name:    name,
		encoder: enc,
		decoder: dec,
		pcmBuf:  make([]int16, FrameSize),
		pcmQ:    make(chan []int16, pcmQueueSize),
		sendCh:  make(chan []byte, 4),
	}, nil
}

// decodeAudio decodes Opus data into PCM and queues the frame for the mix loop.
// Returns the decoded samples (for speaking detection).
func (p *participant) decodeAudio(opusData []byte) ([]int16, error) {
	p.mu.Lock()
	n, err := p.decoder.Decode(opusData, p.pcmBuf, FrameSize)
	if err != nil {
		p.mu.Unlock()
		return nil, err
	}
	// Copy while still holding the lock so pcmBuf can't be overwritten
	// by a concurrent decode (single-writer today, but defensive).
	out := make([]int16, n)
	copy(out, p.pcmBuf[:n])
	p.mu.Unlock()

	// Non-blocking enqueue — drop oldest if full.
	select {
	case p.pcmQ <- out:
	default:
		// Queue full — drop oldest frame to make room.
		select {
		case <-p.pcmQ:
		default:
		}
		select {
		case p.pcmQ <- out:
		default:
		}
	}

	return out, nil
}

// consumePCM returns the next queued PCM frame for mixing.
// Returns nil if no frames are available or the participant is muted.
func (p *participant) consumePCM() []int16 {
	p.mu.Lock()
	muted := p.muted
	p.mu.Unlock()

	if muted {
		// Drain queue while muted so we don't play stale audio on unmute.
		for {
			select {
			case <-p.pcmQ:
			default:
				return nil
			}
		}
	}

	select {
	case pcm := <-p.pcmQ:
		return pcm
	default:
		return nil
	}
}

// setMuted sets the participant's mute state.
func (p *participant) setMuted(muted bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.muted = muted
}

// updateSpeaking computes the RMS of the PCM samples and sets speaking state.
func (p *participant) updateSpeaking(pcm []int16) {
	if len(pcm) == 0 {
		return
	}

	var sumSq float64
	for _, s := range pcm {
		sumSq += float64(s) * float64(s)
	}
	rms := math.Sqrt(sumSq / float64(len(pcm)))

	p.mu.Lock()
	defer p.mu.Unlock()
	p.speaking = rms > speakingThresholdRMS
}

// info returns a broadcast-ready ParticipantInfo snapshot.
func (p *participant) info() ParticipantInfo {
	p.mu.Lock()
	defer p.mu.Unlock()
	return ParticipantInfo{
		OperatorID: p.id,
		Name:       p.name,
		Muted:      p.muted,
		Speaking:   p.speaking,
	}
}

// SendCh returns the channel for receiving encoded mix packets to send to this participant.
func (p *participant) SendCh() <-chan []byte {
	return p.sendCh
}

// trySend attempts a non-blocking send to the participant's send channel.
// Returns false if the channel is full or the participant is closed.
// Safe to call concurrently with close(). Holds mutex through the send
// to prevent TOCTOU race with close() (sendCh is buffered, so the
// non-blocking select never blocks under the lock).
func (p *participant) trySend(data []byte) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false
	}
	select {
	case p.sendCh <- data:
		return true
	default:
		return false
	}
}

// close shuts down the participant's send channel.
// Must not be called concurrently with itself.
func (p *participant) close() {
	p.mu.Lock()
	p.closed = true
	p.closedAtomic.Store(true)
	p.mu.Unlock()
	close(p.sendCh)
}

// encodeAndSend atomically checks the participant is not closed, runs the
// Opus encoder on the supplied mix, and non-blocking-sends the resulting
// packet to the sendCh. Holding p.mu across the encode call is the guarantee
// that closes this item's TOCTOU window: participant.close() cannot observe
// a half-finished encode, and a future commit that destroys the cgo encoder
// inside close() remains safe because the destroy sequences after this
// method's p.mu release.
//
// INVARIANT: opusEncoderAPI implementations MUST NOT call back into
// participant methods that acquire p.mu (trySend, setMuted, info,
// updateSpeaking, decodeAudio) — p.mu is held across Encode and sync.Mutex
// is not reentrant. Real opusEncoder is pure cgo and satisfies this
// naturally; test encoders must too.
//
// Returns nil (not an error) when the participant is already closed — the
// mix loop should not log a warning for what is a normal Leave race. Real
// encoder errors still propagate for the caller to log.
func (p *participant) encodeAndSend(mix []int16, encodeBuf []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	n, err := p.encoder.Encode(mix, FrameSize, encodeBuf)
	if err != nil {
		return err
	}
	packet := make([]byte, n)
	copy(packet, encodeBuf[:n])
	// Non-blocking send — drop if channel full. sendCh is buffered so the
	// select never blocks under the lock (same invariant trySend relies on).
	select {
	case p.sendCh <- packet:
	default:
	}
	return nil
}
