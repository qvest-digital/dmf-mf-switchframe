package audio

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A source's audio stamped ahead of the read point, as audio placed by its
// own PTS arrives, is read on its stamps once the read point reaches it:
// silence before its first sample, then the audio sample for sample. The
// first read finding it more than a period ahead must not leave the ring
// to be read from the next read on as if it were on time, up to a period
// early for as long as the source plays.
func TestMixer_RingAheadOfTheReadPointIsReadOnItsStamps(t *testing.T) {
	const n = 960
	const periods = 6
	m := newProduceTestMixer(t)
	m.AddChannel("src")
	m.SetActive("src", true)

	tl := newTimeline()
	for k := 0; k < periods; k++ {
		pcm := make([]float32, n*2)
		for i := range pcm {
			pcm[i] = 0.1 * float32(k+1)
		}
		tl.push(m, "src", pcm, k*n)
	}

	// The ring starts just under two periods after the first read point.
	const ahead = 2*n - 25
	tl.read = tl.base - ahead
	for r := 0; r < 4; r++ {
		first := int(tl.read) - int(tl.base)
		out := tl.produce(m, n)
		require.Len(t, out, n*2)
		for j := 0; j < n; j++ {
			want := float32(0)
			if s := first + j; s >= 0 {
				want = 0.1 * float32(s/n+1)
			}
			require.InDelta(t, want, out[2*j], 0.001,
				"read %d sample %d (ring sample %d)", r, j, first+j)
		}
	}
	require.Zero(t, m.ChannelRealigns()["src"])
}
