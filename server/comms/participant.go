package comms

import (
	"math"
	"sync"
)

// speakingThresholdRMS is roughly -40 dBFS for int16: 32768 * 10^(-40/20).
const speakingThresholdRMS = 328

// participant represents a single operator in the voice comms channel,
// managing their Opus codec pair, PCM buffer, and speaking state.
type participant struct {
	id   string
	name string

	mu       sync.Mutex
	muted    bool
	speaking bool

	encoder *opusEncoder
	decoder *opusDecoder
	pcmBuf  []int16
	hasPCM  bool

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
		sendCh:  make(chan []byte, 4),
	}, nil
}

// decodeAudio decodes Opus data into PCM, stores it in the participant's buffer,
// and returns the decoded samples.
func (p *participant) decodeAudio(opusData []byte) ([]int16, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	n, err := p.decoder.Decode(opusData, p.pcmBuf, FrameSize)
	if err != nil {
		return nil, err
	}
	p.hasPCM = true
	out := make([]int16, n)
	copy(out, p.pcmBuf[:n])
	return out, nil
}

// consumePCM returns a copy of the buffered PCM data and clears the buffer flag.
// Returns nil if no data is available or the participant is muted.
func (p *participant) consumePCM() []int16 {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.hasPCM || p.muted {
		return nil
	}
	p.hasPCM = false
	out := make([]int16, FrameSize)
	copy(out, p.pcmBuf)
	return out
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

// close shuts down the participant's send channel.
func (p *participant) close() {
	close(p.sendCh)
}
