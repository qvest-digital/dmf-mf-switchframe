package fastctrl

import "encoding/binary"

const (
	MsgPing byte = 0xFE
	MsgPong byte = 0xFF
)

// BuildPong creates a pong datagram with the engine's PTP timestamp and
// the echoed ping sequence number for RTT correlation.
// Format: [0xFF][4 bytes: seq BE][8 bytes: PTP µs big-endian] = 13 bytes
func BuildPong(ptpMicroseconds int64, seq uint32) []byte {
	buf := make([]byte, 13)
	buf[0] = MsgPong
	binary.BigEndian.PutUint32(buf[1:5], seq)
	binary.BigEndian.PutUint64(buf[5:13], uint64(ptpMicroseconds))
	return buf
}

// ParsePing extracts an optional sequence number from a ping datagram.
// The type byte (0xFE) is already stripped by the dispatcher.
// Format: [] (empty) or [4 bytes: sequence number big-endian]
func ParsePing(data []byte) (seq uint32, err error) {
	if len(data) >= 4 {
		seq = binary.BigEndian.Uint32(data[:4])
	}
	return seq, nil
}
