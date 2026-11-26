package scte104

import (
	"errors"
	"fmt"
)

// RFC 8331 ancillary data as an MXL video/smpte291 grain holds it: the RTP
// payload from the Length field on, without the Extended Sequence Number.
//
//	Length (16)  ANC_Count (8)  F (2)  reserved (22)
//	per packet:  C (1)  Line_Number (11)  Horizontal_Offset (12)
//	             S (1)  StreamNum (7)
//	             DID, SDID, Data_Count, UDW..., Checksum_Word as 10-bit words
//	             word_align: zero bits up to a 32-bit boundary
//
// Length counts the octets from the first packet's C bit on. The 32-bit
// alignment counts from the start of the RTP payload, two bytes ahead of
// Length, so in a grain every packet starts at an offset of 2 modulo 4.

// rfc8331HeaderLen is Length, ANC_Count, F and the reserved bits.
const rfc8331HeaderLen = 6

// ErrRFC8331Malformed indicates ancillary data that does not follow RFC 8331:
// a length past the grain, a word with wrong parity or a wrong checksum.
var ErrRFC8331Malformed = errors.New("rfc8331: malformed ancillary data")

// ANCPacket is one ST 291 packet of an RFC 8331 payload.
type ANCPacket struct {
	Chroma           bool   // C: carried in the color-difference channel
	Line             uint16 // Line_Number
	HorizontalOffset uint16 // Horizontal_Offset
	StreamFlag       bool   // S: StreamNum is meaningful
	StreamNum        uint8
	DID              uint8
	SDID             uint8
	UDW              []byte // user data words, 8 data bits each
}

// ParseRFC8331 returns the ANC packets of an RFC 8331 payload, in order.
// Bytes past Length are ignored: an MXL grain is read at its full size.
// Every word's parity and every packet's checksum are verified, since a
// mismatch means the words are read out of step with the packing.
func ParseRFC8331(grain []byte) ([]ANCPacket, error) {
	if len(grain) < rfc8331HeaderLen {
		return nil, fmt.Errorf("%w: header needs %d bytes, got %d",
			ErrRFC8331Malformed, rfc8331HeaderLen, len(grain))
	}
	length := int(grain[0])<<8 | int(grain[1])
	count := int(grain[2])
	end := rfc8331HeaderLen + length
	if end > len(grain) {
		return nil, fmt.Errorf("%w: Length %d past the %d bytes after the header",
			ErrRFC8331Malformed, length, len(grain)-rfc8331HeaderLen)
	}
	if grain[3]>>6 == 0b01 {
		return nil, fmt.Errorf("%w: F is 0b01", ErrRFC8331Malformed)
	}

	r := bitReader{data: grain[:end], pos: rfc8331HeaderLen * 8}
	packets := make([]ANCPacket, 0, count)
	for i := range count {
		p, err := r.packet()
		if err != nil {
			return nil, fmt.Errorf("%w: packet %d: %v", ErrRFC8331Malformed, i, err)
		}
		packets = append(packets, p)
	}
	return packets, nil
}

type bitReader struct {
	data []byte
	pos  int // in bits
}

var errRFC8331Short = errors.New("past Length")

func (r *bitReader) bits(n int) (uint16, error) {
	if r.pos+n > len(r.data)*8 {
		return 0, errRFC8331Short
	}
	var v uint16
	for range n {
		v = v<<1 | uint16(r.data[r.pos/8]>>(7-r.pos%8)&1)
		r.pos++
	}
	return v, nil
}

// word reads one 10-bit data word, checks its parity and adds its nine
// low bits to sum.
func (r *bitReader) word(sum *uint16) (byte, error) {
	w, err := r.bits(10)
	if err != nil {
		return 0, err
	}
	if w != parityWord(byte(w)) {
		return 0, fmt.Errorf("word 0x%03X fails parity", w)
	}
	*sum += w & 0x1FF
	return byte(w), nil
}

func (r *bitReader) packet() (ANCPacket, error) {
	var p ANCPacket
	var f [5]uint16
	for i, width := range []int{1, 11, 12, 1, 7} {
		v, err := r.bits(width)
		if err != nil {
			return p, err
		}
		f[i] = v
	}
	p.Chroma, p.Line, p.HorizontalOffset = f[0] != 0, f[1], f[2]
	p.StreamFlag, p.StreamNum = f[3] != 0, uint8(f[4])

	var err error
	var sum uint16
	if p.DID, err = r.word(&sum); err != nil {
		return p, err
	}
	if p.SDID, err = r.word(&sum); err != nil {
		return p, err
	}
	dc, err := r.word(&sum)
	if err != nil {
		return p, err
	}
	p.UDW = make([]byte, dc)
	for i := range p.UDW {
		if p.UDW[i], err = r.word(&sum); err != nil {
			return p, err
		}
	}
	cs, err := r.bits(10)
	if err != nil {
		return p, err
	}
	if cs != checksumWord(sum) {
		return p, fmt.Errorf("checksum 0x%03X, computed 0x%03X", cs, checksumWord(sum))
	}

	// word_align: the next packet starts on a 32-bit boundary of the RTP
	// payload, which begins two bytes ahead of the grain.
	r.pos = ((r.pos+16+31)/32)*32 - 16
	return p, nil
}

// WrapRFC8331 builds an RFC 8331 payload, from Length on, carrying packets.
func WrapRFC8331(packets []ANCPacket) ([]byte, error) {
	if len(packets) > 255 {
		return nil, fmt.Errorf("rfc8331: %d packets, ANC_Count holds 255", len(packets))
	}
	w := bitWriter{data: make([]byte, rfc8331HeaderLen, 64)}
	for i, p := range packets {
		if len(p.UDW) > 255 {
			return nil, fmt.Errorf("rfc8331: packet %d has %d user data words, Data_Count holds 255", i, len(p.UDW))
		}
		if p.Line > 0x7FF || p.HorizontalOffset > 0xFFF || p.StreamNum > 0x7F {
			return nil, fmt.Errorf("rfc8331: packet %d has a field past its width", i)
		}
		w.put(boolBit(p.Chroma), 1)
		w.put(p.Line, 11)
		w.put(p.HorizontalOffset, 12)
		w.put(boolBit(p.StreamFlag), 1)
		w.put(uint16(p.StreamNum), 7)
		var sum uint16
		for _, b := range append([]byte{p.DID, p.SDID, byte(len(p.UDW))}, p.UDW...) {
			pw := parityWord(b)
			sum += pw & 0x1FF
			w.put(pw, 10)
		}
		w.put(checksumWord(sum), 10)
		w.flush()
		for (len(w.data)+2)%4 != 0 {
			w.data = append(w.data, 0)
		}
	}
	length := len(w.data) - rfc8331HeaderLen
	if length > 0xFFFF {
		return nil, fmt.Errorf("rfc8331: %d bytes of packets, Length holds 65535", length)
	}
	w.data[0], w.data[1], w.data[2] = byte(length>>8), byte(length), byte(len(packets))
	return w.data, nil
}

type bitWriter struct {
	data []byte
	acc  uint32
	n    uint
}

func (w *bitWriter) put(v uint16, width uint) {
	w.acc = w.acc<<width | uint32(v)
	w.n += width
	for w.n >= 8 {
		w.n -= 8
		w.data = append(w.data, byte(w.acc>>w.n))
	}
}

func (w *bitWriter) flush() {
	if w.n > 0 {
		w.data = append(w.data, byte(w.acc<<(8-w.n)))
		w.n = 0
	}
	w.acc = 0
}

func boolBit(b bool) uint16 {
	if b {
		return 1
	}
	return 0
}

// parityWord is v as a 10-bit ST 291 data word: even parity over the eight
// data bits in b8, its inverse in b9.
func parityWord(v byte) uint16 {
	w := uint16(v)
	ones := 0
	for b := v; b != 0; b &= b - 1 {
		ones++
	}
	if ones%2 == 1 {
		return w | 1<<8
	}
	return w | 1<<9
}

// checksumWord is the ST 291 Checksum_Word: the nine low bits of sum, with
// b9 the inverse of b8.
func checksumWord(sum uint16) uint16 {
	cs := sum & 0x1FF
	if cs&0x100 == 0 {
		cs |= 1 << 9
	}
	return cs
}
