package comms

import (
	"math"
	"sync"
)

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

	encoder *opusEncoder
	decoder *opusDecoder
	pcmBuf  []int16      // scratch buffer for Opus decode
	pcmQ    chan []int16  // queued decoded PCM frames for mix loop

	sendCh chan []byte
}

// newParticipant creates a participant with encoder, decoder, PCM buffer, and send channel.
func newParticipant(id, name string) (*participant, error) {
	enc, err := newOpusEncoder(SampleRate, Channels)
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
	p.mu.Unlock()
	if err != nil {
		return nil, err
	}

	out := make([]int16, n)
	copy(out, p.pcmBuf[:n])

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

// ingestRawPCM stores raw PCM samples directly (bypassing Opus decode).
func (p *participant) ingestRawPCM(pcm []int16) {
	frame := make([]int16, FrameSize)
	n := len(pcm)
	if n > FrameSize {
		n = FrameSize
	}
	copy(frame[:n], pcm[:n])

	select {
	case p.pcmQ <- frame:
	default:
		select {
		case <-p.pcmQ:
		default:
		}
		select {
		case p.pcmQ <- frame:
		default:
		}
	}
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
// Safe to call concurrently with close().
func (p *participant) trySend(data []byte) bool {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return false
	}
	p.mu.Unlock()

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
	p.mu.Unlock()
	close(p.sendCh)
}
