package scte104

import (
	"encoding/hex"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/scte35"
)

// Grains as an independent RFC 8331 writer frames them, from Length on.
// The SCTE-104 packet sits on line 9 and carries payload descriptor 0x08
// (ST 2010 version 1) followed by vectorMOM.
const (
	// vectorMOM is a multiple_operation_message with one splice_request:
	// start normal, event 0x1234, program 0x22, pre-roll 4000 ms, break
	// 300 x 100 ms, avail 1 of 2, auto-return.
	vectorMOM = "ffff001e00000700000000010101000e010000123400220fa0012c010201"

	vectorNoPackets = "000000000000"
	vectorOneSCTE   = "003001000000009000009050747d08bfeff8021e8020041e008020080101405018010e40600802124d20088a0fa81014b10140901bb0"
	// vectorTwoPackets carries DID 0x61 SDID 0x01 (captions) on line 9
	// ahead of the SCTE-104 packet on line 10.
	vectorTwoPackets = "004002000000009000005850180e969a50199400000000a000009050747d08bfeff8021e8020041e008020080101405018010e40600802124d20088a0fa81014b10140901bb0"
)

// grainSize is the size an MXL data grain is read at, whatever it holds.
const grainSize = 4096

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	require.NoError(t, err)
	return b
}

// asGrain pads b to grainSize, as the MXL reader returns a grain.
func asGrain(b []byte) []byte {
	g := make([]byte, grainSize)
	copy(g, b)
	return g
}

func vectorUDW(t *testing.T) []byte {
	return append([]byte{0x08}, mustHex(t, vectorMOM)...)
}

func flipBit(b []byte, bit int) {
	b[bit/8] ^= 0x80 >> (bit % 8)
}

// firstWordBit is the bit offset of the first packet's DID word: the
// 6-byte header, then 32 bits of packet header.
const firstWordBit = 6*8 + 32

func TestParseRFC8331_NoPackets(t *testing.T) {
	for _, g := range [][]byte{mustHex(t, vectorNoPackets), asGrain(nil)} {
		packets, err := ParseRFC8331(g)
		require.NoError(t, err)
		require.Empty(t, packets)
	}
}

func TestParseRFC8331_OnePacket(t *testing.T) {
	packets, err := ParseRFC8331(asGrain(mustHex(t, vectorOneSCTE)))
	require.NoError(t, err)
	require.Equal(t, []ANCPacket{{
		Line: 9, DID: 0x41, SDID: 0x07, UDW: vectorUDW(t),
	}}, packets)
}

func TestParseRFC8331_TwoPackets(t *testing.T) {
	packets, err := ParseRFC8331(asGrain(mustHex(t, vectorTwoPackets)))
	require.NoError(t, err)
	require.Equal(t, []ANCPacket{
		{Line: 9, DID: 0x61, SDID: 0x01, UDW: []byte{0x96, 0x69, 0x01}},
		{Line: 10, DID: 0x41, SDID: 0x07, UDW: vectorUDW(t)},
	}, packets)
}

func TestParseRFC8331_PacketHeaderFields(t *testing.T) {
	want := []ANCPacket{{
		Chroma: true, Line: 0x7FE, HorizontalOffset: 0xABC,
		StreamFlag: true, StreamNum: 0x55, DID: 0x45, SDID: 0x01, UDW: []byte{},
	}}
	g, err := WrapRFC8331(want)
	require.NoError(t, err)
	got, err := ParseRFC8331(g)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestParseRFC8331_ParityError(t *testing.T) {
	g := mustHex(t, vectorOneSCTE)
	// Data bit b0 of the first user data word.
	flipBit(g, firstWordBit+3*10+9)

	_, err := ParseRFC8331(g)
	require.ErrorIs(t, err, ErrRFC8331Malformed)
	require.ErrorContains(t, err, "parity")
}

func TestParseRFC8331_ChecksumError(t *testing.T) {
	g := mustHex(t, vectorOneSCTE)
	// Change the first user data word from 0x08 to 0x09 with both parity
	// bits flipped along, so the word is valid but the sum is not.
	w := firstWordBit + 3*10
	flipBit(g, w+9)
	flipBit(g, w+1)
	flipBit(g, w)

	_, err := ParseRFC8331(g)
	require.ErrorIs(t, err, ErrRFC8331Malformed)
	require.ErrorContains(t, err, "checksum")
}

func TestParseRFC8331_Truncated(t *testing.T) {
	full := mustHex(t, vectorOneSCTE)
	for _, n := range []int{0, 3, 5, 6, 10, len(full) - 1} {
		_, err := ParseRFC8331(full[:n])
		require.ErrorIs(t, err, ErrRFC8331Malformed, "cut at %d bytes", n)
	}
}

func TestParseRFC8331_CountPastLength(t *testing.T) {
	g := asGrain(mustHex(t, vectorOneSCTE))
	g[2] = 2 // ANC_Count claims a second packet Length leaves no room for

	_, err := ParseRFC8331(g)
	require.ErrorIs(t, err, ErrRFC8331Malformed)
}

func TestParseRFC8331_InvalidField(t *testing.T) {
	g := mustHex(t, vectorOneSCTE)
	g[3] = 0x40 // F = 0b01

	_, err := ParseRFC8331(g)
	require.ErrorIs(t, err, ErrRFC8331Malformed)
}

// The writer must produce exactly what an independent RFC 8331 writer
// produces, alignment included.
func TestWrapRFC8331_MatchesVectors(t *testing.T) {
	udw := vectorUDW(t)
	g, err := WrapRFC8331(nil)
	require.NoError(t, err)
	require.Equal(t, vectorNoPackets, hex.EncodeToString(g))

	g, err = WrapRFC8331([]ANCPacket{{Line: 9, DID: 0x41, SDID: 0x07, UDW: udw}})
	require.NoError(t, err)
	require.Equal(t, vectorOneSCTE, hex.EncodeToString(g))

	g, err = WrapRFC8331([]ANCPacket{
		{Line: 9, DID: 0x61, SDID: 0x01, UDW: []byte{0x96, 0x69, 0x01}},
		{Line: 10, DID: 0x41, SDID: 0x07, UDW: udw},
	})
	require.NoError(t, err)
	require.Equal(t, vectorTwoPackets, hex.EncodeToString(g))
}

func TestWrapRFC8331_Limits(t *testing.T) {
	_, err := WrapRFC8331(make([]ANCPacket, 256))
	require.Error(t, err)
	_, err = WrapRFC8331([]ANCPacket{{UDW: make([]byte, 256)}})
	require.Error(t, err)
	_, err = WrapRFC8331([]ANCPacket{{Line: 0x800}})
	require.Error(t, err)
}

// A message goes out as the packet an independent writer frames, payload
// descriptor 0x08 included, and reads back as the same message.
func TestMessagePacket_MatchesVector(t *testing.T) {
	mom := mustHex(t, vectorMOM)
	p, err := MessagePacket(mom, 9)
	require.NoError(t, err)

	g, err := WrapRFC8331([]ANCPacket{p})
	require.NoError(t, err)
	require.Equal(t, vectorOneSCTE, hex.EncodeToString(g))

	msgs, err := ParseDataGrain(asGrain(g))
	require.NoError(t, err)
	require.Equal(t, [][]byte{mom}, msgs)
}

func TestMessagePacket_TooLarge(t *testing.T) {
	_, err := MessagePacket(make([]byte, st291MaxPayload), 9)
	require.NoError(t, err)
	_, err = MessagePacket(make([]byte, st291MaxPayload+1), 9)
	require.ErrorIs(t, err, ErrST291PayloadTooLarge)
}

func TestParseDataGrain_RFC8331(t *testing.T) {
	mom := mustHex(t, vectorMOM)

	msgs, err := ParseDataGrain(asGrain(mustHex(t, vectorNoPackets)))
	require.NoError(t, err)
	require.Empty(t, msgs)

	msgs, err = ParseDataGrain(asGrain(mustHex(t, vectorOneSCTE)))
	require.NoError(t, err)
	require.Equal(t, [][]byte{mom}, msgs)

	// The caption packet beside it is skipped without an error.
	msgs, err = ParseDataGrain(asGrain(mustHex(t, vectorTwoPackets)))
	require.NoError(t, err)
	require.Equal(t, [][]byte{mom}, msgs)
}

func TestParseDataGrain_TwoSCTE104Packets(t *testing.T) {
	g, err := WrapRFC8331([]ANCPacket{
		{Line: 9, DID: 0x41, SDID: 0x07, UDW: []byte{0x00, 0x01, 0x02}},
		{Line: 9, DID: 0x41, SDID: 0x07, UDW: []byte{0x08, 0x03}},
	})
	require.NoError(t, err)

	msgs, err := ParseDataGrain(asGrain(g))
	require.NoError(t, err)
	require.Equal(t, [][]byte{{0x01, 0x02}, {0x03}}, msgs)
}

func TestParseDataGrain_FragmentBesideWholeMessage(t *testing.T) {
	g, err := WrapRFC8331([]ANCPacket{
		{DID: 0x41, SDID: 0x07, UDW: []byte{st291PayloadDescFollowing, 0x01}},
		{DID: 0x41, SDID: 0x07, UDW: []byte{0x00, 0x02}},
	})
	require.NoError(t, err)

	msgs, err := ParseDataGrain(g)
	require.ErrorIs(t, err, ErrST291Fragmented)
	require.Equal(t, [][]byte{{0x02}}, msgs)
}

func TestParseDataGrain_Malformed(t *testing.T) {
	g := asGrain(mustHex(t, vectorOneSCTE))
	flipBit(g, firstWordBit+3*10+9)

	msgs, err := ParseDataGrain(g)
	require.ErrorIs(t, err, ErrRFC8331Malformed)
	require.Empty(t, msgs)
}

func TestParseDataGrain_ByteOriented(t *testing.T) {
	data := []byte{0x01, 0x02, 0x03}
	packet := mustWrap(t, data)

	// As written, and padded to the size a grain is read at.
	for _, g := range [][]byte{packet, asGrain(packet)} {
		msgs, err := ParseDataGrain(g)
		require.NoError(t, err)
		require.Equal(t, [][]byte{data}, msgs)
	}

	packet[len(packet)-1] ^= 0xFF
	_, err := ParseDataGrain(packet)
	require.ErrorIs(t, err, ErrST291BadChecksum)
}

// A grain starting 0x41 0x07 that is long enough to hold a Length of
// 0x4107 is RFC 8331.
func TestParseDataGrain_ByteOrientedBoundary(t *testing.T) {
	g := make([]byte, rfc8331HeaderLen+0x4107)
	g[0], g[1] = ST291DID, ST291SDID
	require.False(t, isByteOrientedST291(g))
	require.True(t, isByteOrientedST291(g[:len(g)-1]))

	msgs, err := ParseDataGrain(g)
	require.NoError(t, err) // ANC_Count 0
	require.Empty(t, msgs)
}

func TestPipeline_RFC8331SpliceRequest(t *testing.T) {
	msgs, err := ParseDataGrain(asGrain(mustHex(t, vectorTwoPackets)))
	require.NoError(t, err)
	require.Len(t, msgs, 1)

	msg, err := Decode(msgs[0])
	require.NoError(t, err)
	require.Equal(t, uint8(7), msg.MessageNumber)

	cue, err := ToCueMessage(msg)
	require.NoError(t, err)
	require.Equal(t, uint8(scte35.CommandSpliceInsert), cue.CommandType)
	require.Equal(t, uint32(0x1234), cue.EventID)
	require.True(t, cue.IsOut)
	require.Equal(t, "scheduled", cue.Timing)
	require.NotNil(t, cue.BreakDuration)
	require.Equal(t, 30*time.Second, *cue.BreakDuration)
	require.True(t, cue.AutoReturn)
	require.Equal(t, uint16(0x22), cue.UniqueProgramID)
	require.Equal(t, int64(4000), PreRollMs(msg))
}
