package codec

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// holdingEncoder models an encoder with one frame of output delay: the
// first send yields nothing, and the second makes both the held packet
// and its own packet ready at once.
type holdingEncoder struct {
	sends int
	held  *EncodedPacket
	ready []EncodedPacket
}

func (f *holdingEncoder) send(pts int64) {
	pkt := EncodedPacket{Data: []byte{byte(pts)}, PTS: pts, DTS: pts}
	f.sends++
	if f.sends == 1 {
		f.held = &pkt
		return
	}
	if f.held != nil {
		f.ready = append(f.ready, *f.held)
		f.held = nil
	}
	f.ready = append(f.ready, pkt)
}

func (f *holdingEncoder) receive() (EncodedPacket, bool, error) {
	if len(f.ready) == 0 {
		return EncodedPacket{}, false, nil
	}
	pkt := f.ready[0]
	f.ready = f.ready[1:]
	return pkt, true, nil
}

// A packet the encoder held back must come out with the PTS of the frame
// it encodes, not the PTS of the frame whose send released it, and a
// send that releases two packets must deliver both.
func TestDrainPackets_HeldPacketKeepsItsOwnPTS(t *testing.T) {
	enc := &holdingEncoder{}
	inputs := []int64{1000, 4000, 5500}

	var perCall [][]EncodedPacket
	var got []int64
	for _, pts := range inputs {
		enc.send(pts)
		pkts, err := drainPackets(enc.receive)
		require.NoError(t, err)
		perCall = append(perCall, pkts)
		for _, p := range pkts {
			got = append(got, p.PTS)
		}
	}

	require.Empty(t, perCall[0], "the first send is held by the encoder")
	require.Len(t, perCall[1], 2, "the second send releases the held packet and its own")
	require.Equal(t, inputs, got, "every packet carries its own frame's PTS, none lost")
}

func TestDrainPackets_StopsOnReceiveError(t *testing.T) {
	boom := errors.New("receive failed")
	calls := 0
	pkts, err := drainPackets(func() (EncodedPacket, bool, error) {
		calls++
		if calls == 1 {
			return EncodedPacket{Data: []byte{1}, PTS: 3000}, true, nil
		}
		return EncodedPacket{}, false, boom
	})
	require.ErrorIs(t, err, boom)
	require.Len(t, pkts, 1, "packets received before the error are returned")
}
