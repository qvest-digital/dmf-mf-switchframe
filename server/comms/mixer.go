package comms

// mixer is a simple N-1 PCM mixer that sums all inputs except the
// requesting participant and clamps to int16 range.
type mixer struct {
	scratch []int16
}

// newMixer creates a mixer with a pre-allocated scratch buffer.
func newMixer() *mixer {
	return &mixer{
		scratch: make([]int16, FrameSize),
	}
}

// mixFor sums all inputs except excludeID, clamps to int16 range,
// and returns a new slice (not the internal scratch buffer).
func (m *mixer) mixFor(excludeID string, inputs map[string][]int16) []int16 {
	// Zero the scratch buffer.
	for i := range m.scratch {
		m.scratch[i] = 0
	}

	// Accumulate all sources except the excluded participant.
	for id, samples := range inputs {
		if id == excludeID {
			continue
		}
		n := len(samples)
		if n > FrameSize {
			n = FrameSize
		}
		for i := 0; i < n; i++ {
			sum := int32(m.scratch[i]) + int32(samples[i])
			// Clamp to int16 range.
			if sum > 32767 {
				sum = 32767
			} else if sum < -32768 {
				sum = -32768
			}
			m.scratch[i] = int16(sum)
		}
	}

	// Return a copy so callers don't alias the scratch buffer.
	out := make([]int16, FrameSize)
	copy(out, m.scratch)
	return out
}
