package codec

// EncodedPacket is one access unit out of an encoder. PTS and DTS are in
// 90 kHz units and belong to the frame the packet encodes, which is not
// the frame just sent when the encoder holds input.
type EncodedPacket struct {
	Data       []byte
	PTS        int64
	DTS        int64
	IsKeyframe bool
}

// drainPackets calls receive until no packet is ready. One send can make
// several packets ready, and a packet left unread is paired with a later
// frame or never read at all.
func drainPackets(receive func() (EncodedPacket, bool, error)) ([]EncodedPacket, error) {
	var pkts []EncodedPacket
	for {
		pkt, ok, err := receive()
		if err != nil {
			return pkts, err
		}
		if !ok {
			return pkts, nil
		}
		pkts = append(pkts, pkt)
	}
}
