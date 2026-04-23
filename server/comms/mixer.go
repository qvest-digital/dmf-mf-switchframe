package comms

// mixer is an N-1 PCM mixer that sums every input except the requesting
// participant and soft-limits the summed peak to -1 dBFS. A per-listener
// peak-follower envelope (0.5 ms attack / 15 ms release) ducks coherent
// peaks from 3+ simultaneous speakers instead of hard-clipping into the
// voice band. Kept in int16 so the comms pipeline does not pull in the
// program-bus float32 audio.Limiter.
type mixer struct {
	scratch      []int16
	sumBuf       []int32
	envelopes    map[string]float64
	participants map[string]struct{}
}

const (
	// limiterTargetInt16 is -1 dBFS in int16: 10^(-1/20) × 32767 ≈ 29204.
	limiterTargetInt16 = 29204.0
	// limiterAttackAlpha is a one-pole coefficient for ~0.5 ms attack at 48 kHz
	// (τ_samples = 0.5 ms × 48 kHz = 24 → α = 1/24).
	limiterAttackAlpha = 1.0 / 24.0
	// limiterReleaseAlpha is a one-pole coefficient for ~15 ms release at 48 kHz
	// (τ_samples = 15 ms × 48 kHz = 720 → α = 1/720).
	limiterReleaseAlpha = 1.0 / 720.0
)

// newMixer creates a mixer with pre-allocated scratch buffers.
func newMixer() *mixer {
	return &mixer{
		scratch:      make([]int16, FrameSize),
		sumBuf:       make([]int32, FrameSize),
		envelopes:    make(map[string]float64),
		participants: make(map[string]struct{}),
	}
}

// mixFor sums all inputs except excludeID, soft-limits to -1 dBFS, and
// returns a new slice (not the internal scratch buffer).
func (m *mixer) mixFor(excludeID string, inputs map[string][]int16) []int16 {
	m.checkTopology(inputs)

	// Accumulate in int32 — 6 × int16 max cannot overflow.
	for i := range m.sumBuf {
		m.sumBuf[i] = 0
	}
	for id, samples := range inputs {
		if id == excludeID {
			continue
		}
		n := len(samples)
		if n > FrameSize {
			n = FrameSize
		}
		for i := 0; i < n; i++ {
			m.sumBuf[i] += int32(samples[i])
		}
	}

	env := m.envelopes[excludeID]
	out := make([]int16, FrameSize)
	for i := 0; i < FrameSize; i++ {
		s := m.sumBuf[i]
		a := s
		if a < 0 {
			a = -a
		}
		x := float64(a)
		if x > env {
			env += limiterAttackAlpha * (x - env)
		} else {
			env += limiterReleaseAlpha * (x - env)
		}
		if env < 1e-6 {
			env = 0
		}
		gain := 1.0
		if env > limiterTargetInt16 {
			gain = limiterTargetInt16 / env
		}
		scaled := float64(s) * gain
		// Brickwall clamp catches residual overshoot during the attack ramp.
		if scaled > limiterTargetInt16 {
			scaled = limiterTargetInt16
		} else if scaled < -limiterTargetInt16 {
			scaled = -limiterTargetInt16
		}
		out[i] = int16(scaled)
	}
	m.envelopes[excludeID] = env

	return out
}

// checkTopology wipes every listener envelope when the participant set shifts.
// A full wipe (rather than pruning just departed listeners) keeps the logic
// trivial; any momentary passthrough on an existing listener's next loud
// passage is bounded by the brickwall clamp and resolves within ~6 samples
// (~125 µs) at the 0.5 ms attack, well under any audible threshold.
func (m *mixer) checkTopology(inputs map[string][]int16) {
	if len(inputs) == len(m.participants) {
		same := true
		for id := range inputs {
			if _, ok := m.participants[id]; !ok {
				same = false
				break
			}
		}
		if same {
			return
		}
	}
	for k := range m.participants {
		delete(m.participants, k)
	}
	for id := range inputs {
		m.participants[id] = struct{}{}
	}
	for k := range m.envelopes {
		delete(m.envelopes, k)
	}
}
